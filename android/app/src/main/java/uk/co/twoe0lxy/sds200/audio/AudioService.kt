package uk.co.twoe0lxy.sds200.audio

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.graphics.drawable.Icon
import android.media.AudioAttributes
import android.media.AudioFocusRequest
import android.media.AudioManager
import android.media.MediaMetadata
import android.media.session.MediaSession
import android.media.session.PlaybackState
import android.net.ConnectivityManager
import android.net.Network
import android.net.wifi.WifiManager
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.os.PowerManager
import androidx.core.app.ServiceCompat
import uk.co.twoe0lxy.sds200.MainActivity
import uk.co.twoe0lxy.sds200.R
import uk.co.twoe0lxy.sds200.Sds200App

/**
 * Foreground media-playback service that owns the one RTSP audio session so
 * listening continues with the screen off. Holds a Wi-Fi lock and a partial
 * wake lock while streaming; tears the session down on Stop, on network loss,
 * on permanent audio-focus loss and when destroyed.
 */
class AudioService : Service() {
    private var session: AudioSession? = null
    private var mediaSession: MediaSession? = null
    private var wifiLock: WifiManager.WifiLock? = null
    private var wakeLock: PowerManager.WakeLock? = null
    private var focusRequest: AudioFocusRequest? = null
    private var networkCallback: ConnectivityManager.NetworkCallback? = null
    private val main = Handler(Looper.getMainLooper())
    private var lastPhase: AudioPhase? = null

