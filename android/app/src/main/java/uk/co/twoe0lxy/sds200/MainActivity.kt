package uk.co.twoe0lxy.sds200

import android.os.Bundle
import android.view.WindowManager
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.annotation.DrawableRes
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavDestination.Companion.hierarchy
import androidx.navigation.NavGraph.Companion.findStartDestination
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import uk.co.twoe0lxy.sds200.ui.ListsScreen
import uk.co.twoe0lxy.sds200.ui.LiveScreen
import uk.co.twoe0lxy.sds200.ui.RemoteScreen
import uk.co.twoe0lxy.sds200.ui.SettingsScreen
import uk.co.twoe0lxy.sds200.ui.StatusDot
import uk.co.twoe0lxy.sds200.ui.WaterfallScreen
import uk.co.twoe0lxy.sds200.ui.appGraph
import uk.co.twoe0lxy.sds200.ui.theme.Sds200Theme

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent {
            Sds200Theme {
                val graph = appGraph()
                val settings by graph.settings.collectAsStateWithLifecycle()
                LaunchedEffect(settings.keepScreenOn) {
                    if (settings.keepScreenOn) {
                        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
                    } else {
                        window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
                    }
                }
                AppRoot()
            }
        }
    }
}

private enum class Dest(val route: String, val label: String, @param:DrawableRes val icon: Int) {
    LIVE("live", "Live", R.drawable.ic_nav_live),
    REMOTE("remote", "Remote", R.drawable.ic_nav_remote),
    LISTS("lists", "Lists", R.drawable.ic_nav_lists),
    WATERFALL("waterfall", "Waterfall", R.drawable.ic_nav_waterfall),
    SETTINGS("settings", "Settings", R.drawable.ic_nav_settings),
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun AppRoot() {
    val nav = rememberNavController()
    val graph = appGraph()
    val status by graph.scanner.status.collectAsStateWithLifecycle()
    val backStack by nav.currentBackStackEntryAsState()
    val current = backStack?.destination
    fun go(route: String) {
        nav.navigate(route) {
            popUpTo(nav.graph.findStartDestination().id) { saveState = true }
            launchSingleTop = true
            restoreState = true
        }
    }
    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text("SDS200 Remote", modifier = Modifier.weight(1f))
                        if (status.host.isNotEmpty()) {
                            StatusDot(status.online)
                            Spacer(Modifier.width(6.dp))
                            Text(
                                status.host + (status.latencyMs?.takeIf { status.online }?.let { "  %.0f ms".format(java.util.Locale.UK, it) } ?: ""),
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                modifier = Modifier.padding(end = 12.dp),
                            )
                        }
                    }
                },
            )
        },
        bottomBar = {
            NavigationBar {
                for (d in Dest.entries) {
                    NavigationBarItem(
                        selected = current?.hierarchy?.any { it.route == d.route } == true,
                        onClick = { go(d.route) },
                        icon = { Icon(painterResource(d.icon), contentDescription = d.label) },
                        label = { Text(d.label) },
                    )
                }
            }
        },
    ) { padding ->
        NavHost(nav, startDestination = Dest.LIVE.route, modifier = Modifier.padding(padding).fillMaxWidth()) {
            composable(Dest.LIVE.route) { LiveScreen(onOpenSettings = { go(Dest.SETTINGS.route) }) }
            composable(Dest.REMOTE.route) { RemoteScreen() }
            composable(Dest.LISTS.route) { ListsScreen() }
            composable(Dest.WATERFALL.route) { WaterfallScreen() }
            composable(Dest.SETTINGS.route) { SettingsScreen() }
        }
    }
}
