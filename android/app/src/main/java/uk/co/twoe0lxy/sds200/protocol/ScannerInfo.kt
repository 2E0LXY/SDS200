package uk.co.twoe0lxy.sds200.protocol

/** Channel element kinds reported by GSI, in lookup priority order. */
enum class ChannelKind(val tag: String, val navTarget: String?) {
    CONV("ConvFrequency", "CFREQ"),
    TGID("TGID", "TGID"),
    SEARCH("SrchFrequency", null),
    CC_HIT("CcHitsChannel", "CCHIT"),
    TONE_OUT("ToneOutChannel", "FTO"),
    WX("WxChannel", "WX");

    companion object {
        fun fromTag(tag: String): ChannelKind? = entries.firstOrNull { it.tag == tag }
    }
}

/** Snapshot of GSI (ScannerInfo) state, mirroring parseScannerInfo in protocol.go. */
data class ScannerInfo(
    val mode: String = "",
    val screen: String = "",
    val monitorList: String = "",
    val system: String = "",
    val department: String = "",
    val site: String = "",
    val channel: String = "",
    val channelKind: ChannelKind? = null,
    val frequency: String = "",
    val tgid: String = "",
    val unitId: String = "",
    val modulation: String = "",
    val serviceType: String = "",
    val systemHold: Boolean? = null,
    val departmentHold: Boolean? = null,
    val siteHold: Boolean? = null,
    val channelHold: Boolean? = null,
    val channelAvoid: String = "",
    val systemIndex: Int? = null,
    val departmentIndex: Int? = null,
    val siteIndex: Int? = null,
    val channelIndex: Int? = null,
    val volume: Int? = null,
    val squelch: Int? = null,
    val rssi: String = "",
    val signal: Int? = null,
    val recording: String = "",
    val mute: String = "",
    val attenuator: String = "",
    val p25Status: String = "",
    val parseError: String? = null,
) {
    /** Channel name, or the formatted frequency in search/Close Call modes without a name. */
    val channelOrFrequency: String
        get() = channel.ifBlank { Frequency.format(frequency) }
}

object GsiParser {
    fun parse(raw: String, factory: PullParserFactory): ScannerInfo {
        val start = raw.indexOf("<ScannerInfo")
        if (start < 0) return ScannerInfo(parseError = "ScannerInfo XML not found")
        val parsed = XmlText.parseElements(raw.substring(start), factory)
        val els = parsed.elements
        if (els.isEmpty()) return ScannerInfo(parseError = parsed.error ?: "empty ScannerInfo")
        val root = els.firstOrNull { it.tag == "ScannerInfo" }?.attrs ?: emptyMap()
        fun first(tag: String): Map<String, String> =
            els.firstOrNull { it.tag == tag && it.depth >= 2 }?.attrs ?: emptyMap()

        val monitor = first("MonitorList")
        val sys = first("System")
        val dept = first("Department")
        val site = first("Site")
        val prop = first("Property")
        val unit = first("UnitID")
        val kind = ChannelKind.entries.firstOrNull { k -> els.any { it.tag == k.tag && it.depth >= 2 } }
        val ch = kind?.let { first(it.tag) } ?: emptyMap()
        return ScannerInfo(
            mode = root["Mode"].orEmpty(),
            screen = root["V_Screen"].orEmpty(),
            monitorList = monitor["Name"].orEmpty(),
            system = sys["Name"].orEmpty(),
            department = dept["Name"].orEmpty(),
            site = site["Name"].orEmpty(),
            channel = ch["Name"].orEmpty(),
            channelKind = kind,
            frequency = firstNonBlank(ch["Freq"], ch["Frequency"], first("SiteFrequency")["Freq"], site["Freq"]),
            tgid = firstNonBlank(ch["TGID"], ch["Id"], ch["ID"]),
            unitId = firstNonBlank(ch["U_Id"], ch["UID"], unit["U_Id"], prop["U_Id"]),
            modulation = firstNonBlank(ch["Mod"], site["Mod"], prop["P25Status"]),
            serviceType = firstNonBlank(ch["SvcType"], ch["ServiceType"]),
            systemHold = holdValue(sys["Hold"]),
            departmentHold = holdValue(dept["Hold"]),
            siteHold = holdValue(site["Hold"]),
            channelHold = holdValue(ch["Hold"]),
            channelAvoid = ch["Avoid"].orEmpty(),
            systemIndex = sys["Index"]?.trim()?.toIntOrNull(),
            departmentIndex = dept["Index"]?.trim()?.toIntOrNull(),
            siteIndex = site["Index"]?.trim()?.toIntOrNull(),
            channelIndex = ch["Index"]?.trim()?.toIntOrNull(),
            volume = prop["VOL"]?.trim()?.toIntOrNull(),
            squelch = prop["SQL"]?.trim()?.toIntOrNull(),
            rssi = firstNonBlank(prop["Rssi"], prop["RSSI"]),
            signal = firstNonBlank(prop["Sig"], prop["Signal"], prop["S_Level"]).trim().toIntOrNull(),
            recording = firstNonBlank(prop["Rec"], prop["REC"], prop["Recording"]),
            mute = firstNonBlank(prop["Mute"], prop["MUTE"]),
            attenuator = firstNonBlank(prop["ATT"], prop["Att"]),
            p25Status = prop["P25Status"].orEmpty(),
            parseError = parsed.error,
        )
    }

