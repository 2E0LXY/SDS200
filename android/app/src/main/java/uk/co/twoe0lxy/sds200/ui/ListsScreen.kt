package uk.co.twoe0lxy.sds200.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.itemsIndexed
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.PrimaryTabRow
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.Tab
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.launch
import uk.co.twoe0lxy.sds200.AppGraph
import uk.co.twoe0lxy.sds200.protocol.GltRecord
import uk.co.twoe0lxy.sds200.protocol.Replies

/** One level of the memory browser. [kinds] are fetched with [parent] and concatenated. */
data class MemoryLevel(val title: String, val kinds: List<String>, val parent: String?)

class ListsViewModel(private val graph: AppGraph) : ViewModel() {
    var fqk by mutableStateOf<List<Int>?>(null)
        private set
    var svc by mutableStateOf<List<Boolean>?>(null)
        private set
    var memoryStack by mutableStateOf(listOf(MemoryLevel("Favourites", listOf("FL"), null)))
        private set
    var memoryRows by mutableStateOf<List<GltRecord>?>(null)
        private set
    var busy by mutableStateOf(false)
        private set
    var error by mutableStateOf<String?>(null)
        private set

    private fun run(block: suspend () -> Unit) {
        if (busy) return
        busy = true
        error = null
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

    fun loadFqk() = run { fqk = graph.scanner.fqk() }

    fun toggleFqk(index: Int) = run {
        val cur = fqk ?: graph.scanner.fqk()
        if (cur[index] == 2) return@run
        val next = cur.toMutableList().also { it[index] = if (it[index] == 1) 0 else 1 }
        graph.scanner.setFqk(next)
        fqk = graph.scanner.fqk()
        if (fqk?.get(index) != next[index]) error = "Scanner did not confirm quick key ${index.toString().padStart(2, '0')}"
    }

    fun loadSvc() = run { svc = graph.scanner.serviceTypes() }

    fun toggleSvc(slot: Int) = run {
        val cur = svc ?: graph.scanner.serviceTypes()
        val next = cur.toMutableList().also { it[slot] = !it[slot] }
        graph.scanner.setServiceTypes(next)
        svc = graph.scanner.serviceTypes()
        if (svc?.get(slot) != next[slot]) error = "Scanner did not confirm ${Replies.serviceTypeName(slot)}"
    }

    fun loadMemory() = run {
        val level = memoryStack.last()
        val rows = ArrayList<GltRecord>()
        var failure: Exception? = null
        var anyOk = false
        // SITE (trunked only) or TGID lists may be rejected for some systems; keep what works.
        for (k in level.kinds) {
            try {
                rows += graph.scanner.list(k, level.parent)
                anyOk = true
            } catch (e: Exception) {
                failure = e
            }
        }
        memoryRows = rows
        if (!anyOk && failure != null) throw failure
    }

    fun open(r: GltRecord) {
        val next = when (r.tag.uppercase()) {
            "FL" -> MemoryLevel(r.label, listOf("SYS"), r.index)
            "SYS" -> MemoryLevel(r.label, listOf("DEPT", "SITE"), r.index)
            "DEPT" -> MemoryLevel(r.label, listOf("CFREQ", "TGID"), r.index)
            "SITE" -> MemoryLevel(r.label, listOf("SFREQ"), r.index)
            else -> null
        } ?: return
        if (r.index.isEmpty()) return
        memoryStack = memoryStack + next
        memoryRows = null
        loadMemory()
    }

    fun back(toDepth: Int) {
        if (toDepth < 1 || toDepth >= memoryStack.size) return
        memoryStack = memoryStack.take(toDepth)
        memoryRows = null
        loadMemory()
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ListsScreen() {
    val vm = graphViewModel { ListsViewModel(it) }
    var tab by rememberSaveable { mutableIntStateOf(0) }
    Column(Modifier.fillMaxSize()) {
        PrimaryTabRow(selectedTabIndex = tab) {
            listOf("Quick keys", "Service types", "Memory").forEachIndexed { i, t ->
                Tab(selected = tab == i, onClick = { tab = i }, text = { Text(t) })
            }
        }
        Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.weight(1f)) { ErrorText(vm.error) }
            if (vm.busy) CircularProgressIndicator(Modifier.padding(4.dp), strokeWidth = 2.dp)
        }
        when (tab) {
            0 -> QuickKeys(vm)
            1 -> ServiceTypes(vm)
            else -> Memory(vm)
        }
    }
}

@Composable
private fun QuickKeys(vm: ListsViewModel) {
    LaunchedEffect(Unit) { if (vm.fqk == null) vm.loadFqk() }
    Column(Modifier.padding(horizontal = 12.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                "Favourites Quick Keys: tap to switch on or off. Greyed keys are unassigned.",
                style = MaterialTheme.typography.bodySmall,
                modifier = Modifier.weight(1f),
            )
            TextButton(onClick = { vm.loadFqk() }) { Text("Refresh") }
        }
        val states = vm.fqk
        if (states != null) {
            LazyVerticalGrid(columns = GridCells.Fixed(10), horizontalArrangement = Arrangement.spacedBy(4.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                itemsIndexed(states) { i, s ->
                    val colours = MaterialTheme.colorScheme
                    Surface(
                        shape = RoundedCornerShape(6.dp),
                        color = when (s) {
                            1 -> colours.primary
                            0 -> colours.surfaceContainerHighest
                            else -> colours.surfaceContainer
                        },
                        contentColor = when (s) {
                            1 -> colours.onPrimary
                            0 -> colours.onSurface
                            else -> colours.onSurface.copy(alpha = 0.3f)
                        },
                        modifier = Modifier.aspectRatio(1f).clickable(enabled = s != 2 && !vm.busy) { vm.toggleFqk(i) },
                    ) {
                        Box(contentAlignment = Alignment.Center) { Text(i.toString().padStart(2, '0'), fontSize = 12.sp) }
                    }
                }
            }
        }
    }
}

@Composable
private fun ServiceTypes(vm: ListsViewModel) {
    LaunchedEffect(Unit) { if (vm.svc == null) vm.loadSvc() }
    val states = vm.svc
    LazyColumn(Modifier.fillMaxSize().padding(horizontal = 16.dp)) {
        item {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text("Service types scanned in scan mode.", style = MaterialTheme.typography.bodySmall, modifier = Modifier.weight(1f))
                TextButton(onClick = { vm.loadSvc() }) { Text("Refresh") }
            }
        }
        if (states != null) {
            val slots = states.indices.filter { Replies.serviceTypeName(it).isNotBlank() }
            items(slots) { slot ->
                Row(Modifier.fillMaxWidth().padding(vertical = 2.dp), verticalAlignment = Alignment.CenterVertically) {
                    Text(Replies.serviceTypeName(slot), modifier = Modifier.weight(1f))
                    Switch(checked = states[slot], onCheckedChange = { vm.toggleSvc(slot) }, enabled = !vm.busy)
                }
            }
        }
    }
}

