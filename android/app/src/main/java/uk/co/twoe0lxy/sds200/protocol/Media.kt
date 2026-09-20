package uk.co.twoe0lxy.sds200.protocol

/** G.711 µ-law decoding (identical to muLawToPCM in protocol.go). */
object MuLaw {
    private val TABLE = ShortArray(256) { decodeSample(it.toByte()).toShort() }

    fun decodeSample(b: Byte): Int {
        val u = b.toInt().inv() and 0xff
        val sign = u and 0x80
        val exponent = (u shr 4) and 0x07
        val mantissa = u and 0x0f
        var sample = ((mantissa shl 3) + 0x84) shl exponent
        sample -= 0x84
        if (sign != 0) sample = -sample
        return sample.coerceIn(-32768, 32767)
    }

    fun decode(b: Byte): Short = TABLE[b.toInt() and 0xff]

    fun decode(src: ByteArray, offset: Int, length: Int, dst: ShortArray, dstOffset: Int = 0) {
        for (i in 0 until length) dst[dstOffset + i] = TABLE[src[offset + i].toInt() and 0xff]
    }
}

/** RTP header view (RFC 3550). */
data class RtpPacket(
    val payloadOffset: Int,
    val payloadLength: Int,
    val sequence: Int,
    val timestamp: Long,
    val ssrc: Long,
    val payloadType: Int,
)

object Rtp {
    /** Returns null for malformed packets (never throws). */
    fun parse(d: ByteArray, length: Int = d.size): RtpPacket? {
        if (length < 12) return null
        val b0 = d[0].toInt() and 0xff
        if (b0 shr 6 != 2) return null
        val cc = b0 and 0x0f
        val ext = b0 and 0x10 != 0
        val padding = b0 and 0x20 != 0
        var off = 12 + 4 * cc
        if (length < off) return null
        if (ext) {
            if (length < off + 4) return null
            val n = ((d[off + 2].toInt() and 0xff) shl 8) or (d[off + 3].toInt() and 0xff)
            off += 4 + 4 * n
            if (length < off) return null
        }
        var end = length
        if (padding) {
            val pad = d[length - 1].toInt() and 0xff
            if (pad == 0 || pad > end - off) return null
            end -= pad
        }
        fun u8(i: Int) = d[i].toInt() and 0xff
        return RtpPacket(
            payloadOffset = off,
            payloadLength = end - off,
            sequence = (u8(2) shl 8) or u8(3),
            timestamp = ((u8(4).toLong() shl 24) or (u8(5).toLong() shl 16) or (u8(6).toLong() shl 8) or u8(7).toLong()),
            ssrc = ((u8(8).toLong() shl 24) or (u8(9).toLong() shl 16) or (u8(10).toLong() shl 8) or u8(11).toLong()),
            payloadType = u8(1) and 0x7f,
        )
    }

    /** 4-byte firewall/NAT keepalive (live555/VLC pattern). */
    val PUNCH = byteArrayOf(0xCE.toByte(), 0xFA.toByte(), 0xED.toByte(), 0xFE.toByte())
}

data class RtspResponse(val status: Int, val reason: String, val headers: Map<String, String>, val body: ByteArray) {
    fun header(name: String): String? = headers[name.lowercase()]
}

data class RtpTransport(val source: String, val serverPort: Int, val clientPort: Int, val ssrc: Long?)

class RtspException(message: String) : Exception(message)

/** Pure RTSP text handling, ported from audio.go. */
object Rtsp {
    const val DEFAULT_PORT = 554
    const val DEFAULT_PATH = "/au:scanner.au"

    fun aggregate(host: String, port: Int = DEFAULT_PORT, path: String = DEFAULT_PATH): String {
        val authority = if (port != DEFAULT_PORT) "$host:$port" else host
        return "rtsp://$authority$path"
    }

    fun buildRequest(method: String, uri: String, cseq: Int, headers: List<Pair<String, String>>, userAgent: String): String {
        val sb = StringBuilder()
        sb.append(method).append(' ').append(uri).append(" RTSP/1.0\r\n")
        sb.append("CSeq: ").append(cseq).append("\r\n")
        sb.append("User-Agent: ").append(userAgent).append("\r\n")
        for ((k, v) in headers) sb.append(k).append(": ").append(v).append("\r\n")
        sb.append("\r\n")
        return sb.toString()
    }

