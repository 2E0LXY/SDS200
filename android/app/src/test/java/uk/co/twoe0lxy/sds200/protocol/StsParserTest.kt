package uk.co.twoe0lxy.sds200.protocol

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class StsParserTest {
    private fun makeSts(form: String, texts: List<String>, modes: List<String> = emptyList()): String {
        val parts = mutableListOf("STS", form)
        for (i in form.indices) {
            parts += texts.getOrElse(i) { "" }
            parts += modes.getOrElse(i) { "" }
        }
        return parts.joinToString(",")
    }

    // Real scanning-mode sample from main_test.go (SDS200E fw 1.23.15).
    @Test
    fun parsesRealScanningSample() {
        val form = "00001010100000000"
        val texts = listOf(
            "              Sep19 17:40",
            "F0:- -------- XPT",
            "S0:----------   VOL: 0 SQL: 2",
            "D1:012 456789   Tag:01.--.---",
            "LEEDS",
            "Local",
            "Huddersfield",
            "",
            "Cummins Turbo Technologies L\u0006\u0007",
            " 453.112500MHz",
            "Custom 1",
            "Sys ID: ---     TGID: ---",
            "RFSS ID: ---    Site ID: ---",
            "WACN: ---       Batt:-.--V",
            "UID: ---        RSSI:-101dBm",
            "N\u0014\u0015",
            " SYSTEM      DEPT     CHANNEL",
        )
        val d = StsParser.parse(makeSts(form, texts))
        assertNull(d.error)
        assertEquals(17, d.lines.size)
        for (idx in listOf(4, 6, 8)) assertTrue("line $idx should be large", d.lines[idx].large)
        assertFalse(d.lines[5].large)
        assertEquals("Cummins Turbo Technologies L", d.lines[8].text)
        assertEquals("N", d.lines[15].text)
        assertEquals(" SYSTEM      DEPT     CHANNEL", d.lines[16].text)
        assertFalse(d.isBlank)
    }

    @Test
    fun parsesRealWaterfallSample() {
        val form = "00000000000000000000"
        val texts = MutableList(form.length) { "" }
        texts[0] = "              Sep19 17:29"
        texts[2] = "                     8.33k"
        texts[3] = "                 MHz AM"
        texts[17] = "SPAN:2.88MHz    GAIN:Auto"
        texts[18] = "CF:453.7250MHz  VOL: 0 SQL: 0"
        texts[19] = " to Scan     SPAN      HOLD"
        val d = StsParser.parse(makeSts(form, texts))
        assertNull(d.error)
        assertEquals("SPAN:2.88MHz    GAIN:Auto", d.lines[17].text)
        assertEquals("CF:453.7250MHz  VOL: 0 SQL: 0", d.lines[18].text)
        assertEquals(" to Scan     SPAN      HOLD", d.lines[19].text)
    }

    @Test
    fun keepsModesAndDecodesEscapedComma() {
        val raw = "STS,01,Hello\tWorld,*****      ,BIG,___" + ",,,,,,,,,"
        val d = StsParser.parse(raw)
        assertNull(d.error)
        assertEquals("Hello,World", d.lines[0].text)
        assertEquals("*****", d.lines[0].mode)
        assertTrue(d.lines[1].large)
        assertEquals("___", d.lines[1].mode)
    }

    @Test
    fun glyphBytesBecomeSpacesPreservingColumns() {
        // Latin-1 decoded bytes above 0x7E are icon glyphs.
        assertEquals("A  B", StsParser.decodeDisplayText("A\u0090ÿB"))
    }

    @Test
    fun blankDisplayDetected() {
        val d = StsParser.parse(makeSts("00000", List(5) { "" }))
        assertNull(d.error)
        assertTrue(d.isBlank)
    }

    @Test
    fun malformedRepliesDoNotThrow() {
        assertNotNull(StsParser.parse("").error)
        assertNotNull(StsParser.parse("STS").error)
        assertNotNull(StsParser.parse("STS,0011,a,b").error)
        assertNotNull(StsParser.parse("STS,xyz,a,b,c,d,e,f").error)
        assertNotNull(StsParser.parse("GSI,<XML>,").error)
    }

    @Test
    fun gstTailParsed() {
        val form = "00000"
        val lines = List(form.length) { listOf("L$it", "") }.flatten()
        val tail = listOf("0", "0", "0", "1", "4537250", "AM", "120", "4537250", "4522850", "4551650", "0", "3")
        val raw = (listOf("GST", form) + lines + tail).joinToString(",")
        val g = GstParser.parse(raw)!!
        assertEquals("1", g.waterfallMode)
        assertEquals("4537250", g.centreFrequency)
        assertEquals("453.7250 MHz", Frequency.format(g.centreFrequency))
        assertEquals("L4", g.display.lines[4].text)
        assertNull(GstParser.parse("GST,000"))
    }
}
