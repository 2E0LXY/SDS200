# SDS200 Remote (Android)

A native Android client (Kotlin, Jetpack Compose, Material 3, dark theme) that
controls a Uniden SDS200E directly over the local network. It talks to the
scanner itself: no PC, web app or ESP32 panel is needed, and there is no
WebView. It uses the same protocol as the Go web app and the ESP32 panel in this
repository.

- Control: UDP port 50536 (SDS Remote Command Specification V2.00).
- Audio: RTSP on TCP port 554 (`rtsp://<scanner>/au:scanner.au`), then RTP
  G.711 µ-law at 8 kHz mono.

Requires Android 8.0 (API 26) or later.

## Installing (sideload)

1. Download `SDS200-Remote-<version>.apk` from the GitHub release, or from the
   **android** workflow run's artefacts.
2. On the phone, allow *Install unknown apps* for your browser or file manager.
3. Open the APK and install it. If Android says the package conflicts with an
   existing one, uninstall the old build first. This happens when two builds
   were signed with different keys (see *Signing* below).

A debug build (`SDS200-Remote-<version>-debug.apk`, package
`uk.co.twoe0lxy.sds200.debug`) can be installed alongside the release build.

## Permissions

| Permission | Why |
|---|---|
| `INTERNET` | UDP control and RTSP/RTP audio to the scanner |
| `ACCESS_NETWORK_STATE`, `ACCESS_WIFI_STATE` | Find the Wi-Fi subnet for Discover; stop audio if the network drops |
| `FOREGROUND_SERVICE`, `FOREGROUND_SERVICE_MEDIA_PLAYBACK` | Keep live audio playing with the screen off |
| `POST_NOTIFICATIONS` (Android 13+) | Show the playback notification with its Stop button. You are asked the first time you press Listen |
| `WAKE_LOCK` | Hold a partial wake lock and a low-latency Wi-Fi lock while audio is streaming |

The app has no location, storage or account permissions. It sends nothing
anywhere except to the scanner address you configure.

## Using the app

- **Settings**: enter the scanner's IP address, or press **Discover**. Discover
  sends `MDL` to every host on the phone's Wi-Fi /24 at once and lists the ones
  that reply `SDS200`. This screen also shows the connection status, the
  command round-trip latency, the model and firmware, and the scanner clock
  with **Sync to phone** (`DTM`). It also has a *Keep screen on* option and
  shows the app version.
- **Live**: shows the mode, system › department › site, and the channel. In
  search and Close Call modes it shows the frequency instead of a channel name.
  It also shows the frequency, TGID, UID, modulation or P25 status, service
  type, RSSI and signal bars. The four hold chips are System, Dept, Site and
  Channel. Each chip sends the scanner's own key sequence (`A`, `B`, `F`+`B` or
  `C`), then confirms the change by reading `GSI` again. The screen also has
  **Avoid**, **Previous** and **Next**. Previous and Next send `NXT`/`PRV` with
  the target keyword that matches the channel type; where no keyword applies
  they fall back to turning the rotary knob. **Listen** starts live audio and
  shows a packet counter. `GSI` is polled about twice a second, but only while
  the screen is visible.
- **Remote**: a mirror of the scanner display built from `STS`. It uses large
  and small fonts, reverse video and underline, and replaces icon glyphs with
  spaces. Below it are the full keypad, MENU, FUNC, AVOID, REPLAY, SERVICE,
  RANGE and ZIP, the rotary left, right and push, and the three soft keys,
  which take their labels from the bottom display line. The volume (0–29) and
  squelch (0–19) sliders write their value after a short pause and then read it
  back from the scanner.
- **Lists**: three tabs.
  - **Quick keys** is a grid for Favourites Quick Keys 00–99 (`FQK`). Tapping a
    key writes the change and reads the grid back.
  - **Service types** switches service types on and off (`SVC`, 37 preset and
    10 custom).
  - **Memory** browses the scanner's memory: Favourites list → system →
    department or site → channels (`GLT`).
- **Waterfall**: a live spectrum line and a scrolling waterfall of the
  scanner's 240 FFT bins (`GWF`, about 4 Hz). **Start** switches the scanner to
  waterfall mode only if it was not already in it. Leaving the screen, or
  sending the app to the background, stops the waterfall. The scanner goes back
  to scan mode only if this app switched it to waterfall.

