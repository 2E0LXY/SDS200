SDS200 WebApp v0.7.1-native - Windows test notes

1. Close old copies of SDS200-WebApp and old browser tabs.
2. If the SDS200E RTSP service has previously become unavailable, fully power-cycle the scanner. Do NOT use Test-NetConnection or any bare TCP 554 probe before the audio test.
3. Run SDS200-WebApp-v0.7.1.exe. The browser opens http://127.0.0.1:8765.
4. Verify the scanner is selected at its real IP and UDP control is ONLINE.
5. Control page:
   - Confirm the keypad has a '. / NO' key.
   - Enter a test frequency using the real keypad controls and observe Scanner Entry / Confirmation after every key press.
   - Press SERVICE TYPES and RANGE separately. The persistent log will record the exact KEY command, acknowledgement and STS confirmation.
6. Audio:
   - Idle status must read NOT STARTED. Merely opening Dashboard/System/Audio must not contact TCP 554.
   - Press Listen Live once. The log should show RTSP TCP connect, OPTIONS, DESCRIBE, SETUP, PLAY, then first RTP packet.
   - Browser WAV playback is not attached until a real RTP packet has arrived.
   - Stop should log TEARDOWN before the socket closes.
   - Remote WebApp Recording requires Listen Live to already be receiving RTP; it shares that session and does not open another RTSP connection.
7. Logging:
   - System -> Persistent Diagnostic Log.
   - INFO is the normal level. DEBUG adds HTTP/UDP details. TRACE includes raw protocol traffic.
   - Download Log downloads the current persistent file.
   - Open Log Folder opens %LOCALAPPDATA%\SDS200-WebApp\logs.
   - Copy Diagnostics copies app/scanner/audio/waterfall/current STS + recent log entries.
8. Waterfall:
   - No graph should appear unless real 240-value GWF frames arrive.
   - Scanner STS labels/Span/Center must come from the real radio.

No simulation/fallback/demo scanner data is used.
