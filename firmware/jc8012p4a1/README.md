# SDS200 Panel – Guition JC8012P4A1 firmware

Standalone touchscreen remote for the Uniden SDS200 / SDS200E, running on the
Guition **JC8012P4A1** (ESP32-P4, 10.1" 800×1280 MIPI-DSI panel used in
landscape, ESP32-C6 Wi-Fi co-processor, ES8311 codec + speaker). Written in C
against ESP-IDF v5.4.2 and LVGL 9 – no Arduino, no MicroPython.

It talks to the scanner exactly like the desktop WebApp in the repository root:
UDP 50536 for control and RTSP/RTP (TCP 554) for live audio.

## Features

| Page | What it does |
|---|---|
| **Live** | Mode / screen, system, department, site, channel, frequency, TGID, unit ID, modulation, service type, 5-bar signal + RSSI, hold tags. Buttons: Sys Hold (A), Dept Hold (B), Hold (C), Avoid (L), Prev (<), Next (>). Scanner volume 0–29 and squelch 0–19 sliders. GSI polled at ~2 Hz. |
| **Remote** | Mirror of the scanner display from `STS` (~3 Hz and immediately after each key), with large/small fonts, reverse (`*`) and underline (`_`). Keypad 0–9, `.`/NO, E/YES, MENU, FUNC, REPLAY, AVOID, SERVICE, RANGE, ZIP, soft keys A/B/C, rotary left/push/right, volume and squelch knob pushes. |
| **Quick Keys** | Favourites Quick Keys 00–99 (`FQK`): green = on, red = off, grey = unused. Tap to toggle. |
| **Waterfall** | `JPM,WF_MODE` → `PWF,1,ON` → `GWF,1,ON` polled at ~4 Hz; 240-bin spectrum line plus scrolling waterfall. Stop sends `GWF,1,OFF`, `PWF,1,OFF`, `JPM,SCN_MODE`. |
| **Audio** | Listen/Stop, panel speaker volume, packet/byte/loss/underrun counters and buffer depth. PCMU 8 kHz → PCM16 → 200 ms jitter buffer → 16 kHz I2S to the ES8311/NS4150B. |
| **Settings** | Wi-Fi setup (scan list + on-screen keyboard), scanner IP (manual or **Discover**), backlight, flip 180°, panel revision (applied on reboot), firmware/IDF version, reboot. |

HTTP API on port 80 (enable/disable with `CONFIG_SDS_WEB_SERVER`, on by default):

```
GET  /api/state          {"online":true,"mode":"Scan Mode","system":"LEEDS",...,"channel_hold":false}
POST /api/control/key    {"key":"M"}
```

The API has **no authentication** – use it only on a trusted network.

### Audio rules (same as the WebApp)
- The scanner allows **one** RTSP session; a leaked session locks TCP 554 until
  the scanner is power-cycled. The panel sends `TEARDOWN` on Stop, when Wi-Fi
  drops, when the RTP stream stalls for 10 s, and before every reboot initiated
  from the UI.
- Port 554 is only opened when you press **Listen** – never as a probe.
- After `SETUP`, and with every 15 s `GET_PARAMETER` keepalive, the panel sends
  the 4-byte datagram `CE FA ED FE` from its RTP port to the scanner's RTP
  source port to open stateful firewalls/NAT.

## Flashing

CI (`.github/workflows/firmware.yml`) produces **`SDS200-Panel-JC8012P4A1.factory.bin`**,
a merged image (bootloader + partition table + app) to be written at offset `0x0`.
It is attached to GitHub releases for `v*` tags and uploaded as a workflow
artifact on every build.

1. Connect the board's **USB-UART** port (CH340) – not the native USB port.
2. Hold **BOOT**, tap **RST** (or plug in while holding BOOT), release BOOT.
3. Flash:

   ```bash
   pip install esptool
   esptool.py --chip esp32p4 -p /dev/ttyUSB0 -b 460800 write_flash 0x0 SDS200-Panel-JC8012P4A1.factory.bin
   ```

   On Windows use the CH340's `COMx` port. If it fails at 460800, try `-b 115200`.
4. Press **RST**. Serial log: 115200 baud on the same port.

To erase saved settings (Wi-Fi, scanner IP, panel revision) run
`esptool.py --chip esp32p4 -p PORT erase_flash` before writing the image.

## Building

```bash
. ~/esp/esp-idf/export.sh            # ESP-IDF v5.4.2
cd firmware/jc8012p4a1
idf.py set-target esp32p4
idf.py build
idf.py merge-bin -o SDS200-Panel-JC8012P4A1.factory.bin   # written to build/
idf.py -p /dev/ttyUSB0 flash monitor                     # or flash directly
```