    /** Parses the status line and header block (without body). */
    fun parseHead(statusLine: String, headerLines: List<String>): Pair<Int, Pair<String, Map<String, String>>> {
        val p = statusLine.trimEnd('\r', '\n').split(' ', limit = 3)
        if (p.size < 2 || p[0] != "RTSP/1.0") throw RtspException("invalid RTSP status \"$statusLine\"")
        val status = p[1].toIntOrNull() ?: throw RtspException("invalid RTSP status code \"${p[1]}\"")
        val h = LinkedHashMap<String, String>()
        for (ln in headerLines) {
            val i = ln.indexOf(':')
            if (i < 0) throw RtspException("invalid RTSP header \"$ln\"")
            h[ln.substring(0, i).trim().lowercase()] = ln.substring(i + 1).trim()
        }
        return status to ((p.getOrNull(2) ?: "") to h)
    }

    /** Finds the control attribute of the PCMU (payload 0) audio media section. */
    fun parseSdpControl(body: String): String {
        var inAudio = false
        var hasPcmu = false
        var control = ""
        for (raw in body.replace("\r\n", "\n").split('\n')) {
            val ln = raw.trim()
            if (ln.startsWith("m=")) {
                val f = ln.removePrefix("m=").trim().split(Regex("\\s+"))
                inAudio = f.size >= 4 && f[0] == "audio" && f[2] == "RTP/AVP"
                if (inAudio && f.drop(3).contains("0")) hasPcmu = true
                continue
            }
            if (inAudio && ln.startsWith("a=control:")) control = ln.removePrefix("a=control:").trim()
        }
        if (!hasPcmu) throw RtspException("SDP does not advertise PCMU payload type 0")
        if (control.isEmpty()) throw RtspException("SDP has no audio control track")
        return control
    }

    /** SETUP URL for the track: absolute control, or content-base (or aggregate + "/") + track. */
    fun trackUrl(control: String, contentBase: String?, aggregate: String): String {
        if (control.startsWith("rtsp://", ignoreCase = true)) return control
        val base = contentBase?.takeIf { it.isNotBlank() } ?: "$aggregate/"
        return base.trimEnd('/') + "/" + control.trimStart('/')
    }

    fun sessionId(header: String?): String = header?.substringBefore(';')?.trim().orEmpty()

    fun parseTransport(v: String?, clientPort: Int, fallbackSource: String? = null): RtpTransport {
        if (v.isNullOrBlank()) throw RtspException("SETUP missing Transport header")
        var source = ""
        var serverPort = 0
        var ssrc: Long? = null
        for (part in v.split(';')) {
            val f = part.trim()
            val i = f.indexOf('=')
            if (i < 0) continue
            val k = f.substring(0, i).lowercase()
            var value = f.substring(i + 1)
            when (k) {
                "source" -> source = value
                "server_port" -> serverPort = value.substringBefore('-').trim().toIntOrNull() ?: 0
                "client_port" -> {
                    val got = value.substringBefore('-').trim().toIntOrNull() ?: 0
                    if (got != 0 && got != clientPort) throw RtspException("SETUP client_port $got does not match $clientPort")
                }
                "ssrc" -> {
                    var radix = 10
                    if (value.startsWith("0x", true) || value.any { it in 'a'..'f' || it in 'A'..'F' }) {
                        radix = 16
                        value = value.removePrefix("0x").removePrefix("0X")
                    }
                    ssrc = value.toLongOrNull(radix)?.takeIf { it in 0..0xFFFFFFFFL }
                }
            }
        }
        if (source.isEmpty()) source = fallbackSource.orEmpty()
        if (source.isEmpty()) throw RtspException("SETUP Transport missing source")
        if (serverPort == 0) throw RtspException("SETUP Transport missing server_port")
        return RtpTransport(source, serverPort, clientPort, ssrc)
    }
}
