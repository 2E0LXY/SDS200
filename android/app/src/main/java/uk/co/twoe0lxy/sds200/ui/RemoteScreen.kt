package uk.co.twoe0lxy.sds200.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Slider
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.FlowPreview
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.debounce
import kotlinx.coroutines.launch
import kotlin.math.roundToInt
import uk.co.twoe0lxy.sds200.AppGraph
import uk.co.twoe0lxy.sds200.protocol.StsDisplay
import uk.co.twoe0lxy.sds200.protocol.StsLine
import uk.co.twoe0lxy.sds200.ui.theme.LcdBackground
import uk.co.twoe0lxy.sds200.ui.theme.LcdText

@OptIn(FlowPreview::class)
class RemoteViewModel(private val graph: AppGraph) : ViewModel() {
    var display by mutableStateOf<StsDisplay?>(null)
        private set
    var error by mutableStateOf<String?>(null)
        private set
    var volume by mutableStateOf<Int?>(null)
        private set
    var squelch by mutableStateOf<Int?>(null)
        private set
    var keyBusy by mutableStateOf(false)
        private set

    private val volumeWrites = MutableSharedFlow<Int>(extraBufferCapacity = 8)
    private val squelchWrites = MutableSharedFlow<Int>(extraBufferCapacity = 8)
    private var levelsLoaded = false

    init {
        viewModelScope.launch {
            volumeWrites.debounce(300).collectLatest { v ->
                runCatching { graph.scanner.setVolume(v); volume = graph.scanner.volume() }
                    .onFailure { error = it.userMessage() }
            }
        }
        viewModelScope.launch {
            squelchWrites.debounce(300).collectLatest { v ->
                runCatching { graph.scanner.setSquelch(v); squelch = graph.scanner.squelch() }
                    .onFailure { error = it.userMessage() }
            }
        }
    }

    suspend fun poll() {
        if (graph.scanner.host.isEmpty() || keyBusy) return
        try {
            if (!levelsLoaded) {
                volume = graph.scanner.volume()
                squelch = graph.scanner.squelch()
                levelsLoaded = true
            }
            val d = graph.scanner.sts()
            if (!d.isBlank || display == null) display = d
            error = null
        } catch (e: Exception) {
            error = e.userMessage()
        }
    }

    fun key(code: Char) {
        keyBusy = true
        viewModelScope.launch {
            try {
                graph.scanner.key(code)?.let { display = it }
                error = null
            } catch (e: Exception) {
                error = e.userMessage()
            } finally {
                keyBusy = false
            }
        }
    }

    fun changeVolume(v: Int) {
        volume = v
        volumeWrites.tryEmit(v)
    }

    fun changeSquelch(v: Int) {
        squelch = v
        squelchWrites.tryEmit(v)
    }
}

@Composable
fun RemoteScreen() {
    val vm = graphViewModel { RemoteViewModel(it) }
    PollWhileVisible(400) { vm.poll() }
    Column(
        Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(12.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        ScannerDisplay(vm.display)
        ErrorText(vm.error)
        Keypad(vm)
        SectionCard {
            LevelSlider("Volume", vm.volume, 29) { vm.changeVolume(it) }
            LevelSlider("Squelch", vm.squelch, 19) { vm.changeSquelch(it) }
        }
    }
}

private const val COLUMNS = 30

@Composable
fun ScannerDisplay(display: StsDisplay?) {
    BoxWithConstraints(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(8.dp))
            .background(LcdBackground)
            .border(1.dp, MaterialTheme.colorScheme.surfaceVariant, RoundedCornerShape(8.dp))
            .padding(8.dp),
    ) {
        val density = LocalDensity.current
        // Monospace glyphs are ~0.6 em wide: size the small font so 30 columns fill the width.
        val widthPx = with(density) { maxWidth.toPx() }
        val smallPx = widthPx / (COLUMNS * 0.6f)
        val smallSp = with(density) { smallPx.toSp() }
        Column(Modifier.heightIn(min = 160.dp)) {
            val lines = display?.lines.orEmpty()
            if (lines.isEmpty()) {
                Text("No display data", color = LcdText.copy(alpha = 0.5f), fontFamily = FontFamily.Monospace, fontSize = smallSp)
            }
            for (line in lines) {
                val len = line.text.trimEnd().length.coerceAtLeast(1)
                val largePx = minOf(smallPx * 1.7f, widthPx / (len * 0.6f))
                val size = if (line.large) with(density) { largePx.toSp() } else smallSp
                Text(
                    styledLine(line),
                    style = TextStyle(
                        fontFamily = FontFamily.Monospace,
                        fontSize = size,
                        lineHeight = size * 1.15f,
                        fontWeight = if (line.large) FontWeight.Bold else FontWeight.Normal,
                        color = LcdText,
                    ),
                    maxLines = 1,
                    softWrap = false,
                    overflow = TextOverflow.Clip,
                )
            }
        }
    }
}

