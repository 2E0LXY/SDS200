# SDS200 WebApp

Standalone Windows web remote for Uniden SDS200 / SDS200E scanners.

This source tree was reconstructed from the v0.6.2 embedded web assets and the validated scanner protocol behavior from the project test history. It is not a byte-for-byte recovery of the earlier lost Go source.

## Design rules

- Real scanner only: no simulation, demo telemetry, synthetic waterfall or fallback audio.
- UDP scanner control uses port 50536.
- Network audio uses a single RTSP session on TCP 554 at `/au:scanner.au`, with RTP/PCMU audio.
- There are no background or bare TCP 554 reachability probes. Only **Listen Live** may create an RTSP session. Remote WebApp recording requires an already-running live audio session and shares it.
- Scanner display confirmation uses fresh STS readback after control-key presses.
- Firmware/database/Profile management remains a Sentinel/USB task and is not represented as a LAN control.

## Logging

Logs are written to:

`%LOCALAPPDATA%\SDS200-WebApp\logs\sds200-YYYY-MM-DD.log`

Levels: `ERROR`, `WARN`, `INFO`, `DEBUG`, `TRACE`.

Structured components include `APP`, `HTTP`, `UDP`, `CONTROL`, `STS`, `DISCOVERY`, `LEASE`, `AUDIO`, `RTSP`, `RTP`, `WATERFALL`, `RECORD`, and browser-side events. The System page can change the log level, filter/search the in-memory log, download the current log, open the log directory, and copy a diagnostic bundle.

`TRACE` includes raw protocol traffic; use it only while diagnosing a problem because it is intentionally verbose.

## Build

Requires Go 1.23+ and Node.js for the JavaScript syntax check.

```bash
go test ./...
go vet ./...
node --check static/app.js
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o SDS200-WebApp.exe .
```

## GitHub Actions

`.github/workflows/build.yml` runs tests/vet/JavaScript checking and cross-compiles the Windows amd64 executable. It uploads a Windows build artifact for each push/PR/manual run.

## Current version

`0.7.1-native`

## v0.7.1 parser fix

`GSI,<XML>,` can arrive as a standalone UDP datagram before the `ScannerInfo` XML document. v0.7.1 keeps waiting for the XML instead of treating that marker as a complete response. This fixes repeated `ScannerInfo XML not found` warnings seen on the physical SDS200E while preserving ordinary non-XML command handling.
