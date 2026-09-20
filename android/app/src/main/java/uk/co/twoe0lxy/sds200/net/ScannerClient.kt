package uk.co.twoe0lxy.sds200.net

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import uk.co.twoe0lxy.sds200.protocol.ChannelKind
import uk.co.twoe0lxy.sds200.protocol.GltParser
import uk.co.twoe0lxy.sds200.protocol.GltRecord
import uk.co.twoe0lxy.sds200.protocol.GsiParser
import uk.co.twoe0lxy.sds200.protocol.GstParser
import uk.co.twoe0lxy.sds200.protocol.GstStatus
import uk.co.twoe0lxy.sds200.protocol.PullParserFactory
import uk.co.twoe0lxy.sds200.protocol.Replies
import uk.co.twoe0lxy.sds200.protocol.ReplyAssembler
import uk.co.twoe0lxy.sds200.protocol.ScannerInfo
import uk.co.twoe0lxy.sds200.protocol.StsDisplay
import uk.co.twoe0lxy.sds200.protocol.StsParser
import uk.co.twoe0lxy.sds200.protocol.Waterfall
import java.io.IOException
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.SocketTimeoutException
import java.time.LocalDateTime

class ScannerException(message: String) : Exception(message)

data class CommandResult(val wire: String, val raw: String, val elapsedMs: Double)

data class ConnectionStatus(
    val host: String = "",
    val online: Boolean = false,
    val everConnected: Boolean = false,
    val latencyMs: Double? = null,
    val lastError: String? = null,
)

enum class HoldScope(val keys: List<Char>, val label: String) {
    SYSTEM(listOf('A'), "System"),
    DEPARTMENT(listOf('B'), "Dept"),
    SITE(listOf('F', 'B'), "Site"),
    CHANNEL(listOf('C'), "Channel");

    fun current(info: ScannerInfo): Boolean? = when (this) {
        SYSTEM -> info.systemHold
        DEPARTMENT -> info.departmentHold
        SITE -> info.siteHold
        CHANNEL -> info.channelHold
    }
}

/**
 * The single scanner control connection. Commands are strictly request/response
 * with no transaction ids, so every exchange is serialised with a Mutex.
 * All socket work runs on Dispatchers.IO.
 */
