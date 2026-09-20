package uk.co.twoe0lxy.sds200.audio

import android.content.Context
import android.content.Intent
import androidx.core.content.ContextCompat
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/** UI-facing handle for the audio foreground service. */
class AudioController {
    private val _state = MutableStateFlow(AudioState())
    val state: StateFlow<AudioState> = _state.asStateFlow()

    internal fun publish(s: AudioState) {
        _state.value = s
    }

    fun start(context: Context, host: String) {
        if (host.isBlank()) {
            publish(AudioState(AudioPhase.ERROR, error = "No scanner IP address set"))
            return
        }
        publish(AudioState(AudioPhase.STARTING, stage = "Starting service"))
        val i = Intent(context, AudioService::class.java)
            .setAction(AudioService.ACTION_START)
            .putExtra(AudioService.EXTRA_HOST, host)
        ContextCompat.startForegroundService(context, i)
    }

    fun stop(context: Context) {
        if (!_state.value.active) return
        val i = Intent(context, AudioService::class.java).setAction(AudioService.ACTION_STOP)
        runCatching { context.startService(i) }
    }
}
