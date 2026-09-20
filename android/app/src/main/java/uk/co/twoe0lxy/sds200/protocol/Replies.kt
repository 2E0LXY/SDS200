package uk.co.twoe0lxy.sds200.protocol

import java.time.LocalDateTime

/** Helpers for simple comma-separated replies. */
object Replies {
    /** Fields after "CMD," in the first line of [raw] that belongs to [command]; null if absent. */
    fun fields(raw: String, command: String): List<String>? {
        for (l in raw.split('\r', '\n')) {
            val line = l.trim()
            if (line.equals(command, ignoreCase = true)) return emptyList()
            val p = "$command,"
            if (line.length >= p.length && line.substring(0, p.length).equals(p, ignoreCase = true)) {
                return line.substring(p.length).split(',')
            }
        }
        return null
    }

    fun isOk(raw: String, command: String): Boolean =
        fields(raw, command)?.firstOrNull()?.trim()?.equals("OK", ignoreCase = true) == true

    /** "VOL,12" -> 12 */
    fun intValue(raw: String, command: String): Int? = fields(raw, command)?.firstOrNull()?.trim()?.toIntOrNull()

    fun text(raw: String, command: String): String? = fields(raw, command)?.joinToString(",")?.trim()

    /** FQK: 100 states 0 (off) / 1 (on) / 2 (not assigned). */
    fun fqk(raw: String): List<Int>? {
        val f = fields(raw, "FQK") ?: return null
        val states = f.mapNotNull { v -> v.trim().takeIf { it.isNotEmpty() }?.let { it.toIntOrNull()?.coerceIn(0, 2) ?: 0 } }
        if (states.size == 1 && f.size == 1) {
            // Tolerate an unseparated 100-digit form.
            val digits = f[0].trim()
            if (digits.length == 100 && digits.all { it in '0'..'2' }) return digits.map { it - '0' }
        }
        if (states.size < 100) return null
        return states.take(100)
    }

    fun fqkWire(states: List<Int>): String {
        require(states.size == 100 && states.all { it in 0..2 }) { "FQK requires 100 states of 0/1/2" }
        return "FQK," + states.joinToString(",")
    }

    const val SVC_COUNT = 47

    /** SVC: 37 preset + 10 custom flags. */
    fun svc(raw: String): List<Boolean>? {
        val f = fields(raw, "SVC") ?: return null
        if (f.size < SVC_COUNT) return null
        return f.take(SVC_COUNT).map { it.trim() == "1" }
    }

    fun svcWire(states: List<Boolean>): String {
        require(states.size == SVC_COUNT)
        return "SVC," + states.joinToString(",") { if (it) "1" else "0" }
    }

    /** Preset service type names in SVC slot order (handlers.go presetServiceTypes; blanks are reserved). */
    val presetServiceTypes = listOf(
        "Multi-Dispatch", "Law Dispatch", "Fire Dispatch", "EMS Dispatch", "", "Multi-Tac", "Law Tac", "Fire-Tac",
        "EMS-Tac", "", "Interop", "Hospital", "Ham", "Public Works", "Aircraft", "Federal", "Business", "", "",
        "Railroad", "Other", "Multi-Talk", "Law Talk", "Fire-Talk", "EMS-Talk", "Transportation", "", "",
        "Emergency Ops", "Military", "Media", "Schools", "Security", "Utilities", "", "", "Corrections",
    )

    fun serviceTypeName(slot: Int): String =
        if (slot < presetServiceTypes.size) presetServiceTypes[slot] else "Custom ${slot - 36}"

    data class Clock(val time: LocalDateTime, val dst: Boolean, val rtcOk: Boolean?)

    /** DTM,dst,YYYY,MM,DD,hh,mm,ss,rtc */
    fun dtm(raw: String): Clock? {
        val f = fields(raw, "DTM") ?: return null
        if (f.size < 7) return null
        val n = f.take(7).map { it.trim().toIntOrNull() ?: return null }
        return try {
            Clock(LocalDateTime.of(n[1], n[2], n[3], n[4], n[5], n[6]), n[0] == 1, f.getOrNull(7)?.trim()?.let { it == "1" })
        } catch (_: Exception) {
            null
        }
    }

    /** The scanner shows DTM time as-is: write local wall-clock time with DST cleared. */
    fun dtmWire(t: LocalDateTime): String =
        "DTM,0,${t.year},${t.monthValue},${t.dayOfMonth},${t.hour},${t.minute},${t.second}"
}

object Waterfall {
    const val BINS = 240

    /**
     * GWF,<240 values>, : values are hex bytes. An unseparated 480-digit hex
     * string is also accepted (see the C port). Returns null on anything else.
     */
    fun parseGwf(raw: String): IntArray? {
        val line = raw.split('\r', '\n').firstOrNull { it.startsWith("GWF,", true) || it.startsWith("PWF,", true) }
            ?: return null
        var vals = line.substring(4).split(',').map { it.trim() }
        if (vals.isNotEmpty() && vals.last().isEmpty()) vals = vals.dropLast(1)
        if (vals.size == 1 && vals[0].length == BINS * 2) vals = vals[0].chunked(2)
        if (vals.size != BINS) return null
        val out = IntArray(BINS)
        for (i in 0 until BINS) {
            val v = vals[i]
            if (v.isEmpty() || v.length > 4) return null
            out[i] = v.toIntOrNull(16) ?: return null
        }
        return out
    }
}
