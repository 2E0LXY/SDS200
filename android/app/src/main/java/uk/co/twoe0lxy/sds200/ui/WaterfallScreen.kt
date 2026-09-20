package uk.co.twoe0lxy.sds200.ui

import android.graphics.Bitmap
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.FilterQuality
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.dp
import androidx.core.graphics.createBitmap
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.LocalLifecycleOwner
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import uk.co.twoe0lxy.sds200.AppGraph
import uk.co.twoe0lxy.sds200.protocol.Frequency
import uk.co.twoe0lxy.sds200.protocol.GstStatus
import uk.co.twoe0lxy.sds200.protocol.Waterfall
import uk.co.twoe0lxy.sds200.ui.theme.LcdBackground

/**
 * Waterfall polling. Runs in the application scope so the scanner is always
 * returned to scan mode, even if the screen is torn down mid-request.
 */
class WaterfallViewModel(private val graph: AppGraph) : ViewModel() {
    var running by mutableStateOf(false)
        private set
    var starting by mutableStateOf(false)
        private set
    var error by mutableStateOf<String?>(null)
        private set
    var status by mutableStateOf<GstStatus?>(null)
        private set
    var latest by mutableStateOf<IntArray?>(null)
        private set
    var frames by mutableIntStateOf(0)
        private set

    val rows = 160
    private val pixels = IntArray(Waterfall.BINS * rows)
    val bitmap: Bitmap = createBitmap(Waterfall.BINS, rows)
    private var job: Job? = null
    private var cleanup: Job? = null
    private var startedWaterfallMode = false
    private var lo = 0f
    private var hi = 255f

    fun start() {
        if (job != null) return
        starting = true
        error = null
        val pendingCleanup = cleanup
        job = graph.scope.launch(Dispatchers.Main) {
            var misses = 0
            try {
                pendingCleanup?.join()
                val before = runCatching { graph.scanner.gst() }.getOrNull()
                if (before?.waterfallMode?.trim() != "1") {
                    startedWaterfallMode = true
                    graph.scanner.enterWaterfall()
                } else {
                    runCatching { graph.scanner.command("PWF,1,ON", 1500) }
                }
                status = runCatching { graph.scanner.gst() }.getOrNull() ?: before
                running = true
                starting = false
                var lastGst = System.currentTimeMillis()
                while (isActive) {
                    val t0 = System.currentTimeMillis()
                    val frame = runCatching { graph.scanner.waterfallFrame() }.getOrNull()
                    if (frame == null) {
                        if (++misses >= 3) {
                            error = "Waterfall stopped after repeated GWF failures"
                            break
                        }
                    } else {
                        misses = 0
                        push(frame)
                    }
                    if (t0 - lastGst >= 1000) {
                        lastGst = t0
                        runCatching { graph.scanner.gst() }.getOrNull()?.let { status = it }
                    }
                    delay((250 - (System.currentTimeMillis() - t0)).coerceAtLeast(20))
                }
            } catch (e: Exception) {
                if (e !is kotlinx.coroutines.CancellationException) error = e.userMessage()
            } finally {
                starting = false
                running = false
            }
        }
    }

    private fun push(frame: IntArray) {
        var mn = Int.MAX_VALUE
        var mx = Int.MIN_VALUE
        for (v in frame) {
            if (v < mn) mn = v
            if (v > mx) mx = v
        }
        // Smoothed auto-range: values are uncalibrated scanner FFT units.
        lo = if (frames == 0) mn.toFloat() else lo * 0.9f + mn * 0.1f
        hi = if (frames == 0) mx.toFloat() else hi * 0.9f + mx * 0.1f
        if (hi - lo < 8f) hi = lo + 8f
        System.arraycopy(pixels, 0, pixels, Waterfall.BINS, Waterfall.BINS * (rows - 1))
        for (i in 0 until Waterfall.BINS) pixels[i] = colour(((frame[i] - lo) / (hi - lo)).coerceIn(0f, 1f))
        bitmap.setPixels(pixels, 0, Waterfall.BINS, 0, 0, Waterfall.BINS, rows)
        latest = frame
        frames++
    }

    fun normalised(v: Int): Float = ((v - lo) / (hi - lo)).coerceIn(0f, 1f)

