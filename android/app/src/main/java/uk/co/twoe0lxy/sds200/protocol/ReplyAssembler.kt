package uk.co.twoe0lxy.sds200.protocol

/**
 * Reassembles one scanner reply from UDP datagrams (port of udpCommand in protocol.go).
 *
 * Handles: plain "CMD,..." replies, "CMD,<XML>," markers sent as a separate
 * datagram, XML documents spread over several datagrams, and numbered GLT
 * fragments carrying <Footer No="n" EOT="0|1"/>.
 *
 * Datagrams must be decoded as ISO-8859-1 so that every byte maps to one char.
 */
class ReplyAssembler(wire: String) {
    val expected: String = commandName(wire)

    private val ordinary = ArrayList<String>()
    private val fragments = HashMap<Int, String>()
    private var eotNo = -1
    private var firstNo = -1
    private var rootOpen = ""
    private var rootClose = ""

    /** Offers one datagram. Returns the complete reply, or null if more data is needed. */
    fun offer(datagram: String): String? {
        val text = datagram.trimEnd('\u0000')
        val footer = XmlText.FOOTER.find(text)
        if (footer != null) {
            val no = footer.groupValues[1].toIntOrNull() ?: return null
            if (firstNo < 0 || no < firstNo) firstNo = no
            if (footer.groupValues[2] == "1") eotNo = no
            val xml = XmlText.extractXml(text)
            val parts = XmlText.parts(xml)
            if (rootOpen.isEmpty() && parts != null && parts.open.isNotEmpty()) {
                rootOpen = parts.open
                rootClose = parts.close
            }
            fragments[no] = if (parts != null && parts.inner.isNotEmpty()) parts.inner else XmlText.FOOTER.replace(xml, "")
            return merge()
        }
        for (line in text.replace('\n', '\r').split('\r')) {
            if (line.isEmpty()) continue
            ordinary += line
            val u = line.trim().uppercase()
            if (u == "$expected,<XML>,") continue
            if (u == "ERR" || u == "$expected,NG" || u == "$expected,ERR") return u
            if (u.startsWith("$expected,") || u == expected) {
                val joined = ordinary.joinToString("\r")
                if (!isXmlReply(joined)) return joined
                if (XmlText.isComplete(joined)) return joined
            }
        }
        val joined = ordinary.joinToString("\r")
        if (ordinary.isNotEmpty() && isXmlReply(joined) && XmlText.isComplete(joined)) return joined
        return null
    }

    /**
     * XML replies always carry the "CMD,<XML>," marker or an XML declaration.
     * Plain replies (STS) may legitimately contain '<' in display text.
     */
    private fun isXmlReply(s: String): Boolean =
        s.contains(",<XML>,", ignoreCase = true) || s.contains("<?xml")

    /** Called when the socket times out: returns whatever usable reply has been gathered. */
    fun onTimeout(): String? {
        if (fragments.isNotEmpty()) {
            merge()?.let { return it }
            // Incomplete fragment set: return what arrived, in order, rather than nothing.
            if (rootOpen.isNotEmpty()) {
                val sb = StringBuilder(rootOpen)
                for (k in fragments.keys.sorted()) sb.append(fragments[k])
                return sb.append(rootClose).toString()
            }
        }
        if (ordinary.isNotEmpty()) {
            val joined = ordinary.joinToString("\r")
            // A lone "CMD,<XML>," marker is never a reply on its own.
            if (joined.trim().uppercase() == "$expected,<XML>,") return null
            return joined
        }
        return null
    }

    private fun merge(): String? {
        if (eotNo < 0 || firstNo < 0 || rootOpen.isEmpty() || rootClose.isEmpty()) return null
        // Fragments are numbered from 1 (0 tolerated); a later first fragment means one is missing.
        if (firstNo > 1) return null
        for (i in firstNo..eotNo) if (!fragments.containsKey(i)) return null
        val sb = StringBuilder(rootOpen)
        for (i in firstNo..eotNo) sb.append(fragments[i])
        sb.append(rootClose)
        return sb.toString()
    }

    companion object {
        fun commandName(wire: String): String = wire.substringBefore(',').trim().uppercase()
    }
}
