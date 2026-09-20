package uk.co.twoe0lxy.sds200.audio

import android.media.AudioAttributes
import android.media.AudioFormat
import android.media.AudioTrack
import android.os.Process
import android.util.Log
import uk.co.twoe0lxy.sds200.protocol.MuLaw
import uk.co.twoe0lxy.sds200.protocol.Rtp
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.SocketException
import java.net.SocketTimeoutException
import java.util.concurrent.atomic.AtomicBoolean

enum class AudioPhase { IDLE, STARTING, WAITING_RTP, STREAMING, ERROR }

data class AudioState(
    val phase: AudioPhase = AudioPhase.IDLE,
    val packets: Long = 0,
    val stage: String = "",
    val error: String? = null,
    val bufferedMs: Int = 0,
) {
    val active: Boolean get() = phase == AudioPhase.STARTING || phase == AudioPhase.WAITING_RTP || phase == AudioPhase.STREAMING
}

/**
 * One RTSP + RTP + AudioTrack session. [start] spawns the worker thread; [stop]
 * is safe from any thread and always results in TEARDOWN being sent if a
 * session was established.
 */
class AudioSession(
    private val host: String,
    private val onState: (AudioState) -> Unit,
) {
    private val running = AtomicBoolean(false)
    private val stopped = AtomicBoolean(false)
    @Volatile private var rtpSocket: DatagramSocket? = null
    @Volatile private var rtsp: RtspClient? = null
    private var worker: Thread? = null
    private val jitter = JitterBuffer()

    fun start() {
        if (!running.compareAndSet(false, true)) return
        worker = Thread({ runWorker() }, "sds200-rtp").also { it.start() }
    }

    fun stop() {
        if (!stopped.compareAndSet(false, true)) return
        // Closing the RTP socket unblocks receive(); the worker then sends TEARDOWN.
        runCatching { rtpSocket?.close() }
        val w = worker
        if (w == null || !w.isAlive) {
            // Worker never ran or already finished: make sure the RTSP session is gone.
            runCatching { rtsp?.close() }
        }
    }

    private fun runWorker() {
        Process.setThreadPriority(Process.THREAD_PRIORITY_URGENT_AUDIO)
        var track: AudioTrack? = null
        var player: Thread? = null
        var packets = 0L
        var lastReport = 0L
        var error: String? = null
        val client = RtspClient(host)
        rtsp = client
        try {
            onState(AudioState(AudioPhase.STARTING, stage = "Binding RTP socket"))
            val sock = DatagramSocket(null).apply {
                reuseAddress = true
                receiveBufferSize = 256 * 1024
                bind(InetSocketAddress(0))
            }
            rtpSocket = sock
            if (stopped.get()) return
            onState(AudioState(AudioPhase.STARTING, stage = "RTSP OPTIONS/DESCRIBE/SETUP/PLAY"))
            val tr = client.start(sock.localPort)
            if (stopped.get()) return
            val source = InetSocketAddress(InetAddress.getByName(tr.source), tr.serverPort)
            punch(sock, source)
            onState(AudioState(AudioPhase.WAITING_RTP, stage = "PLAY OK, waiting for RTP"))

            val t = buildTrack()
            track = t
            t.play()
            player = Thread({ playLoop(t) }, "sds200-audio-out").also { it.start() }

            val buf = ByteArray(2048)
            val pcm = ShortArray(2048)
            var lastKeepalive = System.nanoTime()
            var lastPacketAt = System.nanoTime()
            sock.soTimeout = 500
            while (!stopped.get()) {
                val p = DatagramPacket(buf, buf.size)
                try {
                    sock.receive(p)
                    val from = p.socketAddress as? InetSocketAddress
                    if (from != null && (from.address != source.address || from.port != source.port)) continue
                    val rtp = Rtp.parse(p.data, p.length) ?: continue
                    if (rtp.payloadType != 0) continue
                    val n = minOf(rtp.payloadLength, pcm.size)
                    MuLaw.decode(p.data, rtp.payloadOffset, n, pcm)
                    jitter.push(pcm, 0, n)
                    packets++
                    lastPacketAt = System.nanoTime()
                    if (packets == 1L || System.nanoTime() - lastReport > 250_000_000L) {
                        lastReport = System.nanoTime()
                        onState(
                            AudioState(
                                AudioPhase.STREAMING, packets, "Streaming",
                                bufferedMs = jitter.buffered() / 8,
                            ),
                        )
                    }
                } catch (_: SocketTimeoutException) {
                    // fall through to housekeeping
                }
                val now = System.nanoTime()
                if (now - lastKeepalive >= KEEPALIVE_NS) {
                    lastKeepalive = now
                    punch(sock, source)
                    client.getParameter()
                }
                if (now - lastPacketAt >= SILENCE_NS) {
                    throw IllegalStateException(
                        if (packets == 0L) "PLAY succeeded but no RTP arrived (firewall or Wi-Fi isolation?)"
                        else "No RTP packets for 10 s",
                    )
                }
            }
        } catch (e: SocketException) {
            if (!stopped.get()) error = e.message ?: "Socket error"
        } catch (e: Exception) {
            if (!stopped.get()) error = e.message ?: e.javaClass.simpleName
            Log.w(TAG, "audio session failed", e)
        } finally {
            stopped.set(true)
            runCatching { client.close() } // TEARDOWN on every exit path
            runCatching { rtpSocket?.close() }
            player?.let { runCatching { it.join(500) } }
            track?.let { runCatching { it.pause(); it.flush(); it.stop() }; runCatching { it.release() } }
            jitter.clear()
            onState(
                if (error != null) AudioState(AudioPhase.ERROR, packets, "Stopped", error)
                else AudioState(AudioPhase.IDLE, packets, "Stopped"),
            )
        }
    }

    private fun punch(sock: DatagramSocket, to: InetSocketAddress) {
        runCatching { sock.send(DatagramPacket(Rtp.PUNCH, Rtp.PUNCH.size, to)) }
    }

    private fun buildTrack(): AudioTrack {
        val min = AudioTrack.getMinBufferSize(SAMPLE_RATE, AudioFormat.CHANNEL_OUT_MONO, AudioFormat.ENCODING_PCM_16BIT)
        return AudioTrack.Builder()
            .setAudioAttributes(
                AudioAttributes.Builder()
                    .setUsage(AudioAttributes.USAGE_MEDIA)
                    .setContentType(AudioAttributes.CONTENT_TYPE_SPEECH)
                    .build(),
            )
            .setAudioFormat(
                AudioFormat.Builder()
                    .setSampleRate(SAMPLE_RATE)
                    .setEncoding(AudioFormat.ENCODING_PCM_16BIT)
                    .setChannelMask(AudioFormat.CHANNEL_OUT_MONO)
                    .build(),
            )
            .setBufferSizeInBytes(maxOf(min, 3200))
            .setTransferMode(AudioTrack.MODE_STREAM)
            .build()
    }

    private fun playLoop(track: AudioTrack) {
        Process.setThreadPriority(Process.THREAD_PRIORITY_URGENT_AUDIO)
        val chunk = ShortArray(CHUNK)
        while (!stopped.get()) {
            jitter.pull(chunk, CHUNK)
            val w = track.write(chunk, 0, CHUNK)
            if (w < 0) break
        }
    }

    companion object {
        private const val TAG = "AudioSession"
        const val SAMPLE_RATE = 8000
        private const val CHUNK = 160 // 20 ms
        private const val KEEPALIVE_NS = 15_000_000_000L
        private const val SILENCE_NS = 10_000_000_000L
    }
}