### Live audio

The SDS200 allows **one** RTSP audio session at a time. The app always sends
RTSP `TEARDOWN` when:

- you press Stop, in the app or in the notification;
- the network is lost;
- another app takes audio focus permanently;
- the service is destroyed.

The app keeps the session alive with RTSP `GET_PARAMETER` and a 4-byte UDP
"punch" every 15 seconds. Audio passes through a jitter buffer of about 200 ms
and plays through `AudioTrack` as media audio.

If audio starts ("Waiting for audio…") but no packets arrive, check two things:

- The phone and the scanner must be on the same subnet.
- The Wi-Fi network must not use client or AP isolation. Guest networks often
  do.

## Building

```sh
cd android
./gradlew testDebugUnitTest assembleDebug assembleRelease lint
```

You need JDK 21 and an Android SDK with platform 35 and build-tools 35.0.0. Set
`ANDROID_HOME` or add a `local.properties` file with `sdk.dir=...`. The APKs
are written to `app/build/outputs/apk/{debug,release}/`.

- `versionName` comes from the `APP_VERSION` environment variable (a leading
  `v` is removed). The default is `0.8.0-dev`.
- `versionCode` comes from `GITHUB_RUN_NUMBER`. The default is 1.
- The release build is minified and resource-shrunk with R8 (see
  `app/proguard-rules.pro`).

### Signing

A release build is signed with a real keystore when all of these environment
variables (GitHub secrets in CI) are set:

| Secret | Value |
|---|---|
| `SDS200_KEYSTORE_B64` | The keystore file, base64-encoded (`base64 -w0 release.jks`) |
| `SDS200_KEYSTORE_PASSWORD` | Store password |
| `SDS200_KEY_ALIAS` | Key alias |
| `SDS200_KEY_PASSWORD` | Key password (the store password is used if this is empty) |

To create a keystore:

```sh
keytool -genkeypair -v -keystore release.jks -alias sds200 -keyalg RSA -keysize 4096 -validity 10000
```

Without these variables the release APK is signed with the local or CI debug
key, so CI always produces an installable APK. However, each CI runner has its
own debug key, so builds signed this way cannot be installed as updates over
each other. Never commit a keystore: `*.jks` and `*.keystore` are git-ignored.

### CI

`.github/workflows/android.yml` runs on:

- pushes that touch `android/**`;
- pull requests;
- manual dispatch;
- `v*` tags.

It runs the unit tests, lint and the release build, and uploads the APK and its
SHA-256 checksum as an artefact. On a `v*` tag it also attaches both files to
the GitHub release.

## Tests

The JVM unit tests (`app/src/test`) cover:

- the STS parser, using real SDS200E samples from the Go tests;
- the GSI and GLT XML parsers, including UTF-8 names, truncated XML and garbage;
- UDP reply reassembly: a separate `GSI,<XML>,` marker, XML split across
  datagrams, and numbered GLT fragments;
- µ-law decoding, RTP header parsing, and SDP and `Transport` parsing;
- the jitter buffer;
- a loopback fake-scanner UDP test of the command client, including the retry
  when the display is blank after a key press;
- a loopback fake RTSP server test that checks the exact
  OPTIONS → DESCRIBE → SETUP → PLAY → GET_PARAMETER → TEARDOWN sequence.

## Limitations

- Only tested by the automated tests above. The app has **not** yet been run
  against a real SDS200 or on a physical phone. The protocol handling mirrors
  the Go app, which has been checked on SDS200E firmware 1.23.15.
- IPv4 and a /24 subnet only for Discover. You can enter any IPv4 address by
  hand.
- The waterfall levels are uncalibrated scanner FFT units and are auto-scaled.
  The centre, lower and upper frequencies come from `GST`. Bare numbers are
  shown on the assumption that they are in units of 100 Hz; this format has not
  been checked on hardware.
- No recording, analysis screens, location/range or menu editing. These
  features are in the Go web app only.
- There is no authentication. Anyone on the same network can control the
  scanner, just as with the scanner's own network interface.
- Live audio sometimes stops for a moment; in the app this shows as a
  "no packets" error. This happens if Android puts Wi-Fi into power saving
  despite the Wi-Fi lock, which some phone makers' battery managers do. Exempt
  the app from battery optimisation if this happens.