/** Applies per-column STS modes: '*' reverse video, '_' underline. */
fun styledLine(line: StsLine): AnnotatedString = buildAnnotatedString {
    val width = maxOf(line.text.length, line.mode.trimEnd().length)
    val text = line.text.padEnd(width)
    for (i in text.indices) {
        val m = line.mode.getOrNull(i) ?: ' '
        when (m) {
            '*' -> pushStyle(SpanStyle(color = LcdBackground, background = LcdText))
            '_' -> pushStyle(SpanStyle(textDecoration = TextDecoration.Underline))
            else -> pushStyle(SpanStyle())
        }
        append(text[i])
        pop()
    }
}

private data class K(val label: String, val code: Char)

@Composable
private fun Keypad(vm: RemoteViewModel) {
    val soft = softKeyLabels(vm.display)
    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
        KeyRow(vm, K(soft[0], 'A'), K(soft[1], 'B'), K(soft[2], 'C'))
        KeyRow(vm, K("MENU", 'M'), K("FUNC", 'F'), K("AVOID", 'L'), K("REPLAY", 'Y'))
        KeyRow(vm, K("◀", '<'), K("PUSH", '^'), K("▶", '>'))
        KeyRow(vm, K("1", '1'), K("2", '2'), K("3", '3'))
        KeyRow(vm, K("4", '4'), K("5", '5'), K("6", '6'))
        KeyRow(vm, K("7", '7'), K("8", '8'), K("9", '9'))
        KeyRow(vm, K(". / NO", '.'), K("0", '0'), K("E / YES", 'E'))
        KeyRow(vm, K("SERVICE", 'T'), K("RANGE", 'R'), K("ZIP", 'Z'))
        KeyRow(vm, K("VOL push", 'V'), K("SQL push", 'Q'))
    }
}

@Composable
private fun KeyRow(vm: RemoteViewModel, vararg keys: K) {
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
        for (k in keys) KeyButton(k, vm)
    }
}

@Composable
private fun RowScope.KeyButton(k: K, vm: RemoteViewModel) {
    val digit = k.code.isDigit()
    val mod = Modifier.weight(1f).height(46.dp)
    val pad = PaddingValues(horizontal = 4.dp)
    if (digit) {
        FilledTonalButton(onClick = { vm.key(k.code) }, modifier = mod, contentPadding = pad, shape = RoundedCornerShape(8.dp)) {
            Text(k.label, fontSize = 18.sp, maxLines = 1)
        }
    } else {
        OutlinedButton(onClick = { vm.key(k.code) }, modifier = mod, contentPadding = pad, shape = RoundedCornerShape(8.dp)) {
            Text(k.label, fontSize = 12.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
    }
}

/** Soft-key captions come from the bottom display line, split into three 10-column cells. */
fun softKeyLabels(d: StsDisplay?): List<String> {
    val last = d?.lines?.lastOrNull()?.text.orEmpty().padEnd(COLUMNS)
    val cells = listOf(last.substring(0, 10), last.substring(10, 20), last.substring(20, 30)).map { it.trim() }
    return cells.mapIndexed { i, s -> s.ifEmpty { "Soft ${i + 1}" } }
}

@Composable
private fun LevelSlider(label: String, value: Int?, max: Int, onChange: (Int) -> Unit) {
    Column {
        Row {
            Text(label, style = MaterialTheme.typography.bodyMedium, modifier = Modifier.width(80.dp))
            Text(value?.toString() ?: "—", style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.primary)
        }
        Slider(
            value = (value ?: 0).toFloat(),
            onValueChange = { onChange(it.roundToInt()) },
            valueRange = 0f..max.toFloat(),
            steps = max - 1,
            enabled = value != null,
        )
    }
}
