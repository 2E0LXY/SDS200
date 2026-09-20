package uk.co.twoe0lxy.sds200.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.platform.LocalContext
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory
import kotlinx.coroutines.delay
import uk.co.twoe0lxy.sds200.AppGraph
import uk.co.twoe0lxy.sds200.Sds200App

@Composable
inline fun <reified VM : ViewModel> graphViewModel(crossinline create: (AppGraph) -> VM): VM {
    val app = LocalContext.current.applicationContext as Sds200App
    return viewModel(factory = viewModelFactory { initializer { create(app.graph) } })
}

@Composable
fun appGraph(): AppGraph = (LocalContext.current.applicationContext as Sds200App).graph

/** Runs [block] every [intervalMs] only while the screen is at least STARTED (visible). */
@Composable
fun PollWhileVisible(intervalMs: Long, block: suspend () -> Unit) {
    val owner = LocalLifecycleOwner.current
    val current = rememberUpdatedState(block)
    LaunchedEffect(owner) {
        owner.lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) {
                current.value()
                delay(intervalMs)
            }
        }
    }
}

fun Throwable.userMessage(): String = message ?: javaClass.simpleName
