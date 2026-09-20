package uk.co.twoe0lxy.sds200.ui

import android.Manifest
import android.content.pm.PackageManager
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.launch
import uk.co.twoe0lxy.sds200.AppGraph
import uk.co.twoe0lxy.sds200.audio.AudioPhase
import uk.co.twoe0lxy.sds200.net.HoldScope
import uk.co.twoe0lxy.sds200.protocol.Frequency
import uk.co.twoe0lxy.sds200.protocol.ScannerInfo

class LiveViewModel(private val graph: AppGraph) : ViewModel() {
    var info by mutableStateOf<ScannerInfo?>(null)
        private set
    var error by mutableStateOf<String?>(null)
        private set
    var pendingHold by mutableStateOf<HoldScope?>(null)
        private set
    var busy by mutableStateOf(false)
        private set

    suspend fun poll() {
        if (graph.scanner.host.isEmpty()) return
        try {
            info = graph.scanner.gsi()
            if (!busy && pendingHold == null) error = null
        } catch (e: Exception) {
            error = e.userMessage()
        }
    }

    fun toggleHold(scope: HoldScope) {
        val cur = info?.let { scope.current(it) } ?: false
        pendingHold = scope
        viewModelScope.launch {
            try {
                info = graph.scanner.setHold(scope, !cur)
                error = null
            } catch (e: Exception) {
                error = e.userMessage()
            } finally {
                pendingHold = null
            }
        }
    }

    private fun act(block: suspend () -> Unit) {
        if (busy) return
        busy = true
        viewModelScope.launch {
            try {
                block()
                error = null
                poll()
            } catch (e: Exception) {
                error = e.userMessage()
            } finally {
                busy = false
            }
        }
    }

    fun avoid() = act { graph.scanner.avoid() }
    fun step(forward: Boolean) = act {
        val i = info ?: graph.scanner.gsi()
        graph.scanner.step(i, forward)
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
fun LiveScreen(onOpenSettings: () -> Unit) {
    val vm = graphViewModel { LiveViewModel(it) }
    val graph = appGraph()
    val settings by graph.settings.collectAsStateWithLifecycle()
    val audio by graph.audio.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
    PollWhileVisible(500) { vm.poll() }

    val notifLauncher = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) {
        graph.audio.start(context, settings.scannerIp)
    }
    fun listen() {
        if (Build.VERSION.SDK_INT >= 33 &&
            ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            notifLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
        } else {
            graph.audio.start(context, settings.scannerIp)
        }
    }

    Column(
        Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        if (settings.scannerIp.isBlank()) {
            SectionCard(title = "No scanner set") {
                Text("Enter the scanner's IP address or use Discover on the Settings screen.")
                Button(onClick = onOpenSettings) { Text("Open Settings") }
            }
            return@Column
        }
        val i = vm.info
        SectionCard {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    i?.mode?.ifBlank { null } ?: "—",
                    style = MaterialTheme.typography.labelLarge,
                    color = MaterialTheme.colorScheme.secondary,
                    modifier = Modifier.weight(1f),
                )
                SignalBars(i?.signal)
            }
            Text(
                listOfNotNull(i?.system, i?.department, i?.site).filter { it.isNotBlank() }.joinToString(" › ").ifBlank { "—" },
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Text(
                i?.channelOrFrequency?.ifBlank { null } ?: "—",
                style = MaterialTheme.typography.headlineSmall,
                fontWeight = FontWeight.SemiBold,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            Text(Frequency.format(i?.frequency).ifBlank { "—" }, style = MaterialTheme.typography.titleLarge, color = MaterialTheme.colorScheme.primary)
            LabelValue("TGID", i?.tgid.orEmpty())
            LabelValue("UID", i?.unitId.orEmpty())
            LabelValue("Modulation", listOfNotNull(i?.modulation, i?.p25Status?.takeIf { it.isNotBlank() && it != i?.modulation && it != "None" }).joinToString(" • "))
            LabelValue("Service type", i?.serviceType.orEmpty())
            LabelValue("RSSI", i?.rssi.orEmpty())
            LabelValue("Volume / Squelch", if (i?.volume != null) "${i.volume} / ${i.squelch ?: "—"}" else "")
            ErrorText(vm.error)
        }

        SectionCard(title = "Hold") {
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                for (scope in HoldScope.entries) {
                    val held = i?.let { scope.current(it) } == true
                    FilterChip(
                        selected = held,
                        onClick = { vm.toggleHold(scope) },
                        enabled = vm.pendingHold == null && i != null,
                        label = { Text(if (vm.pendingHold == scope) "${scope.label}…" else scope.label) },
                    )
                }
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedButton(onClick = { vm.step(false) }, enabled = !vm.busy, modifier = Modifier.weight(1f)) { Text("Previous") }
                OutlinedButton(onClick = { vm.avoid() }, enabled = !vm.busy, modifier = Modifier.weight(1f)) { Text("Avoid") }
                OutlinedButton(onClick = { vm.step(true) }, enabled = !vm.busy, modifier = Modifier.weight(1f)) { Text("Next") }
            }
        }

        SectionCard(title = "Live audio") {
            Row(verticalAlignment = Alignment.CenterVertically) {
                if (audio.active) {
                    Button(
                        onClick = { graph.audio.stop(context) },
                        colors = ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.error),
                    ) { Text("Stop") }
                } else {
                    FilledTonalButton(onClick = { listen() }) { Text("Listen") }
                }
                Spacer(Modifier.width(12.dp))
                Column {
                    Text(
                        when (audio.phase) {
                            AudioPhase.IDLE -> "Not listening"
                            AudioPhase.STARTING -> audio.stage.ifBlank { "Starting…" }
                            AudioPhase.WAITING_RTP -> "Waiting for audio…"
                            AudioPhase.STREAMING -> "Streaming"
                            AudioPhase.ERROR -> "Error"
                        },
                        style = MaterialTheme.typography.bodyMedium,
                    )
                    Text("${audio.packets} packets • buffer ${audio.bufferedMs} ms", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
            if (audio.phase == AudioPhase.ERROR) ErrorText(audio.error)
            Text(
                "The scanner allows one audio session at a time. Stop listening here before using another client.",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}
