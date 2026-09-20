package uk.co.twoe0lxy.sds200.protocol

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.kxml2.io.KXmlParser

class ReplyAssemblerTest {
    @Test
    fun plainReplyCompletesImmediately() {
        val a = ReplyAssembler("VOL,12")
        assertEquals("VOL,OK", a.offer("VOL,OK\r"))
    }

    @Test
    fun stsWithAngleBracketsIsNotMistakenForXml() {
        val a = ReplyAssembler("STS")
        val sts = "STS,00000,<HOLD>,,  <Scan>,,,,,,,,,,,,,,,,,"
        assertEquals(sts, a.offer("$sts\r"))
    }

    // main_test.go TestUDPCommandWaitsForSeparatedGSIXML
    @Test
    fun waitsForXmlAfterSeparateMarker() {
        val a = ReplyAssembler("GSI")
        assertNull(a.offer("GSI,<XML>,\r"))
        val r = a.offer("<ScannerInfo Mode=\"Scan\" V_Screen=\"Main\"><System Name=\"LEEDS\"/></ScannerInfo>")
        assertNotNull(r)
        assertTrue(r!!.contains("<ScannerInfo"))
        assertEquals("LEEDS", GsiParser.parse(r) { KXmlParser() }.system)
    }

    @Test
    fun loneMarkerIsNeverAReply() {
        val a = ReplyAssembler("GSI")
        assertNull(a.offer("GSI,<XML>,\r"))
        assertNull(a.onTimeout())
    }

    @Test
    fun xmlSplitAcrossDatagrams() {
        val a = ReplyAssembler("GSI")
        assertNull(a.offer("GSI,<XML>,\r<?xml version=\"1.0\" encoding=\"utf-8\"?>\r<ScannerInfo Mode=\"Scan\">\r <System Name=\"A\"/>\r"))
        assertNull(a.offer(" <Department Name=\"B\"/>\r"))
        val r = a.offer("</ScannerInfo>\r")
        assertNotNull(r)
        assertTrue(r!!.endsWith("</ScannerInfo>"))
    }

    @Test
    fun numberedGltFragmentsMerged() {
        val a = ReplyAssembler("GLT,FL")
        val f1 = "GLT,<XML>,\r<?xml version=\"1.0\" encoding=\"utf-8\"?>\r<GLT>\r<FL Index=\"0\" Name=\"One\"/>\r<Footer No=\"1\" EOT=\"0\"/>\r</GLT>\r"
        val f2 = "GLT,<XML>,\r<?xml version=\"1.0\" encoding=\"utf-8\"?>\r<GLT>\r<FL Index=\"1\" Name=\"Two\"/>\r<Footer No=\"2\" EOT=\"1\"/>\r</GLT>\r"
        // Out-of-order arrival.
        assertNull(a.offer(f2))
        val r = a.offer(f1)
        assertNotNull(r)
        val (recs, err) = GltParser.parse(r!!) { KXmlParser() }
        assertNull(err)
        assertEquals(listOf("One", "Two"), recs.map { it.attrs["Name"] })
    }

    @Test
    fun errorRepliesTerminate() {
        assertEquals("ERR", ReplyAssembler("FOO").offer("ERR\r"))
        assertEquals("KEY,NG", ReplyAssembler("KEY,Z,P").offer("KEY,NG\r"))
    }

    @Test
    fun unrelatedLinesFallBackOnTimeout() {
        val a = ReplyAssembler("MDL")
        assertNull(a.offer("XYZ,1\r"))
        assertEquals("XYZ,1", a.onTimeout())
    }

    @Test
    fun commandNameParsing() {
        assertEquals("KEY", ReplyAssembler.commandName("key,M,P"))
        assertEquals("GLT", ReplyAssembler.commandName("GLT,SYS,3"))
    }
}
