package uk.co.twoe0lxy.sds200.protocol

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.kxml2.io.KXmlParser

class GsiParserTest {
    private val factory: PullParserFactory = { KXmlParser() }

    private val trunkSample =
        "GSI,<XML>,\r<?xml version=\"1.0\" encoding=\"utf-8\"?>\r" +
            "<ScannerInfo Mode=\"Trunk Scan\" V_Screen=\"trunk_scan\">\r" +
            " <MonitorList Name=\"Full Database\" Index=\"4294967295\" ListType=\"FullDb\" Q_Key=\"None\" N_Tag=\"None\" DB_Counter=\"3\" />\r" +
            " <System Name=\"LEEDS\" Index=\"12\" Avoid=\"Off\" SystemType=\"P25 Trunk\" Q_Key=\"None\" N_Tag=\"None\" Hold=\"Off\" />\r" +
            " <Department Name=\"Police &amp; Fire\" Index=\"34\" Avoid=\"Off\" Q_Key=\"None\" Hold=\"On\" />\r" +
            " <TGID Name=\"Huddersfield\" Index=\"56\" Avoid=\"Off\" TGID=\"TGID:1234\" SetSlot=\"Any\" RecSlot=\"Any\" N_Tag=\"None\" Hold=\"Off\" SvcType=\"Law Dispatch\" P_Ch=\"Off\" LVL=\"0\" />\r" +
            " <Site Name=\"Local\" Index=\"78\" Avoid=\"Off\" Q_Key=\"None\" Hold=\"Off\" Mod=\"AUTO\" />\r" +
            " <SiteFrequency Freq=\" 453.112500MHz\" IFX=\"Off\" SAS=\"All\" SAD=\"None\" />\r" +
            " <Property VOL=\"12\" SQL=\"2\" Sig=\"4\" Att=\"Off\" Rec=\"Off\" KeyLock=\"Off\" P25Status=\"P25 Phase1\" Mute=\"Unmute\" Backlight=\"100\" Rssi=\"-101\" />\r" +
            "</ScannerInfo>\r"

    @Test
    fun parsesTrunkedSample() {
        val i = GsiParser.parse(trunkSample, factory)
        assertNull(i.parseError)
        assertEquals("Trunk Scan", i.mode)
        assertEquals("trunk_scan", i.screen)
        assertEquals("Full Database", i.monitorList)
        assertEquals("LEEDS", i.system)
        assertEquals("Police & Fire", i.department)
        assertEquals("Local", i.site)
        assertEquals("Huddersfield", i.channel)
        assertEquals(ChannelKind.TGID, i.channelKind)
        assertEquals("TGID:1234", i.tgid)
        assertEquals(" 453.112500MHz", i.frequency)
        assertEquals("453.1125 MHz", Frequency.format(i.frequency))
        assertEquals("AUTO", i.modulation)
        assertEquals("Law Dispatch", i.serviceType)
        assertEquals(false, i.systemHold)
        assertEquals(true, i.departmentHold)
        assertEquals(false, i.siteHold)
        assertEquals(false, i.channelHold)
        assertEquals(12, i.systemIndex)
        assertEquals(56, i.channelIndex)
        assertEquals(12, i.volume)
        assertEquals(2, i.squelch)
        assertEquals(4, i.signal)
        assertEquals("-101", i.rssi)
        assertEquals("P25 Phase1", i.p25Status)
    }

