package uk.co.twoe0lxy.sds200.net

import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import org.kxml2.io.KXmlParser
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress
import java.net.InetSocketAddress
import kotlin.concurrent.thread

/** End-to-end UDP tests against a loopback fake scanner. */
class ScannerClientTest {
    private lateinit var server: DatagramSocket
    private val received = mutableListOf<String>()

    @Before
    fun setUp() {
        server = DatagramSocket(InetSocketAddress(InetAddress.getLoopbackAddress(), 0))
    }

    @After
    fun tearDown() {
        server.close()
    }

    private fun serve(vararg replies: (String) -> List<String>) {
        thread(isDaemon = true) {
            val buf = ByteArray(2048)
            for (reply in replies) {
                val p = DatagramPacket(buf, buf.size)
                try {
                    server.receive(p)
                } catch (_: Exception) {
                    return@thread
                }
                val req = String(p.data, 0, p.length, Charsets.ISO_8859_1)
                synchronized(received) { received += req }
                for (r in reply(req)) {
                    val b = r.toByteArray(Charsets.ISO_8859_1)
                    server.send(DatagramPacket(b, b.size, p.socketAddress))
                    Thread.sleep(15)
                }
            }
        }
    }

    private fun client() = ScannerClient({ KXmlParser() }, server.localPort).also { it.host = "127.0.0.1" }

    @Test
    fun gsiWithSeparateMarkerDatagram() = runBlocking {
        serve({ listOf("GSI,<XML>,\r", "<ScannerInfo Mode=\"Scan\" V_Screen=\"Main\"><System Name=\"LEEDS\"/></ScannerInfo>") })
        val c = client()
        val info = c.gsi()
        assertEquals("LEEDS", info.system)
        assertEquals("GSI\r", received[0])
        assertTrue(c.status.value.online)
    }

    @Test
    fun keyPressRetriesBlankDisplay() = runBlocking {
        val blank = "STS,00000,,,,,,,,,,,,,,,,,,,"
        val full = "STS,00000,MENU,,,,,,,,,,,,,,,,,,"
        serve({ listOf("KEY,OK\r") }, { listOf("$blank\r") }, { listOf("$full\r") })
        val d = client().key('M')
        assertEquals("MENU", d?.lines?.get(0)?.text)
        assertEquals(listOf("KEY,M,P\r", "STS\r", "STS\r"), received)
    }

    @Test
    fun timeoutReportsError() = runBlocking {
        val c = client()
        try {
            c.command("MDL", 300)
            fail("expected timeout")
        } catch (e: ScannerException) {
            assertTrue(e.message!!.contains("No response"))
        }
    }

    @Test
    fun rejectsMultiLineCommands() = runBlocking {
        try {
            client().command("VOL,1\rVOL,2")
            fail()
        } catch (_: ScannerException) {
        }
    }
}
