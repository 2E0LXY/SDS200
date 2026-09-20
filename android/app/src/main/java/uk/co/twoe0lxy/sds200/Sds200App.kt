package uk.co.twoe0lxy.sds200

import android.app.Application
import android.util.Xml
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import uk.co.twoe0lxy.sds200.audio.AudioController
import uk.co.twoe0lxy.sds200.data.AppSettings
import uk.co.twoe0lxy.sds200.data.SettingsRepository
import uk.co.twoe0lxy.sds200.net.ScannerClient

/** Process-wide singletons (no DI framework needed). */
class AppGraph(app: Application) {
    val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    val settingsRepo = SettingsRepository(app)
    val scanner = ScannerClient(parserFactory = { Xml.newPullParser() })
    val audio = AudioController()
    val settings: StateFlow<AppSettings> =
        settingsRepo.settings.stateIn(scope, SharingStarted.Eagerly, AppSettings())

    init {
        scope.launch { settings.collect { scanner.host = it.scannerIp } }
    }
}

class Sds200App : Application() {
    lateinit var graph: AppGraph
        private set

    override fun onCreate() {
        super.onCreate()
        graph = AppGraph(this)
    }
}