Dependencies are fetched by the component manager (`main/idf_component.yml`,
versions pinned in `dependencies.lock`): LVGL 9.3, esp_lvgl_port (PPA
rotation), esp_lcd_jd9365, esp_lcd_touch + `mangoo1/esp_lcd_touch_gsl3680`,
esp_codec_dev, esp_hosted (SDIO host) and esp_wifi_remote.

Board options live under **`idf.py menuconfig` → SDS200 Panel**.

## Panel revision

Two different JD9365 panels were fitted:

| Batch number (sticker on the back) | Setting | Init sequence / timing |
|---|---|---|
| **2627 or earlier** | *Original* | ESPHome `GUITION_JC8012P4A1`: 60 MHz pclk, 1 Gbps/lane, VBP 8 |
| **2628 or later** | *V2* (default) | ESPHome `GUITION_JC8012P4A1_V2`: 70 MHz pclk, 1.5 Gbps/lane, VBP 10 |

The default comes from Kconfig (`CONFIG_SDS_PANEL_REV_*`). If the screen stays
black or shows noise, the other revision is probably needed: change it in
**Settings → Panel revision → Apply & reboot** (if you can still see the UI),
or rebuild with the other Kconfig choice, or `erase_flash` and flash a build
with the right default.

Landscape rotation is done by the P4's PPA (hardware), not software. If the
picture is upside down, use **Settings → Flip 180°**. If touches land in the
wrong place, adjust `SDS_TOUCH_SWAP_XY / MIRROR_X / MIRROR_Y` (these describe
the touch controller in the panel's native portrait orientation; LVGL then
applies the display rotation).

## First start

1. With no saved Wi-Fi the **Wi-Fi setup** screen opens: pick a network from
   the scan list (or type an SSID for a hidden network), enter the password on
   the on-screen keyboard and tap **Connect**. Credentials are stored in NVS.
   Optional defaults can be compiled in via `CONFIG_SDS_DEFAULT_WIFI_SSID` /
   `CONFIG_SDS_DEFAULT_WIFI_PASSWORD`.
2. Once connected, if no scanner IP is known the panel opens **Settings** and
   runs discovery automatically: it sends `MDL` to every host in the local /24
   and saves the first `MDL,SDS…` responder. You can also type the IP and tap
   **Save**. A fixed DHCP lease for the scanner is recommended.
3. Wi-Fi reconnects automatically with back-off (1 s → 30 s).

## Tasks

| Task | Core | Role |
|---|---|---|
| `scanner` | 0 | Owns the UDP socket; serialises every exchange (1.2 s timeout); polls GSI/STS/GWF; runs queued commands |
| `rtsp` | 0 | RTSP session, RTP receive, keepalive, TEARDOWN |
| `playback` | 0 | Jitter buffer → ES8311 |
| LVGL (esp_lvgl_port) | 1 | UI; pulls state snapshots every 100 ms |
| `httpd` | – | HTTP API |

## Limitations and known gaps

- **Not yet tested on hardware.** The firmware compiles cleanly with ESP-IDF
  v5.4.2, but display bring-up, touch orientation, ES8311 output and the
  ESP-Hosted link have not been verified on a real JC8012P4A1.
- The ESP32-C6 must run a compatible **ESP-Hosted slave** firmware. The host
  side here is esp_hosted 2.12.x; boards shipped with an older slave may need
  the C6 re-flashed (see the esp_hosted documentation). The C6 wake-host line
  (GPIO6) is not used – host power saving is disabled.
- A reboot by power loss or the RST button cannot send `TEARDOWN`; if the
  scanner then refuses RTSP, power-cycle the scanner.
- When Wi-Fi drops, `TEARDOWN` is attempted but usually cannot reach the
  scanner; the same power-cycle rule applies if audio will not restart.
- The screen mirror draws printable ASCII only; scanner icon glyphs are shown
  as spaces. Characters are placed on a fixed 30-column grid using Montserrat,
  so it is not pixel-identical to the scanner's own font.
- Waterfall values are drawn with automatic contrast; they are not calibrated
  in dB.
- CPU runs at 360 MHz (the safe default for all ESP32-P4 silicon revisions
  in IDF 5.4); PSRAM at 200 MHz relies on `CONFIG_IDF_EXPERIMENTAL_FEATURES`.
- The HTTP API has no authentication or TLS.
- The GSL3680 touch driver (`mangoo1/esp_lcd_touch_gsl3680`, derived from the
  Guition vendor SDK) is licensed **GPL-2.0-or-later**, which applies to the
  combined firmware image.
