package uk.co.twoe0lxy.sds200.audio

import org.junit.Assert.assertEquals
import org.junit.Test

class JitterBufferTest {
    @Test
    fun prebuffersThenDrainsThenRebuffers() {
        val jb = JitterBuffer(capacity = 100, prebuffer = 20, maxBuffered = 50)
        val out = ShortArray(10)
        jb.push(ShortArray(10) { 1 })
        assertEquals(0, jb.pull(out)) // still prebuffering
        jb.push(ShortArray(10) { 2 })
        assertEquals(10, jb.pull(out))
        assertEquals(1, out[0].toInt())
        assertEquals(10, jb.pull(out))
        assertEquals(2, out[9].toInt())
        assertEquals(0, jb.pull(out)) // underrun -> silence, back to buffering
        assertEquals(1L, jb.underruns)
    }

    @Test
    fun capsLatency() {
        val jb = JitterBuffer(capacity = 100, prebuffer = 10, maxBuffered = 30)
        jb.push(ShortArray(80) { it.toShort() })
        assertEquals(30, jb.buffered())
        val out = ShortArray(1)
        jb.pull(out)
        assertEquals(50, out[0].toInt()) // oldest samples dropped
    }
}
