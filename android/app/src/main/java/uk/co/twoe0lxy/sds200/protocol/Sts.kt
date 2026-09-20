package uk.co.twoe0lxy.sds200.protocol

/** One display line from STS/GST. [mode] is per column: ' ' normal, '*' reverse, '_' underline. */
data class StsLine(val large: Boolean, val text: String, val mode: String)

data class StsDisplay(
    val form: String,
    val lines: List<StsLine>,
    val error: String? = null,
) {
    val isBlank: Boolean get() = lines.all { it.text.isBlank() }

    /** Soft-key labels are shown on the last line in most screens. */
    val summary: String
        get() = lines.map { it.text.trim() }.filter { it.isNotEmpty() }.takeLast(4).joinToString(" | ")
}

object StsParser {
    /**
     * SDS display text uses a single-byte scanner font: 0x20..0x7E are ASCII and
     * everything else is an icon glyph. Keep printable ASCII, turn the protocol's
     * tab (escaped comma) back into a comma and replace glyphs with spaces so
     * columns stay aligned. The input must be ISO-8859-1 decoded (one char per byte).
     */
    fun decodeDisplayText(s: String): String {
        val sb = StringBuilder(s.length)
        for (ch in s) {
            val c = ch.code
            when {
                ch == '\t' -> sb.append(',')
                c in 0x20..0x7e -> sb.append(ch)
                else -> sb.append(' ')
            }
        }
        return sb.toString().trimEnd(' ')
    }

    fun parse(raw: String): StsDisplay = parseLines(raw, "STS")

    internal fun parseLines(raw: String, command: String): StsDisplay {
        val line = raw.split('\r', '\n').firstOrNull { it.startsWith("$command,", ignoreCase = true) }
            ?: return StsDisplay("", emptyList(), "not an $command response")
        val f = line.split(',')
        if (f.size < 2) return StsDisplay("", emptyList(), "not an $command response")
        val form = f[1]
        val n = form.length
        if (n == 0 || n > 40 || form.any { it != '0' && it != '1' }) {
            return StsDisplay(form, emptyList(), "invalid display form \"$form\"")
        }
        if (f.size < 2 + 2 * n) {
            return StsDisplay(form, emptyList(), "unexpected field count ${f.size} for display form \"$form\"")
        }
        val lines = (0 until n).map { i ->
            StsLine(
                large = form[i] == '1',
                text = decodeDisplayText(f[2 + i * 2]),
                mode = decodeDisplayText(f[3 + i * 2]),
            )
        }
        return StsDisplay(form, lines)
    }
}

/** Parsed GST reply: display plus waterfall status tail (spec v2.00 GST). */
data class GstStatus(
    val display: StsDisplay,
    val waterfallMode: String = "",
    val markerFrequency: String = "",
    val modulation: String = "",
    val centreFrequency: String = "",
    val lowerFrequency: String = "",
    val upperFrequency: String = "",
)

object GstParser {
    private const val TAIL = 12

    fun parse(raw: String): GstStatus? {
        val line = raw.split('\r', '\n').firstOrNull { it.startsWith("GST,", ignoreCase = true) } ?: return null
        val f = line.split(',').drop(1)
        if (f.isEmpty()) return null
        val form = f[0]
        val n = form.length
        if (n < 5 || n > 40) return null
        if (f.size < 1 + 2 * n + TAIL) return null
        val display = StsParser.parseLines(line, "GST")
        val tail = f.takeLast(TAIL)
        return GstStatus(
            display = display,
            waterfallMode = tail[3],
            markerFrequency = tail[4],
            modulation = tail[5],
            centreFrequency = tail[7],
            lowerFrequency = tail[8],
            upperFrequency = tail[9],
        )
    }
}
