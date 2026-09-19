package main

import (
	"net"
	"strings"
	"testing"
	"time"
)

func makeSTS(form string, texts []string, modes []string) string {
	parts := []string{"STS", form}
	for i := range form {
		text := ""
		mode := ""
		if i < len(texts) {
			text = texts[i]
		}
		if i < len(modes) {
			mode = modes[i]
		}
		parts = append(parts, text, mode)
	}
	return strings.Join(parts, ",")
}

func TestParseSTSRealScanningSample(t *testing.T) {
	form := "00001010100000000"
	texts := []string{
		"              Sep19 17:40",
		"F0:- -------- XPT",
		"S0:----------   VOL: 0 SQL: 2",
		"D1:012 456789   Tag:01.--.---",
		"LEEDS",
		"Local",
		"Huddersfield",
		"",
		"Cummins Turbo Technologies L\x06\x07",
		" 453.112500MHz",
		"Custom 1",
		"Sys ID: ---     TGID: ---",
		"RFSS ID: ---    Site ID: ---",
		"WACN: ---       Batt:-.--V",
		"UID: ---        RSSI:-101dBm",
		"N\x14\x15",
		" SYSTEM      DEPT     CHANNEL",
	}
	d := parseSTS(makeSTS(form, texts, nil), 31.2)
	if d.ParseError != "" {
		t.Fatalf("parse error: %s", d.ParseError)
	}
	if len(d.Lines) != 17 {
		t.Fatalf("got %d display lines", len(d.Lines))
	}
	for _, idx := range []int{4, 6, 8} {
		if d.Lines[idx].Font != "large" {
			t.Fatalf("line %d should be large, got %q", idx, d.Lines[idx].Font)
		}
	}
	if got := d.Lines[8].Text; got != "Cummins Turbo Technologies L" {
		t.Fatalf("scanner font control bytes were not stripped: %q", got)
	}
	if got := d.Lines[15].Text; got != "N" {
		t.Fatalf("scanner glyph bytes were not stripped: %q", got)
	}
	if got := d.Lines[16].Text; got != " SYSTEM      DEPT     CHANNEL" {
		t.Fatalf("soft key spacing/labels changed: %q", got)
	}
}

func TestParseSTSRealWaterfallSample(t *testing.T) {
	form := "00000000000000000000"
	texts := make([]string, len(form))
	texts[0] = "              Sep19 17:29"
	texts[2] = "                     8.33k"
	texts[3] = "                 MHz AM"
	texts[17] = "SPAN:2.88MHz    GAIN:Auto"
	texts[18] = "CF:453.7250MHz  VOL: 0 SQL: 0"
	texts[19] = " to Scan     SPAN      HOLD"
	d := parseSTS(makeSTS(form, texts, nil), 34.6)
	if d.ParseError != "" {
		t.Fatalf("parse error: %s", d.ParseError)
	}
	if d.Lines[17].Text != "SPAN:2.88MHz    GAIN:Auto" {
		t.Fatalf("span line mismatch: %q", d.Lines[17].Text)
	}
	if d.Lines[18].Text != "CF:453.7250MHz  VOL: 0 SQL: 0" {
		t.Fatalf("center line mismatch: %q", d.Lines[18].Text)
	}
	if d.Lines[19].Text != " to Scan     SPAN      HOLD" {
		t.Fatalf("soft key line mismatch: %q", d.Lines[19].Text)
	}
}

func TestKeyMapIncludesFrequencyAndServiceControls(t *testing.T) {
	want := map[string]string{".": "No / Decimal", "T": "Service Type", "R": "Range"}
	for key, label := range want {
		if got := keyCodes[key]; got != label {
			t.Fatalf("key %q: got %q want %q", key, got, label)
		}
	}
}

func TestAudioInitialStatusDoesNotProbe(t *testing.T) {
	h := &AudioHub{app: &App{cfg: Config{RTSPPort: 554, RTSPPath: "/au:scanner.au"}}, owners: map[string]bool{}, subs: map[chan []byte]struct{}{}}
	d := h.Status()
	if d["state"] != "NOT STARTED" {
		t.Fatalf("initial audio state = %v", d["state"])
	}
	if d["reachable"] != false {
		t.Fatalf("idle status must not claim RTSP reachability: %v", d["reachable"])
	}
}

func TestParseWaterfallAccepts240HexValues(t *testing.T) {
	vals := make([]string, 240)
	for i := range vals {
		vals[i] = "0a"
	}
	d := parseWaterfallLine("GWF," + strings.Join(vals, ",") + ",")
	if d == nil {
		t.Fatal("GWF frame rejected")
	}
	if got := d["count"]; got != 240 {
		t.Fatalf("count = %v", got)
	}
	if d["calibrated"] != false {
		t.Fatal("waterfall must remain explicitly uncalibrated")
	}
}

func TestUDPCommandWaitsForSeparatedGSIXML(t *testing.T) {
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 1024)
		_ = server.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, client, err := server.ReadFromUDP(buf)
		if err != nil {
			done <- err
			return
		}
		if got := string(buf[:n]); got != "GSI\r" {
			done <- &testError{"unexpected request: " + got}
			return
		}
		if _, err = server.WriteToUDP([]byte("GSI,<XML>,\r"), client); err != nil {
			done <- err
			return
		}
		time.Sleep(15 * time.Millisecond)
		xml := `<ScannerInfo Mode="Scan" V_Screen="Main"><System Name="LEEDS"/></ScannerInfo>`
		_, err = server.WriteToUDP([]byte(xml), client)
		done <- err
	}()

	addr := server.LocalAddr().(*net.UDPAddr)
	r, err := udpCommand("127.0.0.1", addr.Port, "GSI", 2*time.Second)
	if err != nil {
		t.Fatalf("udpCommand failed: %v", err)
	}
	if !strings.Contains(r.Raw, "<ScannerInfo") {
		t.Fatalf("response returned before XML arrived: %q", r.Raw)
	}
	info := parseScannerInfo(r.Raw)
	if info.ParseError != "" {
		t.Fatalf("ScannerInfo parse error: %s; raw=%q", info.ParseError, r.Raw)
	}
	if info.System != "LEEDS" {
		t.Fatalf("system = %q, want LEEDS", info.System)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type testError struct{ s string }

func (e *testError) Error() string { return e.s }
