package uk.co.twoe0lxy.sds200.audio

import uk.co.twoe0lxy.sds200.BuildConfig
import uk.co.twoe0lxy.sds200.protocol.RtpTransport
import uk.co.twoe0lxy.sds200.protocol.Rtsp
import uk.co.twoe0lxy.sds200.protocol.RtspException
import uk.co.twoe0lxy.sds200.protocol.RtspResponse
import java.io.BufferedInputStream
import java.io.ByteArrayOutputStream
import java.io.Closeable
import java.io.IOException
import java.io.InputStream
import java.io.OutputStream
import java.net.InetSocketAddress
import java.net.Socket

/**
 * Blocking RTSP/1.0 client for the scanner's single audio session (port of
 * RTSPClient in audio.go). The scanner allows ONE session, so every exit path
 * must go through [close], which sends TEARDOWN when a session exists.
 * Requests are serialised because keepalives and TEARDOWN come from different threads.
 */
class RtspClient(
    private val host: String,
    private val port: Int = Rtsp.DEFAULT_PORT,
    private val path: String = Rtsp.DEFAULT_PATH,
    private val timeoutMs: Int = 5000,
) : Closeable {
    private val lock = Any()
    private var socket: Socket? = null
    private var input: InputStream? = null
    private var output: OutputStream? = null
    private var cseq = 0

    @Volatile
    var session: String = ""
        private set

    val aggregate: String get() = Rtsp.aggregate(host, port, path)

    private val userAgent = "SDS200-Remote-Android/${BuildConfig.VERSION_NAME}"

    private fun connect() {
        if (socket != null) return
        val s = Socket()
        try {
            s.tcpNoDelay = true
            s.connect(InetSocketAddress(host, port), timeoutMs)
            s.soTimeout = timeoutMs
        } catch (e: IOException) {
            runCatching { s.close() }
            throw RtspException("RTSP connect to $host:$port failed: ${e.message}")
        }
        socket = s
        input = BufferedInputStream(s.getInputStream(), 16 * 1024)
        output = s.getOutputStream()
    }

    private fun request(method: String, uri: String, headers: List<Pair<String, String>> = emptyList()): RtspResponse =
        synchronized(lock) {
            val out = output ?: throw RtspException("RTSP not connected")
            val inp = input ?: throw RtspException("RTSP not connected")
            val seq = ++cseq
            try {
                out.write(Rtsp.buildRequest(method, uri, seq, headers, userAgent).toByteArray(Charsets.ISO_8859_1))
                out.flush()
                val res = readResponse(inp)
                val got = res.header("cseq")?.trim()
                if (got != null && got.isNotEmpty() && got != seq.toString()) throw RtspException("RTSP $method CSeq mismatch $got")
                if (res.status != 200) throw RtspException("RTSP $method failed with ${res.status} ${res.reason}")
                res
            } catch (e: IOException) {
                throw RtspException("RTSP $method: ${e.message ?: e.javaClass.simpleName}")
            }
        }

    private fun readLine(inp: InputStream): String {
        val sb = ByteArrayOutputStream(128)
        while (true) {
            val b = inp.read()
            if (b < 0) throw IOException("connection closed")
            if (b == '\n'.code) break
            sb.write(b)
            if (sb.size() > 8192) throw IOException("RTSP line too long")
        }
        return sb.toString(Charsets.ISO_8859_1.name()).trimEnd('\r')
    }

    private fun readResponse(inp: InputStream): RtspResponse {
        val status = readLine(inp)
        val headerLines = ArrayList<String>()
        while (true) {
            val l = readLine(inp)
            if (l.isEmpty()) break
            headerLines += l
            if (headerLines.size > 100) throw IOException("too many RTSP headers")
        }
        val (code, rest) = Rtsp.parseHead(status, headerLines)
        val (reason, headers) = rest
        val n = headers["content-length"]?.trim()?.toIntOrNull() ?: 0
        if (n < 0 || n > 4 * 1024 * 1024) throw IOException("RTSP content length out of range")
        val body = ByteArray(n)
        var off = 0
        while (off < n) {
            val r = inp.read(body, off, n - off)
            if (r < 0) throw IOException("connection closed in body")
            off += r
        }
        return RtspResponse(code, reason, headers, body)
    }

    /** OPTIONS -> DESCRIBE -> SETUP -> PLAY. On failure the session is torn down and closed. */
    fun start(clientPort: Int): RtpTransport {
        try {
            connect()
            request("OPTIONS", aggregate)
            val d = request("DESCRIBE", aggregate, listOf("Accept" to "application/sdp"))
            val ct = d.header("content-type")?.lowercase().orEmpty()
            if (ct.isNotEmpty() && !ct.startsWith("application/sdp")) throw RtspException("DESCRIBE returned content-type $ct")
            val control = Rtsp.parseSdpControl(String(d.body, Charsets.ISO_8859_1))
            val track = Rtsp.trackUrl(control, d.header("content-base"), aggregate)
            val s = request("SETUP", track, listOf("Transport" to "RTP/AVP;unicast;client_port=$clientPort"))
            val sid = Rtsp.sessionId(s.header("session"))
            if (sid.isEmpty()) throw RtspException("SETUP response missing Session")
            session = sid
            val tr = Rtsp.parseTransport(s.header("transport"), clientPort, host)
            request("PLAY", "$aggregate/", listOf("Session" to sid, "Range" to "npt=0.000-"))
            return tr
        } catch (e: Exception) {
            close()
            throw if (e is RtspException) e else RtspException(e.message ?: e.javaClass.simpleName)
        }
    }

    fun getParameter() {
        val sid = session
        if (sid.isEmpty()) throw RtspException("no RTSP session")
        request("GET_PARAMETER", "$aggregate/", listOf("Session" to sid))
    }

    /** Sends TEARDOWN (best effort) and closes the TCP connection. Idempotent. */
    override fun close() {
        val sid = session
        if (sid.isNotEmpty() && socket != null) {
            runCatching {
                socket?.soTimeout = 1500
                request("TEARDOWN", "$aggregate/", listOf("Session" to sid))
            }
        }
        session = ""
        synchronized(lock) {
            runCatching { socket?.close() }
            socket = null
            input = null
            output = null
        }
    }
}
