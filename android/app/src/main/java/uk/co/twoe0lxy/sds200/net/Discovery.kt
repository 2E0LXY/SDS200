package uk.co.twoe0lxy.sds200.net

import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.Inet4Address
import java.net.InetAddress
import java.net.SocketTimeoutException

data class DiscoveredScanner(val ip: String, val reply: String)

/**
 * Finds scanners by sending MDL to every host on the phone's Wi-Fi /24 at once
 * from one socket and collecting the replies for [timeoutMs].
 */
object Discovery {
    fun localWifiIpv4(context: Context): Inet4Address? {
        val cm = context.getSystemService(ConnectivityManager::class.java) ?: return null
        val candidates = buildList {
            cm.activeNetwork?.let { add(it) }
        }
        for (n in candidates) {
            val caps = cm.getNetworkCapabilities(n)
            val lp = cm.getLinkProperties(n) ?: continue
            val v4 = lp.linkAddresses.map { it.address }.filterIsInstance<Inet4Address>()
                .firstOrNull { !it.isLoopbackAddress && !it.isLinkLocalAddress }
            if (v4 != null && (caps == null || caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) ||
                    caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET))
            ) {
                return v4
            }
        }
        return null
    }

    suspend fun scan(local: Inet4Address, timeoutMs: Int = 900): List<DiscoveredScanner> = withContext(Dispatchers.IO) {
        val base = local.address.copyOf()
        val found = LinkedHashMap<String, DiscoveredScanner>()
        DatagramSocket().use { s ->
            val probe = "MDL\r".toByteArray(Charsets.ISO_8859_1)
            for (h in 1..254) {
                base[3] = h.toByte()
                val addr = InetAddress.getByAddress(base)
                if (addr == local) continue
                runCatching { s.send(DatagramPacket(probe, probe.size, addr, ScannerClient.UDP_PORT)) }
            }
            val deadline = System.nanoTime() + timeoutMs * 1_000_000L
            val buf = ByteArray(2048)
            while (true) {
                val remaining = ((deadline - System.nanoTime()) / 1_000_000L).toInt()
                if (remaining <= 0) break
                s.soTimeout = remaining
                val p = DatagramPacket(buf, buf.size)
                try {
                    s.receive(p)
                } catch (_: SocketTimeoutException) {
                    break
                }
                val text = String(p.data, p.offset, p.length, Charsets.ISO_8859_1).trim()
                val ip = p.address.hostAddress ?: continue
                if (text.startsWith("MDL,", ignoreCase = true) && text.contains("SDS200", ignoreCase = true)) {
                    found[ip] = DiscoveredScanner(ip, text.substringBefore('\r').removePrefix("MDL,"))
                }
            }
        }
        found.values.toList()
    }
}
