package uk.co.twoe0lxy.sds200.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.launch
import uk.co.twoe0lxy.sds200.AppGraph
import uk.co.twoe0lxy.sds200.BuildConfig
import uk.co.twoe0lxy.sds200.data.SettingsRepository
import uk.co.twoe0lxy.sds200.net.DiscoveredScanner
import uk.co.twoe0lxy.sds200.net.Discovery
import uk.co.twoe0lxy.sds200.protocol.Replies
import java.time.format.DateTimeFormatter

class SettingsViewModel(private val graph: AppGraph) : ViewModel() {
    var discovering by mutableStateOf(false)
        private set
    var found by mutableStateOf<List<DiscoveredScanner>?>(null)
        private set
    var discoverError by mutableStateOf<String?>(null)
        private set
    var model by mutableStateOf<String?>(null)
        private set
    var firmware by mutableStateOf<String?>(null)
        private set
    var clock by mutableStateOf<Replies.Clock?>(null)
        private set
    var busy by mutableStateOf(false)
        private set
    var message by mutableStateOf<String?>(null)
        private set
    var error by mutableStateOf<String?>(null)
        private set

    fun save(ip: String) {
        viewModelScope.launch {
            graph.settingsRepo.setScannerIp(ip)
            graph.scanner.host = ip.trim()
            model = null
            firmware = null
            clock = null
            test()
        }
    }

    fun setKeepScreenOn(on: Boolean) {
        viewModelScope.launch { graph.settingsRepo.setKeepScreenOn(on) }
    }

    fun discover(context: android.content.Context) {
        if (discovering) return
        discovering = true
        discoverError = null
        found = null
        viewModelScope.launch {
            try {
                val local = Discovery.localWifiIpv4(context)
                if (local == null) {
                    discoverError = "Not connected to Wi-Fi (no IPv4 address found)"
                } else {
                    found = Discovery.scan(local, 900)
                    if (found.isNullOrEmpty()) discoverError = "No SDS200 answered on ${local.hostAddress?.substringBeforeLast('.')}.0/24"
                }
            } catch (e: Exception) {
                discoverError = e.userMessage()
            } finally {
                discovering = false
            }
        }
    }

    fun test() = run {
        model = graph.scanner.model()
        firmware = graph.scanner.firmware()
        clock = graph.scanner.clock()
    }

    fun readClock() = run { clock = graph.scanner.clock() }

    fun syncClock() = run {
        graph.scanner.syncClock()
        clock = graph.scanner.clock()
        message = "Scanner clock set to phone time"
    }

    private fun run(block: suspend () -> Unit) {
        if (busy || graph.scanner.host.isEmpty()) return
        busy = true
        error = null
        message = null
        viewModelScope.launch {
            try {
                block()
            } catch (e: Exception) {
                error = e.userMessage()
            } finally {
                busy = false
            }
        }
    }
}

@Composable
fun SettingsScreen() {
    val vm = graphViewModel { SettingsViewModel(it) }
    val graph = appGraph()
    val settings by graph.settings.collectAsStateWithLifecycle()
    val status by graph.scanner.status.collectAsStateWithLifecycle()
    val context = LocalContext.current
    var ip by rememberSaveable { mutableStateOf("") }
    var edited by rememberSaveable { mutableStateOf(false) }
    LaunchedEffect(settings.scannerIp) { if (!edited) ip = settings.scannerIp }
    LaunchedEffect(settings.scannerIp) { if (settings.scannerIp.isNotBlank() && vm.model == null) vm.test() }
    val valid = SettingsRepository.isValidIpv4(ip)

    Column(
        Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        SectionCard(title = "Scanner") {
            OutlinedTextField(
                value = ip,
                onValueChange = { ip = it.filter { c -> c.isDigit() || c == '.' }.take(15); edited = true },
                label = { Text("Scanner IP address") },
                placeholder = { Text("192.168.1.50") },
                singleLine = true,
                isError = ip.isNotEmpty() && !valid,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Decimal, imeAction = ImeAction.Done),
                modifier = Modifier.fillMaxWidth(),
            )
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                Button(onClick = { edited = false; vm.save(ip) }, enabled = valid) { Text("Save") }
                OutlinedButton(onClick = { vm.discover(context) }, enabled = !vm.discovering) { Text("Discover") }
                if (vm.discovering) CircularProgressIndicator(Modifier.padding(4.dp), strokeWidth = 2.dp)
            }
            ErrorText(vm.discoverError)
            vm.found?.forEach { s ->
                Row(
                    Modifier.fillMaxWidth().clickable {
                        ip = s.ip
                        edited = false
                        vm.save(s.ip)
                    }.padding(vertical = 8.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Text(s.ip, style = MaterialTheme.typography.bodyLarge, modifier = Modifier.weight(1f))
                    Text(s.reply, color = MaterialTheme.colorScheme.primary)
                    Spacer(Modifier.width(8.dp))
                    Text("Use", color = MaterialTheme.colorScheme.secondary)
                }
            }
        }

        SectionCard(title = "Connection") {
            Row(verticalAlignment = Alignment.CenterVertically) {
                StatusDot(if (settings.scannerIp.isBlank()) null else status.online)
                Spacer(Modifier.width(8.dp))
                Text(
                    when {
                        settings.scannerIp.isBlank() -> "No scanner set"
                        status.online -> "Online"
                        status.everConnected -> "Not responding"
                        else -> "Not connected"
                    },
                )
            }
            LabelValue("Latency", status.latencyMs?.let { "%.0f ms".format(java.util.Locale.UK, it) } ?: "")
            LabelValue("Model", vm.model.orEmpty())
            LabelValue("Firmware", vm.firmware.orEmpty())
            ErrorText(status.lastError?.takeIf { !status.online })
            OutlinedButton(onClick = { vm.test() }, enabled = !vm.busy && settings.scannerIp.isNotBlank()) { Text("Test connection") }
        }

        SectionCard(title = "Clock") {
            val c = vm.clock
            LabelValue("Scanner time", c?.time?.format(DateTimeFormatter.ofPattern("dd/MM/yyyy HH:mm:ss")).orEmpty())
            LabelValue("RTC", when (c?.rtcOk) { true -> "OK"; false -> "Fault"; null -> "" })
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedButton(onClick = { vm.readClock() }, enabled = !vm.busy && settings.scannerIp.isNotBlank()) { Text("Read") }
                Button(onClick = { vm.syncClock() }, enabled = !vm.busy && settings.scannerIp.isNotBlank()) { Text("Sync to phone") }
            }
            vm.message?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.secondary) }
            ErrorText(vm.error)
        }

        SectionCard(title = "App") {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text("Keep screen on")
                    Text("While the app is open", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                Switch(checked = settings.keepScreenOn, onCheckedChange = { vm.setKeepScreenOn(it) })
            }
            LabelValue("Version", "${BuildConfig.VERSION_NAME} (${BuildConfig.VERSION_CODE})")
        }
    }
}