class ScannerClient(
    private val parserFactory: PullParserFactory,
    private val port: Int = UDP_PORT,
) {
    @Volatile
    var host: String = ""
        set(value) {
            val v = value.trim()
            if (v != field) {
                field = v
                _status.value = ConnectionStatus(host = v)
            }
        }

    private val mutex = Mutex()
    private val _status = MutableStateFlow(ConnectionStatus())
    val status: StateFlow<ConnectionStatus> = _status.asStateFlow()
    private var failures = 0

    suspend fun command(wire: String, timeoutMs: Int = DEFAULT_TIMEOUT_MS): CommandResult {
        val h = host
        if (h.isEmpty()) throw ScannerException("No scanner IP address set")
        if (wire.contains('\r') || wire.contains('\n')) throw ScannerException("Command must be a single line")
        return mutex.withLock {
            withContext(Dispatchers.IO) {
                try {
                    val r = exchange(h, wire, timeoutMs)
                    noteResult(h, r.elapsedMs, null)
                    r
                } catch (e: ScannerException) {
                    noteResult(h, null, e.message)
                    throw e
                } catch (e: IOException) {
                    val msg = "Network error: ${e.message ?: e.javaClass.simpleName}"
                    noteResult(h, null, msg)
                    throw ScannerException(msg)
                }
            }
        }
    }

    private fun noteResult(h: String, elapsed: Double?, error: String?) {
        if (h != host) return
        if (elapsed != null) {
            failures = 0
            _status.update { it.copy(host = h, online = true, everConnected = true, latencyMs = elapsed, lastError = null) }
        } else {
            failures++
            _status.update { it.copy(host = h, online = if (failures >= 3) false else it.online, lastError = error) }
        }
    }

    private fun exchange(h: String, wire: String, timeoutMs: Int): CommandResult {
        val addr = try {
            InetAddress.getByName(h)
        } catch (e: Exception) {
            throw ScannerException("Invalid scanner address \"$h\"")
        }
        DatagramSocket().use { s ->
            s.connect(InetSocketAddress(addr, port))
            val started = System.nanoTime()
            val deadline = started + timeoutMs * 1_000_000L
            val out = (wire + "\r").toByteArray(Charsets.ISO_8859_1)
            s.send(DatagramPacket(out, out.size))
            val asm = ReplyAssembler(wire)
            val buf = ByteArray(65_535)
            while (true) {
                val remainingMs = ((deadline - System.nanoTime()) / 1_000_000L).toInt()
                if (remainingMs <= 0) break
                s.soTimeout = remainingMs
                val p = DatagramPacket(buf, buf.size)
                try {
                    s.receive(p)
                } catch (_: SocketTimeoutException) {
                    break
                }
                val text = String(p.data, p.offset, p.length, Charsets.ISO_8859_1)
                asm.offer(text)?.let { return CommandResult(wire, it, elapsed(started)) }
            }
            asm.onTimeout()?.let { return CommandResult(wire, it, elapsed(started)) }
            throw ScannerException("No response to $wire")
        }
    }

    private fun elapsed(started: Long) = (System.nanoTime() - started) / 1_000_000.0

    // ------------------------------------------------------------ high level

    suspend fun model(): String = Replies.text(command("MDL").raw, "MDL") ?: throw ScannerException("Unexpected MDL reply")
    suspend fun firmware(): String = Replies.text(command("VER").raw, "VER") ?: throw ScannerException("Unexpected VER reply")

    suspend fun gsi(): ScannerInfo {
        val r = command("GSI", 1500)
        val info = GsiParser.parse(r.raw, parserFactory)
        if (info.parseError != null && info.mode.isEmpty() && info.system.isEmpty()) {
            throw ScannerException("GSI parse error: ${info.parseError}")
        }
        return info
    }

    suspend fun sts(): StsDisplay {
        val d = StsParser.parse(command("STS").raw)
        if (d.error != null) throw ScannerException("STS: ${d.error}")
        return d
    }

    /** Presses a key, then re-reads STS with backoff because the display blanks briefly after a key. */
    suspend fun key(code: Char): StsDisplay? {
        require(code in KEY_CODES) { "Key code not allowed: $code" }
        val r = command("KEY,$code,P")
        if (!Replies.isOk(r.raw, "KEY")) throw ScannerException("Key $code rejected: ${r.raw.trim()}")
        var last: StsDisplay? = null
        for (attempt in 0 until 4) {
            delay(90L + attempt * 110L)
            val d = runCatching { sts() }.getOrNull() ?: continue
            last = d
            if (!d.isBlank) break
        }
        return last
    }

    private suspend fun keyOnly(code: Char) {
        val r = command("KEY,$code,P")
        if (!Replies.isOk(r.raw, "KEY")) throw ScannerException("Key $code rejected: ${r.raw.trim()}")
    }

    suspend fun volume(): Int = Replies.intValue(command("VOL").raw, "VOL") ?: throw ScannerException("Unexpected VOL reply")
    suspend fun squelch(): Int = Replies.intValue(command("SQL").raw, "SQL") ?: throw ScannerException("Unexpected SQL reply")

    suspend fun setVolume(level: Int) {
        require(level in 0..29)
        val r = command("VOL,$level")
        if (!Replies.isOk(r.raw, "VOL")) throw ScannerException("Volume not accepted: ${r.raw.trim()}")
    }

    suspend fun setSquelch(level: Int) {
        require(level in 0..19)
        val r = command("SQL,$level")
        if (!Replies.isOk(r.raw, "SQL")) throw ScannerException("Squelch not accepted: ${r.raw.trim()}")
    }

    /** Toggles a hold via keys and confirms it from a fresh GSI. Returns the confirmed state. */
    suspend fun setHold(scope: HoldScope, enabled: Boolean): ScannerInfo {
        val before = gsi()
        if (scope.current(before) == enabled) return before
        for (k in scope.keys) {
            keyOnly(k)
            delay(100)
        }
        delay(120)
        var after = gsi()
        if (scope.current(after) != enabled) {
            delay(250)
            after = gsi()
        }
        if (scope.current(after) != enabled) throw ScannerException("Scanner did not confirm ${scope.label} hold")
        return after
    }

    /** Next/previous channel via NXT/PRV with the target keyword from the channel kind; rotary fallback. */
    suspend fun step(info: ScannerInfo, forward: Boolean) {
        val target = info.channelKind?.navTarget
        val idx = info.channelIndex
        if (target != null && idx != null) {
            val cmd = if (forward) "NXT" else "PRV"
            val r = command("$cmd,$target,$idx,,1")
            if (Replies.isOk(r.raw, cmd)) return
        }
        keyOnly(if (forward) '>' else '<')
    }

    suspend fun avoid() = keyOnly('L')

    suspend fun fqk(): List<Int> = Replies.fqk(command("FQK", 1500).raw) ?: throw ScannerException("Unexpected FQK reply")

    suspend fun setFqk(states: List<Int>) {
        val r = command(Replies.fqkWire(states), 2000)
        if (!Replies.isOk(r.raw, "FQK")) throw ScannerException("FQK not accepted: ${r.raw.trim()}")
    }

    suspend fun serviceTypes(): List<Boolean> = Replies.svc(command("SVC", 1500).raw) ?: throw ScannerException("Unexpected SVC reply")

    suspend fun setServiceTypes(states: List<Boolean>) {
        val r = command(Replies.svcWire(states), 2000)
        if (!Replies.isOk(r.raw, "SVC")) throw ScannerException("SVC not accepted: ${r.raw.trim()}")
    }

    suspend fun clock(): Replies.Clock = Replies.dtm(command("DTM").raw) ?: throw ScannerException("Unexpected DTM reply")

    suspend fun syncClock(now: LocalDateTime = LocalDateTime.now()) {
        val r = command(Replies.dtmWire(now), 1500)
        if (!Replies.isOk(r.raw, "DTM")) throw ScannerException("Clock not accepted: ${r.raw.trim()}")
    }

    /** GLT,FL / GLT,SYS,<fl> / GLT,DEPT,<sys> / GLT,SITE,<sys> / GLT,CFREQ,<dept> / GLT,TGID,<dept> */
    suspend fun list(kind: String, parent: String? = null): List<GltRecord> {
        val wire = if (parent == null) "GLT,$kind" else "GLT,$kind,$parent"
        val r = command(wire, 2500)
        if (r.raw.trim().uppercase().let { it == "ERR" || it.endsWith(",NG") }) throw ScannerException("$wire rejected")
        return GltParser.parse(r.raw, parserFactory).first
    }

    // ------------------------------------------------------------ waterfall

    suspend fun enterWaterfall() {
        command("JPM,WF_MODE", 2000)
        runCatching { command("PWF,1,ON", 1500) }
    }

    suspend fun waterfallFrame(): IntArray? = Waterfall.parseGwf(command("GWF,1,ON", 1100).raw)

    suspend fun gst(): GstStatus? = GstParser.parse(command("GST", 1100).raw)

    suspend fun leaveWaterfall(restoreScan: Boolean) {
        runCatching { command("GWF,1,OFF", 900) }
        runCatching { command("PWF,1,OFF", 900) }
        if (restoreScan) runCatching { command("JPM,SCN_MODE", 2000) }
    }

    companion object {
        const val UDP_PORT = 50536
        const val DEFAULT_TIMEOUT_MS = 1200
        val KEY_CODES = setOf(
            'M', 'F', 'L', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9', '.', 'E',
            '>', '<', '^', 'V', 'Q', 'Y', 'A', 'B', 'C', 'Z', 'T', 'R',
        )

        fun isNavigable(kind: ChannelKind?) = kind?.navTarget != null
    }
}
