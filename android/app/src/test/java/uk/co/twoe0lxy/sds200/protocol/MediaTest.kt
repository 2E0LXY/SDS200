package uk.co.twoe0lxy.sds200.protocol

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

class MediaTest {
    @Test
    fun muLawKnownValues() {
        assertEquals(0, MuLaw.decodeSample(0xFF.toByte()))
        assertEquals(0, MuLaw.decodeSample(0x7F.toByte()))
        assertEquals(-32124, MuLaw.decodeSample(0x00.toByte()))
        assertEquals(32124, MuLaw.decodeSample(0x80.toByte()))
        assertEquals(-8, MuLaw.decodeSample(0x7E.toByte()))
        assertEquals(8, MuLaw.decodeSample(0xFE.toByte()))
        // Monotonic across each half.
        for (b in 0x80 until 0xFF) assertTrue(MuLaw.decodeSample(b.toByte()) >= MuLaw.decodeSample((b + 1).toByte()))
        val out = ShortArray(3)
        MuLaw.decode(byteArrayOf(0xFF.toByte(), 0x00, 0x80.toByte()), 0, 3, out)
        assertEquals(listOf<Short>(0, -32124, 32124), out.toList())
    }

    @Test
    fun rtpHeaderParsed() {
        val pkt = ByteArray(12 + 320)
        pkt[0] = 0x80.toByte(); pkt[1] = 0x00
        pkt[2] = 0x12; pkt[3] = 0x34
        pkt[4] = 0; pkt[5] = 0; pkt[6] = 0x01; pkt[7] = 0x40
        pkt[8] = 0xDE.toByte(); pkt[9] = 0xAD.toByte(); pkt[10] = 0xBE.toByte(); pkt[11] = 0xEF.toByte()
        val r = Rtp.parse(pkt)!!
        assertEquals(0, r.payloadType)
        assertEquals(0x1234, r.sequence)
        assertEquals(320L, r.timestamp)
        assertEquals(0xDEADBEEFL, r.ssrc)
        assertEquals(12, r.payloadOffset)
        assertEquals(320, r.payloadLength)
    }

    @Test
    fun rtpRejectsMalformed() {
        assertNull(Rtp.parse(ByteArray(5)))
        assertNull(Rtp.parse(ByteArray(12).also { it[0] = 0x40 })) // version 1
        assertNull(Rtp.parse(ByteArray(12).also { it[0] = 0xA0.toByte(); it[11] = 0 })) // bad padding
        assertNull(Rtp.parse(Rtp.PUNCH))
    }

    private val sampleSdp = "v=0\r\no=- 0 0 IN IP4 192.168.1.50\r\ns=scanner\r\nt=0 0\r\n" +
        "m=audio 0 RTP/AVP 0\r\na=rtpmap:0 PCMU/8000\r\na=control:trackID=1\r\n"

    @Test
    fun sdpControlFound() {
        assertEquals("trackID=1", Rtsp.parseSdpControl(sampleSdp))
    }

    @Test
    fun sdpWithoutPcmuRejected() {
        try {
            Rtsp.parseSdpControl("m=audio 0 RTP/AVP 8\r\na=control:track1\r\n")
            fail("expected RtspException")
        } catch (_: RtspException) {
        }
        try {
            Rtsp.parseSdpControl("m=audio 0 RTP/AVP 0\r\n")
            fail("expected RtspException")
        } catch (_: RtspException) {
        }
    }

    @Test
    fun trackUrlResolution() {
        val agg = Rtsp.aggregate("192.168.1.50")
        assertEquals("rtsp://192.168.1.50/au:scanner.au", agg)
        assertEquals("rtsp://192.168.1.50/au:scanner.au/trackID=1", Rtsp.trackUrl("trackID=1", null, agg))
        assertEquals("rtsp://192.168.1.50/au:scanner.au/trackID=1", Rtsp.trackUrl("trackID=1", "rtsp://192.168.1.50/au:scanner.au/", agg))
        assertEquals("rtsp://x/y", Rtsp.trackUrl("rtsp://x/y", "rtsp://ignored/", agg))
        assertEquals("rtsp://10.0.0.2:8554/au:scanner.au", Rtsp.aggregate("10.0.0.2", 8554))
    }

    @Test
    fun transportHeaderParsed() {
        val t = Rtsp.parseTransport("RTP/AVP;unicast;destination=192.168.1.20;source=192.168.1.50;client_port=40000-40001;server_port=6970-6971;ssrc=1A2B3C4D", 40000)
        assertEquals("192.168.1.50", t.source)
        assertEquals(6970, t.serverPort)
        assertEquals(40000, t.clientPort)
        assertEquals(0x1A2B3C4DL, t.ssrc)
        // Missing source falls back to the scanner host.
        assertEquals("10.0.0.9", Rtsp.parseTransport("RTP/AVP;unicast;server_port=5000", 1, "10.0.0.9").source)
        try {
            Rtsp.parseTransport("RTP/AVP;unicast;source=1.2.3.4;client_port=1234;server_port=5000", 40000)
            fail("client_port mismatch must be rejected")
        } catch (_: RtspException) {
        }
        try {
            Rtsp.parseTransport("RTP/AVP;unicast;source=1.2.3.4", 40000)
            fail("missing server_port must be rejected")
        } catch (_: RtspException) {
        }
    }

    @Test
    fun rtspHeadAndRequest() {
        val (code, rest) = Rtsp.parseHead("RTSP/1.0 200 OK", listOf("CSeq: 3", "Session: 12345678;timeout=60", "Content-Base: rtsp://h/au:scanner.au/"))
        assertEquals(200, code)
        assertEquals("OK", rest.first)
        assertEquals("12345678", Rtsp.sessionId(rest.second["session"]))
        assertEquals("rtsp://h/au:scanner.au/", rest.second["content-base"])
        val req = Rtsp.buildRequest("PLAY", "rtsp://h/au:scanner.au/", 4, listOf("Session" to "abc", "Range" to "npt=0.000-"), "UA/1")
        assertEquals("PLAY rtsp://h/au:scanner.au/ RTSP/1.0\r\nCSeq: 4\r\nUser-Agent: UA/1\r\nSession: abc\r\nRange: npt=0.000-\r\n\r\n", req)
        try {
            Rtsp.parseHead("HTTP/1.1 200 OK", emptyList())
            fail()
        } catch (_: RtspException) {
        }
    }
}
