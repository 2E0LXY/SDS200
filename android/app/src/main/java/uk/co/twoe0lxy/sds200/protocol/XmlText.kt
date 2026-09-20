package uk.co.twoe0lxy.sds200.protocol

import org.xmlpull.v1.XmlPullParser
import java.io.StringReader

/** Supplies an XmlPullParser. On Android this is [android.util.Xml.newPullParser]; tests inject kxml2. */
typealias PullParserFactory = () -> XmlPullParser

/** Text helpers for the SDS "CMD,<XML>," reply framing (ported from protocol.go). */
object XmlText {
    val FOOTER = Regex(
        """<(?:Foot|Footer)\b[^>]*\bNo="([0-9]+)"[^>]*\bEOT="([01])"[^>]*/?>""",
        setOf(RegexOption.IGNORE_CASE, RegexOption.DOT_MATCHES_ALL),
    )
    private val ROOT = Regex("""<([A-Za-z_:][A-Za-z0-9_.:-]*)(?:\s[^>]*)?>""", RegexOption.DOT_MATCHES_ALL)

    /** Returns the XML part of a reply, skipping any "CMD,<XML>," prefix. */
    fun extractXml(s: String): String {
        val decl = s.indexOf("<?xml")
        if (decl >= 0) return s.substring(decl)
        var t = s
        val marker = t.uppercase().indexOf(",<XML>,")
        if (marker >= 0) t = t.substring(marker + 7)
        val m = ROOT.find(t) ?: return t
        return t.substring(m.range.first)
    }

    fun looksLikeXml(s: String): Boolean = extractXml(s).trim().startsWith("<")

    /** Name of the root element, skipping the declaration, comments and processing instructions. */
    fun rootName(x: String): String? {
        var i = 0
        while (true) {
            val lt = x.indexOf('<', i)
            if (lt < 0 || lt + 1 >= x.length) return null
            val c = x[lt + 1]
            if (c == '?' || c == '!') {
                i = lt + 1
                continue
            }
            val sb = StringBuilder()
            var j = lt + 1
            while (j < x.length && !x[j].isWhitespace() && x[j] != '>' && x[j] != '/') sb.append(x[j++])
            return sb.toString().ifEmpty { null }
        }
    }

    /**
     * Structural completeness check: the document's root element has been closed
     * (or is self-closing). This is what the transport needs to know; full
     * well-formedness is left to the parser, which tolerates damage.
     */
    fun isComplete(s: String): Boolean {
        val x = FOOTER.replace(extractXml(s), "")
        val root = rootName(x) ?: return false
        if (x.contains("</$root>")) return true
        val open = Regex("<" + Regex.escape(root) + """\b[^>]*/>""")
        val selfClosed = open.find(x) ?: return false
        return x.substring(selfClosed.range.last + 1).isBlank()
    }

    data class Parts(val open: String, val close: String, val inner: String)

    fun parts(xml: String): Parts? {
        var x = FOOTER.replace(extractXml(xml), "").trim()
        if (x.startsWith("<?xml")) {
            val e = x.indexOf("?>")
            if (e >= 0) x = x.substring(e + 2).trim()
        }
        val m = ROOT.find(x) ?: return null
        val tag = m.groupValues[1]
        val close = "</$tag>"
        val end = x.lastIndexOf(close)
        val inner = if (end < 0 || end < m.range.last + 1) "" else x.substring(m.range.last + 1, end)
        return Parts(m.value, close, inner)
    }

    /** Scanner replies are carried as ISO-8859-1 text (one char per byte); XML payloads are UTF-8. */
    fun latin1ToUtf8(s: String): String =
        String(s.toByteArray(Charsets.ISO_8859_1), Charsets.UTF_8)

    /** Characters XML 1.0 forbids would abort the parser; replace them with spaces. */
    fun sanitise(s: String): String {
        val sb = StringBuilder(s.length)
        for (ch in s) {
            val ok = ch == '\t' || ch == '\n' || ch == '\r' || (ch >= ' ' && ch != '￾' && ch != '￿')
            sb.append(if (ok) ch else ' ')
        }
        return sb.toString()
    }

    /** A flat element: tag name, attributes and its depth (1 = root). */
    data class Element(val tag: String, val attrs: Map<String, String>, val depth: Int)

    data class Parsed(val elements: List<Element>, val error: String?)

    /**
     * Parses [raw] leniently: every start tag seen before any error is returned,
     * so a truncated or slightly malformed document still yields what it can.
     */
    fun parseElements(raw: String, factory: PullParserFactory): Parsed {
        var x = FOOTER.replace(extractXml(raw), "")
        x = sanitise(latin1ToUtf8(x))
        // Drop the declaration: the text is already decoded and some parsers reject a
        // declaration that is not at offset zero after trimming.
        if (x.startsWith("<?xml")) {
            val e = x.indexOf("?>")
            if (e >= 0) x = x.substring(e + 2)
        }
        val out = ArrayList<Element>()
        var error: String? = null
        try {
            val p = factory()
            p.setFeature(XmlPullParser.FEATURE_PROCESS_NAMESPACES, false)
            p.setInput(StringReader(x.trim()))
            var ev = p.eventType
            while (ev != XmlPullParser.END_DOCUMENT) {
                if (ev == XmlPullParser.START_TAG) {
                    val attrs = LinkedHashMap<String, String>()
                    for (i in 0 until p.attributeCount) attrs[p.getAttributeName(i)] = p.getAttributeValue(i) ?: ""
                    out += Element(p.name ?: "", attrs, p.depth)
                }
                ev = p.next()
            }
        } catch (e: Exception) {
            error = e.message ?: e.javaClass.simpleName
        }
        return Parsed(out, error)
    }
}
