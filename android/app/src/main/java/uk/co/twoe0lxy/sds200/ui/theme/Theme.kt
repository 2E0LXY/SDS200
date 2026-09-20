package uk.co.twoe0lxy.sds200.ui.theme

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

val Amber = Color(0xFFFFB300)
val AmberDim = Color(0xFF8A6100)
val LcdBackground = Color(0xFF0B0F0C)
val LcdText = Color(0xFFE8F5E9)
val Ok = Color(0xFF66BB6A)
val Bad = Color(0xFFEF5350)

private val DarkScheme = darkColorScheme(
    primary = Amber,
    onPrimary = Color(0xFF1F1400),
    primaryContainer = Color(0xFF4A3500),
    onPrimaryContainer = Color(0xFFFFDEA6),
    secondary = Color(0xFF80CBC4),
    onSecondary = Color(0xFF00201D),
    secondaryContainer = Color(0xFF1F3B38),
    onSecondaryContainer = Color(0xFFB2DFDB),
    background = Color(0xFF101213),
    onBackground = Color(0xFFE3E3E3),
    surface = Color(0xFF101213),
    onSurface = Color(0xFFE3E3E3),
    surfaceVariant = Color(0xFF2A2D2F),
    onSurfaceVariant = Color(0xFFC4C7C5),
    surfaceContainer = Color(0xFF1A1C1E),
    surfaceContainerHigh = Color(0xFF232628),
    surfaceContainerHighest = Color(0xFF2D3032),
    error = Bad,
)

@Composable
fun Sds200Theme(content: @Composable () -> Unit) {
    MaterialTheme(colorScheme = DarkScheme, content = content)
}
