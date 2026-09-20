package uk.co.twoe0lxy.sds200.protocol

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.LocalDateTime

class RepliesTest {
    @Test
    fun simpleFields() {
        assertEquals("SDS200E", Replies.text("MDL,SDS200E", "MDL"))
        assertEquals(12, Replies.intValue("VOL,12\r", "VOL"))
        assertTrue(Replies.isOk("KEY,OK", "KEY"))
        assertEquals(null, Replies.intValue("SQL,NG", "SQL"))
    }

    @Test
    fun fqkRoundTrip() {
        val states = List(100) { it % 3 }
        val wire = Replies.fqkWire(states)
        assertEquals(states, Replies.fqk(wire))
        assertEquals(states, Replies.fqk("FQK," + states.joinToString("")))
        assertNull(Replies.fqk("FQK,1,0"))
    }

    @Test
    fun svcParsedAndNamed() {
        val raw = "SVC," + List(47) { if (it == 1 || it == 40) "1" else "0" }.joinToString(",")
        val s = Replies.svc(raw)!!
        assertEquals(47, s.size)
        assertTrue(s[1] && s[40])
        assertEquals("Law Dispatch", Replies.serviceTypeName(1))
        assertEquals("Corrections", Replies.serviceTypeName(36))
        assertEquals("Custom 1", Replies.serviceTypeName(37))
        assertEquals("Custom 10", Replies.serviceTypeName(46))
        assertEquals(raw, Replies.svcWire(s))
    }

    @Test
    fun dtm() {
        val c = Replies.dtm("DTM,0,2025,09,19,17,40,05,1")!!
        assertEquals(LocalDateTime.of(2025, 9, 19, 17, 40, 5), c.time)
        assertEquals(true, c.rtcOk)
        assertEquals("DTM,0,2025,9,19,17,40,5", Replies.dtmWire(c.time))
        assertNull(Replies.dtm("DTM,OK"))
    }

    @Test
    fun gwf() {
        val hex = List(240) { (it % 256).toString(16).padStart(2, '0') }
        val v = Waterfall.parseGwf("GWF," + hex.joinToString(",") + ",")!!
        assertEquals(240, v.size)
        assertEquals(0x10, v[16])
        assertEquals(10, Waterfall.parseGwf("GWF," + List(240) { "0a" }.joinToString(",") + ",")!![0])
        assertEquals(240, Waterfall.parseGwf("GWF," + hex.joinToString(""))!!.size)
        assertNull(Waterfall.parseGwf("GWF,01,02,"))
        assertNull(Waterfall.parseGwf("GWF,OK"))
    }

    @Test
    fun frequencyFormatting() {
        assertEquals("456.7500 MHz", Frequency.format(" 456.750000MHz"))
        assertEquals("406.0000 MHz", Frequency.format("4060000"))
        assertEquals("", Frequency.format(null))
        assertEquals("abc", Frequency.format("abc"))
    }
}