    // Spec v2.00 example (conventional) with ConvFrequency.
    @Test
    fun parsesSpecConventionalExample() {
        val raw = """
            GSI,<XML>,
            <?xml version="1.0" encoding="utf-8"?>
            <ScannerInfo Mode="Trunk Scan Hold" V_Screen="trunk_scan">
               <MonitorList Name="Full Database" Index="4294967295" ListType="FullDb" Q_Key="None" N_Tag="None" DB_Counter="3" />
               <System Name="Calcasieu" Index="283" Avoid="Off" SystemType="Conventional" Q_Key="None" N_Tag="None" Hold="On" />
               <Department Name="Calcasieu Parish - Parish Fire &amp; Medical" Index="286" Avoid="Off" Q_Key="None" Hold="Off" />
               <ConvFrequency Name="DeQuincy Fire Department" Index="290" Avoid="Off" Freq=" 154.4150MHz"
                    Mod="NFM" N_Tag="None" Hold="On" SvcType="Fire Dispatch" P_Ch="Off" SAS="All" SAD="None" LVL="0" IFX="Off" />
               <AGC A_AGC="Off" D_AGC="Off" />
               <DualWatch PRI="Off" CC="Off" WX="Off" />
               <Property VOL="0" SQL="9" Sig="0" WiFi="3" Att="Off" Rec="Off" KeyLock="Off" P25Status="None" Mute="Mute" Backlight="100" Rssi="0.377" />
               <ViewDescription>
                    <InfoArea1 Text="F0:01234-6*789" />
                    <PopupScreen Text="Quick Save?\n"/>
               </ViewDescription>
            </ScannerInfo>
        """.trimIndent().replace("\n", "\r")
        val i = GsiParser.parse(raw, factory)
        assertNull(i.parseError)
        assertEquals(ChannelKind.CONV, i.channelKind)
        assertEquals("DeQuincy Fire Department", i.channel)
        assertEquals("154.4150 MHz", Frequency.format(i.frequency))
        assertEquals("NFM", i.modulation)
        assertEquals(true, i.systemHold)
        assertEquals(true, i.channelHold)
        assertEquals(290, i.channelIndex)
        assertEquals("CFREQ", i.channelKind?.navTarget)
    }

    // main_test.go: marker datagram then bare XML.
    @Test
    fun parsesSeparatedMarkerReply() {
        val raw = "GSI,<XML>,\r<ScannerInfo Mode=\"Scan\" V_Screen=\"Main\"><System Name=\"LEEDS\"/></ScannerInfo>"
        val i = GsiParser.parse(raw, factory)
        assertNull(i.parseError)
        assertEquals("LEEDS", i.system)
        assertEquals("Scan", i.mode)
    }

    @Test
    fun searchModeFallsBackToFrequency() {
        val raw = "GSI,<XML>,\r<ScannerInfo Mode=\"Quick Search\" V_Screen=\"quick_search\">" +
            "<SrchFrequency Freq=\" 456.750000MHz\" Mod=\"NFM\" /></ScannerInfo>"
        val i = GsiParser.parse(raw, factory)
        assertEquals(ChannelKind.SEARCH, i.channelKind)
        assertEquals("456.7500 MHz", i.channelOrFrequency)
        assertNull(i.channelKind?.navTarget)
    }

    @Test
    fun utf8NamesDecodedFromLatin1Transport() {
        // Bytes on the wire are UTF-8; the transport decodes datagrams as ISO-8859-1.
        val utf8 = "GSI,<XML>,\r<ScannerInfo Mode=\"Scan\"><System Name=\"Café\"/></ScannerInfo>"
        val onWire = String(utf8.toByteArray(Charsets.UTF_8), Charsets.ISO_8859_1)
        assertEquals("Café", GsiParser.parse(onWire, factory).system)
    }

    @Test
    fun truncatedXmlYieldsPartialResultWithoutThrowing() {
        val raw = "GSI,<XML>,\r<ScannerInfo Mode=\"Scan\"><System Name=\"LEEDS\" Hold=\"On\"/><Department Name=\"Po"
        val i = GsiParser.parse(raw, factory)
        assertEquals("LEEDS", i.system)
        assertEquals(true, i.systemHold)
        assertNotNull(i.parseError)
    }

    @Test
    fun garbageDoesNotThrow() {
        assertNotNull(GsiParser.parse("", factory).parseError)
        assertNotNull(GsiParser.parse("GSI,NG", factory).parseError)
        assertNotNull(GsiParser.parse("<ScannerInfo <<<>>>", factory).parseError)
    }

    @Test
    fun gltListParsed() {
        val raw = "GLT,<XML>,\r<?xml version=\"1.0\" encoding=\"utf-8\"?>\r<GLT>\r" +
            "  <FL Index=\"0\" Name=\"Favorites List 1\" Monitor=\"On\" Q_Key=\"1\" N_Tag=\"None\" />\r" +
            "  <FL Index=\"1\" Name=\"Favorites List 2\" Monitor=\"On\" Q_Key=\"2\" N_Tag=\"2\" />\r" +
            "  <FL Index=\"2\" Name=\"Favorites List 3\" Monitor=\"Off\" Q_Key=\"3\" N_Tag=\"999\" />\r</GLT>\r"
        val (recs, err) = GltParser.parse(raw, factory)
        assertNull(err)
        assertEquals(3, recs.size)
        assertEquals("FL", recs[0].tag)
        assertEquals("Favorites List 3", recs[2].label)
        assertEquals("2", recs[2].index)
        assertTrue(recs[0].meta.contains("QK 1"))
    }
}
