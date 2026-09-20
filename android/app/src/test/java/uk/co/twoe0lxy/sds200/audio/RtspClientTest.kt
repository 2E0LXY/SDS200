package uk.co.twoe0lxy.sds200.audio

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.BufferedReader
import java.io.InputStreamReader
import java.net.InetAddress
import java.net.ServerSocket
import kotlin.concurrent.thread

/** Drives RtspClient against a loopback fake RTSP server and checks the exact method sequence. */
class RtspClientTest {
    @Test
    fun fullHandshakeAndTeardown() {
        val server = ServerSocket(0, 1, InetAddress.getLoopbackAddress())
        val methods = mutableListOf<String>()
        val uris = mutableListOf<String>()
        val sdp = "v=0\r\ns=scanner\r\nm=audio 0 RTP/AVP 0\r\na=rtpmap:0 PCMU/8000\r\na=control:trackID=1\r\n"
        val t = thread {
            server.accept().use { s ->
                val r = BufferedReader(InputStreamReader(s.getInputStream(), Charsets.ISO_8859_1))
                val out = s.getOutputStream()
                while (true) {
                    val line = r.readLine() ?: break
                    if (line.isEmpty()) continue
                    val (method, uri) = line.split(' ')
                    var cseq = ""
                    while (true) {
                        val h = r.readLine() ?: break
                        if (h.isEmpty()) break
                        if (h.startsWith("CSeq:")) cseq = h.substringAfter(':').trim()
                    }
                    synchronized(methods) { methods += method; uris += uri }
                    val extra = when (method) {
                        "DESCRIBE" -> "Content-Type: application/sdp\r\nContent-Base: rtsp://127.0.0.1/au:scanner.au/\r\nContent-Length: ${sdp.length}\r\n"
                        "SETUP" -> "Session: ABCD1234;timeout=60\r\nTransport: RTP/AVP;unicast;source=127.0.0.1;client_port=40000;server_port=6970-6971\r\n"
                        else -> ""
                    }
                    val body = if (method == "DESCRIBE") sdp else ""
                    out.write("RTSP/1.0 200 OK\r\nCSeq: $cseq\r\n$extra\r\n$body".toByteArray(Charsets.ISO_8859_1))
                    out.flush()
                    if (method == "TEARDOWN") break
                }
            }
        }
        val c = RtspClient("127.0.0.1", server.localPort)
        val tr = c.start(40000)
        assertEquals("127.0.0.1", tr.source)
        assertEquals(6970, tr.serverPort)
        assertEquals("ABCD1234", c.session)
        c.getParameter()
        c.close()
        c.close() // idempotent: no second TEARDOWN
        t.join(3000)
        server.close()
        assertEquals(listOf("OPTIONS", "DESCRIBE", "SETUP", "PLAY", "GET_PARAMETER", "TEARDOWN"), methods)
        val agg = "rtsp://127.0.0.1:${server.localPort}/au:scanner.au"
        assertEquals("rtsp://127.0.0.1/au:scanner.au/trackID=1", uris[2])
        assertEquals("$agg/", uris[3])
        assertTrue(uris[5].endsWith("/au:scanner.au/"))
    }
}