    fun holdValue(v: String?): Boolean? = when (v?.trim()?.lowercase()) {
        "on", "1", "true", "held" -> true
        "off", "0", "false", "released" -> false
        else -> null
    }

    private fun firstNonBlank(vararg xs: String?): String = xs.firstOrNull { !it.isNullOrBlank() } ?: ""
}

/** GLT list parsing: every depth-2 element becomes a record (tag + attributes). */
data class GltRecord(val tag: String, val attrs: Map<String, String>) {
    val index: String get() = attrs["Index"].orEmpty()
    val label: String
        get() = listOf(attrs["Name"], attrs["Freq"]?.let { Frequency.format(it) }, attrs["TGID"], attrs["Index"], tag)
            .firstOrNull { !it.isNullOrBlank() }.orEmpty()
    val meta: String
        get() = listOfNotNull(
            attrs["Type"] ?: attrs["SystemType"],
            attrs["Freq"]?.let { Frequency.format(it) },
            attrs["Mod"],
            attrs["TGID"]?.let { "TGID $it" },
            attrs["SvcType"],
            attrs["Monitor"]?.let { "Monitor $it" },
            attrs["Avoid"]?.takeIf { it.isNotBlank() && !it.equals("Off", true) }?.let { "Avoid $it" },
            attrs["Q_Key"]?.takeIf { it.isNotBlank() && !it.equals("None", true) }?.let { "QK $it" },
        ).filter { it.isNotBlank() }.joinToString(" • ")
}

object GltParser {
    fun parse(raw: String, factory: PullParserFactory): Pair<List<GltRecord>, String?> {
        val parsed = XmlText.parseElements(raw, factory)
        val recs = parsed.elements.filter { it.depth == 2 && !it.tag.equals("Footer", true) }
            .map { GltRecord(it.tag, it.attrs) }
        return recs to parsed.error
    }
}

object Frequency {
    /** " 456.750000MHz" -> "456.7500 MHz"; unknown formats are returned trimmed. */
    fun format(raw: String?): String {
        val s = raw?.trim().orEmpty()
        if (s.isEmpty()) return ""
        val num = s.removeSuffix("MHz").removeSuffix("mhz").trim()
        val mhz = num.toDoubleOrNull()
        if (mhz != null && s.contains("MHz", ignoreCase = true)) return String.format(java.util.Locale.UK, "%.4f MHz", mhz)
        // Bare integers from GLT/GST are in units of 100 Hz (e.g. 4060000 = 406.0000 MHz).
        val n = s.toLongOrNull()
        if (n != null && n > 0) {
            val v = n / 10_000.0
            if (v in 25.0..1300.0) return String.format(java.util.Locale.UK, "%.4f MHz", v)
            val hz = n / 1_000_000.0
            if (hz in 25.0..1300.0) return String.format(java.util.Locale.UK, "%.4f MHz", hz)
        }
        if (mhz != null && mhz in 25.0..1300.0) return String.format(java.util.Locale.UK, "%.4f MHz", mhz)
        return s
    }

    fun toMhz(raw: String?): Double? {
        val f = format(raw)
        return if (f.endsWith(" MHz")) f.removeSuffix(" MHz").toDoubleOrNull() else null
    }
}
