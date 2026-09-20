package uk.co.twoe0lxy.sds200.data

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.emptyPreferences
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.catch
import kotlinx.coroutines.flow.map
import java.io.IOException

private val Context.dataStore: DataStore<Preferences> by preferencesDataStore(name = "settings")

data class AppSettings(
    val scannerIp: String = "",
    val keepScreenOn: Boolean = false,
)

class SettingsRepository(private val context: Context) {
    private val ipKey = stringPreferencesKey("scanner_ip")
    private val keepOnKey = booleanPreferencesKey("keep_screen_on")

    val settings: Flow<AppSettings> = context.dataStore.data
        .catch { e -> if (e is IOException) emit(emptyPreferences()) else throw e }
        .map { p -> AppSettings(p[ipKey].orEmpty(), p[keepOnKey] ?: false) }

    suspend fun setScannerIp(ip: String) {
        context.dataStore.edit { it[ipKey] = ip.trim() }
    }

    suspend fun setKeepScreenOn(on: Boolean) {
        context.dataStore.edit { it[keepOnKey] = on }
    }

    companion object {
        private val IPV4 = Regex("""^((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)$""")
        fun isValidIpv4(s: String) = IPV4.matches(s.trim())
    }
}