    fun stop() {
        val j = job ?: return
        job = null
        cleanup = graph.scope.launch(Dispatchers.Main) {
            j.cancel()
            j.join()
            val restore = startedWaterfallMode
            startedWaterfallMode = false
            graph.scanner.leaveWaterfall(restoreScan = restore)
        }
    }

    override fun onCleared() {
        stop()
    }

    companion object {
        /** Dark blue → cyan → yellow → red. */
        fun colour(t: Float): Int {
            val stops = floatArrayOf(0f, 0.35f, 0.65f, 0.85f, 1f)
            val cols = intArrayOf(0xFF000820.toInt(), 0xFF0050A0.toInt(), 0xFF00C8C8.toInt(), 0xFFFFE000.toInt(), 0xFFFF2000.toInt())
            var i = 0
            while (i < stops.size - 2 && t > stops[i + 1]) i++
            val f = ((t - stops[i]) / (stops[i + 1] - stops[i])).coerceIn(0f, 1f)
            val a = cols[i]
            val b = cols[i + 1]
            fun ch(shift: Int) = (((a shr shift) and 0xff) * (1 - f) + ((b shr shift) and 0xff) * f).toInt()
            return (0xFF shl 24) or (ch(16) shl 16) or (ch(8) shl 8) or ch(0)
        }
    }
}

@Composable
fun WaterfallScreen() {
    val vm = graphViewModel { WaterfallViewModel(it) }
    val owner = LocalLifecycleOwner.current
    // Always stop when leaving the screen or when the app goes to the background.
    DisposableEffect(owner) {
        val obs = LifecycleEventObserver { _, e -> if (e == Lifecycle.Event.ON_STOP) vm.stop() }
        owner.lifecycle.addObserver(obs)
        onDispose {
            owner.lifecycle.removeObserver(obs)
            vm.stop()
        }
    }
    Column(Modifier.fillMaxSize().padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Row {
            val st = vm.status
            Column(Modifier.weight(1f)) {
                Text("Centre: ${Frequency.format(st?.centreFrequency).ifBlank { "—" }}", style = MaterialTheme.typography.bodyMedium)
                val lo = Frequency.toMhz(st?.lowerFrequency)
                val hi = Frequency.toMhz(st?.upperFrequency)
                Text(
                    if (lo != null && hi != null) "Span: %.3f MHz (%.4f – %.4f)".format(java.util.Locale.UK, hi - lo, lo, hi) else "Span: —",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Text("Frames: ${vm.frames}", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            if (vm.running || vm.starting) {
                Button(onClick = { vm.stop() }, colors = ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.error)) {
                    Text(if (vm.starting) "Starting…" else "Stop")
                }
            } else {
                Button(onClick = { vm.start() }) { Text("Start") }
            }
        }
        ErrorText(vm.error)
        val latest = vm.latest
        Canvas(Modifier.fillMaxWidth().height(140.dp).background(LcdBackground)) {
            if (latest != null) {
                val path = Path()
                val dx = size.width / (latest.size - 1)
                latest.forEachIndexed { i, v ->
                    val y = size.height * (1f - vm.normalised(v))
                    if (i == 0) path.moveTo(0f, y) else path.lineTo(i * dx, y)
                }
                drawPath(path, Color(0xFFFFB300), style = Stroke(width = 2f))
            }
            drawLine(Color.White.copy(alpha = 0.25f), Offset(size.width / 2, 0f), Offset(size.width / 2, size.height))
        }
        val frameTick = vm.frames
        Canvas(Modifier.fillMaxWidth().weight(1f).background(LcdBackground)) {
            // Reading frameTick makes this redraw on every new frame.
            if (frameTick > 0) {
                drawImage(
                    vm.bitmap.asImageBitmap(),
                    srcOffset = IntOffset.Zero,
                    srcSize = IntSize(vm.bitmap.width, vm.bitmap.height),
                    dstSize = IntSize(size.width.toInt(), size.height.toInt()),
                    filterQuality = FilterQuality.None,
                )
            }
        }
        Text(
            "Uncalibrated scanner FFT levels (240 bins). Stopping returns the scanner to scan mode if this app switched it to waterfall.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}