@Composable
private fun Memory(vm: ListsViewModel) {
    LaunchedEffect(Unit) { if (vm.memoryRows == null) vm.loadMemory() }
    Column(Modifier.fillMaxSize().padding(horizontal = 12.dp)) {
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Row(Modifier.weight(1f)) {
                vm.memoryStack.forEachIndexed { i, level ->
                    val last = i == vm.memoryStack.lastIndex
                    Text(
                        (if (i > 0) " › " else "") + level.title,
                        style = MaterialTheme.typography.labelLarge,
                        color = if (last) MaterialTheme.colorScheme.onSurface else MaterialTheme.colorScheme.primary,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = if (last) Modifier else Modifier.clickable { vm.back(i + 1) },
                    )
                }
            }
            OutlinedButton(onClick = { vm.loadMemory() }, enabled = !vm.busy) { Text("Reload") }
        }
        val rows = vm.memoryRows
        if (rows != null && rows.isEmpty()) {
            Text("No entries at this level.", modifier = Modifier.padding(16.dp), color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        LazyColumn(Modifier.fillMaxSize()) {
            items(rows.orEmpty()) { r ->
                val openable = r.tag.uppercase() in setOf("FL", "SYS", "DEPT", "SITE")
                Column(
                    Modifier
                        .fillMaxWidth()
                        .clickable(enabled = openable && !vm.busy) { vm.open(r) }
                        .padding(vertical = 10.dp, horizontal = 4.dp),
                ) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(r.label, style = MaterialTheme.typography.bodyLarge, modifier = Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis)
                        Text(if (openable) "›" else r.tag, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    if (r.meta.isNotBlank()) {
                        Text(r.meta, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                }
                HorizontalDivider()
            }
        }
    }
}
