# SDS200 WebApp

Browser remote control for Uniden SDS200 / SDS200E scanners over the scanner's LAN port. One static Go binary; no runtime dependencies.

| Platform | Package |
|---|---|
| Windows x64 | `SDS200-WebApp-<ver>-windows-amd64.zip` — run `SDS200-WebApp.exe`, browser opens `http://127.0.0.1:8765` |
| Debian / Ubuntu / Raspberry Pi OS | `sds200-webapp_<ver>_{amd64,arm64,armhf}.deb` — installs a hardened systemd service on port 8765 |
| Guition JC8012P4A1 (ESP32-P4 10.1" panel) | see `firmware/jc8012p4a1/` |

## Protocol (verified on SDS200E firmware 1.23.15)

| Channel | Detail |
|---|---|
| Control | UDP 50536, CR-terminated commands per *SDS Series Remote Command Specification V2.00* (MDL, VER, GSI, STS, GST, KEY, VOL, SQL, FQK, SVC, DTM, GLT, HLD, NXT/PRV, JPM, GWF/PWF, AST/APR, URC, MSI) |
| Audio | RTSP/1.0 TCP 554 `rtsp://<ip>/au:scanner.au` → RTP PCMU (G.711 µ-law) 8 kHz mono, 320-byte/40 ms packets; GET_PARAMETER keepalive every 15 s |
| Not supported | `KAL` returns `ERR` on 1.23.15 |

### Audio rules
- The scanner allows **one** RTSP session. The app always sends TEARDOWN on stop, on shutdown (Ctrl+C / SIGTERM / service stop) and when the last listener leaves. A leaked session makes TCP 554 refuse connections until the scanner is power-cycled.
- No bare TCP 554 reachability probes; only **Listen Live** opens RTSP. Remote recording shares the live session.
- After SETUP the app sends a 4-byte datagram from its RTP port to the scanner's RTP source port. This opens the host firewall's stateful UDP path, so no inbound firewall rule is needed on Windows or Linux.

## Usage

```
SDS200-WebApp [-scanner 192.168.1.211] [-listen 0.0.0.0:8765] [-data-dir DIR] [-no-browser] [-version]
```

Data (config, logs, WAV recordings): Windows `%LOCALAPPDATA%\SDS200-WebApp`, Linux service `/var/lib/sds200-webapp`, otherwise `-data-dir` / `SDS200_DATA_DIR`.

Linux service options: `/etc/default/sds200-webapp` (`SDS200_ARGS`). To front it with Caddy, bind to loopback (`-listen 127.0.0.1:8765`), set `control_token` in `config.json`, and use `/usr/share/doc/sds200-webapp/Caddyfile.example`.

## Design rules
- Real scanner only: no simulation, demo telemetry, synthetic waterfall or fallback audio.
- Key presses are confirmed by a fresh STS read (retried while the scanner redraws).
- Shutdown only restores scan mode if the app itself started the waterfall.
- Firmware/database/profile management stays a Sentinel/USB task.

## Logging
`<data-dir>/logs/sds200-YYYY-MM-DD.log`, levels ERROR/WARN/INFO/DEBUG/TRACE (TRACE includes raw protocol traffic). The System page can change level, filter, download, and copy a diagnostic bundle.

## Build

```bash
go test ./... && go vet ./...
go run github.com/evanw/esbuild/cmd/esbuild@v0.25.10 static/app.js --log-level=warning >/dev/null   # JS syntax check, no Node
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o SDS200-WebApp.exe .
scripts/build-deb.sh 0.8.0 arm64 dist
```

GitHub Actions (`.github/workflows/build.yml`) tests, builds the Windows zip and three .debs on every push, and publishes a GitHub Release on `v*` tags.