    private val controller get() = (application as Sds200App).graph.audio

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        createChannel()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_START -> {
                val host = intent.getStringExtra(EXTRA_HOST).orEmpty()
                goForeground("Connecting to $host…")
                startListening(host)
            }
            ACTION_STOP -> shutdown(null)
            else -> {
                // Restarted without an intent: never resume a session silently.
                goForeground("Stopping…")
                shutdown(null)
            }
        }
        return START_NOT_STICKY
    }

    private fun startListening(host: String) {
        if (session != null) return
        acquireLocks()
        requestFocus()
        registerNetworkCallback()
        ensureMediaSession()
        val s = AudioSession(host) { st -> main.post { onSessionState(st) } }
        session = s
        s.start()
    }

    private fun onSessionState(st: AudioState) {
        if (session == null) {
            // Late report from a session that was already shut down: keep any
            // error already shown (e.g. "Network lost"), otherwise record the final state.
            if (!st.active && controller.state.value.phase != AudioPhase.ERROR) controller.publish(st)
            return
        }
        controller.publish(st)
        if (st.phase != lastPhase) {
            lastPhase = st.phase
            val text = when (st.phase) {
                AudioPhase.STARTING -> "Connecting…"
                AudioPhase.WAITING_RTP -> "Waiting for audio…"
                AudioPhase.STREAMING -> "Listening live"
                AudioPhase.ERROR -> "Stopped: ${st.error}"
                AudioPhase.IDLE -> "Stopped"
            }
            if (st.active) updateNotification(text)
        }
        if (!st.active) shutdown(st)
    }

    /** Stops everything. [final] is the state reported by the finished session, if any. */
    private fun shutdown(final: AudioState?) {
        session?.stop() // worker thread sends TEARDOWN
        session = null
        if (final == null && controller.state.value.active) {
            controller.publish(AudioState(AudioPhase.IDLE, controller.state.value.packets, "Stopped"))
        }
        releaseLocks()
        abandonFocus()
        unregisterNetworkCallback()
        mediaSession?.let { it.isActive = false; it.release() }
        mediaSession = null
        ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    override fun onDestroy() {
        session?.stop()
        session = null
        releaseLocks()
        abandonFocus()
        unregisterNetworkCallback()
        mediaSession?.release()
        mediaSession = null
        if (controller.state.value.active) controller.publish(AudioState(AudioPhase.IDLE, stage = "Stopped"))
        super.onDestroy()
    }

    override fun onTaskRemoved(rootIntent: Intent?) {
        // Keep listening when the task is swiped away; the notification's Stop ends it.
        super.onTaskRemoved(rootIntent)
    }

    // ------------------------------------------------------------ notification

    private fun createChannel() {
        val nm = getSystemService(NotificationManager::class.java)
        val ch = NotificationChannel(CHANNEL_ID, getString(R.string.notification_channel), NotificationManager.IMPORTANCE_LOW)
        ch.description = getString(R.string.notification_channel_desc)
        ch.setShowBadge(false)
        nm.createNotificationChannel(ch)
    }

    private fun ensureMediaSession() {
        if (mediaSession != null) return
        val ms = MediaSession(this, "SDS200Remote")
        ms.setCallback(object : MediaSession.Callback() {
            override fun onStop() = shutdown(null)
            override fun onPause() = shutdown(null)
        })
        ms.setPlaybackState(
            PlaybackState.Builder()
                .setActions(PlaybackState.ACTION_STOP or PlaybackState.ACTION_PAUSE)
                .setState(PlaybackState.STATE_PLAYING, PlaybackState.PLAYBACK_POSITION_UNKNOWN, 1f)
                .build(),
        )
        ms.setMetadata(
            MediaMetadata.Builder()
                .putString(MediaMetadata.METADATA_KEY_TITLE, getString(R.string.app_name))
                .putString(MediaMetadata.METADATA_KEY_ARTIST, getString(R.string.notification_live_audio))
                .build(),
        )
        ms.isActive = true
        mediaSession = ms
    }

    private fun buildNotification(text: String): Notification {
        val open = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val stop = PendingIntent.getService(
            this, 1, Intent(this, AudioService::class.java).setAction(ACTION_STOP),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val style = Notification.MediaStyle().setShowActionsInCompactView(0)
        mediaSession?.let { style.setMediaSession(it.sessionToken) }
        return Notification.Builder(this, CHANNEL_ID)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(getString(R.string.app_name))
            .setContentText(text)
            .setContentIntent(open)
            .setOngoing(true)
            .setCategory(Notification.CATEGORY_TRANSPORT)
            .setVisibility(Notification.VISIBILITY_PUBLIC)
            .addAction(
                Notification.Action.Builder(Icon.createWithResource(this, R.drawable.ic_stop), getString(R.string.stop), stop).build(),
            )
            .setStyle(style)
            .build()
    }

    private fun goForeground(text: String) {
        ensureMediaSession()
        val type = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PLAYBACK else 0
        ServiceCompat.startForeground(this, NOTIFICATION_ID, buildNotification(text), type)
    }

    private fun updateNotification(text: String) {
        val nm = getSystemService(NotificationManager::class.java)
        runCatching { nm.notify(NOTIFICATION_ID, buildNotification(text)) }
    }

    // ------------------------------------------------------------ locks, focus, network

    private fun acquireLocks() {
        if (wifiLock == null) {
            val wm = applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager
            val mode = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                WifiManager.WIFI_MODE_FULL_LOW_LATENCY
            } else {
                @Suppress("DEPRECATION")
                WifiManager.WIFI_MODE_FULL_HIGH_PERF
            }
            wifiLock = wm.createWifiLock(mode, "SDS200Remote:audio").apply { setReferenceCounted(false); acquire() }
        }
        if (wakeLock == null) {
            val pm = getSystemService(PowerManager::class.java)
            wakeLock = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "SDS200Remote:audio").apply {
                setReferenceCounted(false)
                acquire(12 * 60 * 60 * 1000L)
            }
        }
    }

    private fun releaseLocks() {
        wifiLock?.let { if (it.isHeld) it.release() }
        wifiLock = null
        wakeLock?.let { if (it.isHeld) it.release() }
        wakeLock = null
    }

    private val focusListener = AudioManager.OnAudioFocusChangeListener { change ->
        if (change == AudioManager.AUDIOFOCUS_LOSS) main.post { shutdown(null) }
    }

    private fun requestFocus() {
        val am = getSystemService(AudioManager::class.java)
        val req = AudioFocusRequest.Builder(AudioManager.AUDIOFOCUS_GAIN)
            .setAudioAttributes(
                AudioAttributes.Builder().setUsage(AudioAttributes.USAGE_MEDIA)
                    .setContentType(AudioAttributes.CONTENT_TYPE_SPEECH).build(),
            )
            .setOnAudioFocusChangeListener(focusListener, main)
            .build()
        focusRequest = req
        am.requestAudioFocus(req)
    }

    private fun abandonFocus() {
        val req = focusRequest ?: return
        getSystemService(AudioManager::class.java).abandonAudioFocusRequest(req)
        focusRequest = null
    }

    private fun registerNetworkCallback() {
        if (networkCallback != null) return
        val cm = getSystemService(ConnectivityManager::class.java)
        val cb = object : ConnectivityManager.NetworkCallback() {
            override fun onLost(network: Network) {
                main.post {
                    controller.publish(AudioState(AudioPhase.ERROR, controller.state.value.packets, "Stopped", "Network lost"))
                    shutdown(controller.state.value)
                }
            }
        }
        runCatching { cm.registerDefaultNetworkCallback(cb) }.onSuccess { networkCallback = cb }
    }

    private fun unregisterNetworkCallback() {
        val cb = networkCallback ?: return
        runCatching { getSystemService(ConnectivityManager::class.java).unregisterNetworkCallback(cb) }
        networkCallback = null
    }

    companion object {
        const val ACTION_START = "uk.co.twoe0lxy.sds200.action.START_AUDIO"
        const val ACTION_STOP = "uk.co.twoe0lxy.sds200.action.STOP_AUDIO"
        const val EXTRA_HOST = "host"
        private const val CHANNEL_ID = "live_audio"
        private const val NOTIFICATION_ID = 42
    }
}
