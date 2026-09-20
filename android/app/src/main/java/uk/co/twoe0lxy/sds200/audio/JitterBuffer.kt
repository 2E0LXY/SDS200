package uk.co.twoe0lxy.sds200.audio

/**
 * PCM16 ring buffer with a prebuffer threshold. Playback waits until
 * [prebuffer] samples are queued (~200 ms at 8 kHz), then drains in chunks;
 * on underrun it re-enters prebuffering. If more than [maxBuffered] samples
 * accumulate the oldest are dropped to bound latency.
 */
class JitterBuffer(
    capacity: Int = 8000,
    private val prebuffer: Int = 1600,
    private val maxBuffered: Int = 4000,
) {
    private val ring = ShortArray(capacity)
    private var head = 0
    private var count = 0
    private var buffering = true
    private val lock = Object()

    var dropped: Long = 0
        private set
    var underruns: Long = 0
        private set

    fun push(src: ShortArray, offset: Int = 0, length: Int = src.size) {
        synchronized(lock) {
            for (i in 0 until length) {
                if (count == ring.size) {
                    head = (head + 1) % ring.size
                    count--
                    dropped++
                }
                ring[(head + count) % ring.size] = src[offset + i]
                count++
            }
            while (count > maxBuffered) {
                head = (head + 1) % ring.size
                count--
                dropped++
            }
        }
    }

    /**
     * Fills [dst] with [length] samples. Returns the number of real samples
     * copied; the remainder is zero-filled (silence) while buffering.
     */
    fun pull(dst: ShortArray, length: Int = dst.size): Int {
        synchronized(lock) {
            if (buffering) {
                if (count < prebuffer) {
                    dst.fill(0, 0, length)
                    return 0
                }
                buffering = false
            }
            val n = minOf(length, count)
            for (i in 0 until n) {
                dst[i] = ring[head]
                head = (head + 1) % ring.size
            }
            count -= n
            if (n < length) {
                dst.fill(0, n, length)
                buffering = true
                underruns++
            }
            return n
        }
    }

    fun buffered(): Int = synchronized(lock) { count }

    fun clear() {
        synchronized(lock) {
            head = 0
            count = 0
            buffering = true
        }
    }
}
