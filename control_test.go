package main

import (
	"strings"
	"testing"
)

// QSH takes units of 100 Hz. Sending Hz, as the assistant's tune tool used to,
// asks the radio for a frequency 100 times too high.
func TestQSHUnitsAreHundredHertz(t *testing.T) {
	for _, c := range []struct {
		mhz  float64
		want int
	}{
		{406.0, 4060000},     // the spec's own example
		{453.725, 4537250},   // 12.5 kHz raster
		{446.00625, 4460063}, // PMR446 ch 1: 6.25 kHz raster, rounded to 100 Hz
		{121.5, 1215000},     // airband distress
		{25, 250000},
		{1300, 13000000},
	} {
		got, err := qshUnits(c.mhz)
		if err != nil || got != c.want {
			t.Errorf("qshUnits(%v) = %d, %v; want %d", c.mhz, got, err, c.want)
		}
	}
	for _, bad := range []float64{0, 24.999, 1300.001, -1} {
		if _, err := qshUnits(bad); err == nil {
			t.Errorf("qshUnits(%v) accepted an out-of-range frequency", bad)
		}
	}
}

func TestQuickKeyWire(t *testing.T) {
	for _, c := range []struct {
		kind     string
		fav, sys int
		wire     string
	}{
		{"fqk", 0, 0, "FQK"}, {"", 0, 0, "FQK"}, {"SQK", 7, 0, "SQK,7"}, {"dqk", 7, 12, "DQK,7,12"},
	} {
		_, wire, err := quickKeyWire(c.kind, c.fav, c.sys)
		if err != nil || wire != c.wire {
			t.Errorf("quickKeyWire(%q,%d,%d) = %q, %v; want %q", c.kind, c.fav, c.sys, wire, err, c.wire)
		}
	}
	for _, c := range []struct {
		kind     string
		fav, sys int
	}{{"sqk", 100, 0}, {"dqk", 1, -1}, {"xqk", 0, 0}} {
		if _, _, err := quickKeyWire(c.kind, c.fav, c.sys); err == nil {
			t.Errorf("quickKeyWire(%q,%d,%d) should be rejected", c.kind, c.fav, c.sys)
		}
	}
}

// The SQK and DQK replies carry their selector fields before the states, so
// the states are the last 100 fields whatever precedes them.
func TestParseQuickKeyStatesTakesTheLastHundred(t *testing.T) {
	states := make([]string, 100)
	for i := range states {
		states[i] = "0"
	}
	states[0], states[5], states[99] = "2", "1", "2"
	for _, prefix := range [][]string{nil, {"7", "3"}, {"7", "12"}} {
		got := parseQuickKeyStates(append(append([]string{}, prefix...), states...))
		if len(got) != 100 || got[0] != 2 || got[5] != 1 || got[99] != 2 || got[1] != 0 {
			t.Errorf("prefix %v: states misaligned: %v", prefix, got[:8])
		}
	}
	// Junk never becomes a state.
	if got := parseQuickKeyStates(strings.Split(strings.Repeat("x,", 99)+"9", ",")); got[0] != 0 || got[99] != 0 {
		t.Errorf("non-state values were accepted: %v", got)
	}
}

func TestSetModeRejectsUnknownAndUnsafeInput(t *testing.T) {
	a := &App{}
	if _, err := a.setMode("warp", ""); err == nil {
		t.Error("an unknown mode was accepted")
	}
	// A comma or line break in the index would inject a second field or command.
	for _, idx := range []string{"NORMAL,1", "x\rKEY,L,P"} {
		if _, err := a.setMode("weather", idx); err == nil {
			t.Errorf("index %q was accepted", idx)
		}
	}
}

func TestParseLCR(t *testing.T) {
	lat, lon, rng, ok := parseLCR("LCR,53.865900,-1.660600,10.0\r")
	if !ok || lat != 53.8659 || lon != -1.6606 || rng != 10 {
		t.Fatalf("parseLCR = %v %v %v %v", lat, lon, rng, ok)
	}
	for _, bad := range []string{"", "LCR,NG\r", "LCR,ERR\r", "LCR,53.8,-1.6\r", "LCR,a,b,c\r", "STS,1,2,3\r"} {
		if _, _, _, ok := parseLCR(bad); ok {
			t.Errorf("parseLCR(%q) was accepted", bad)
		}
	}
}

// A failed location read must say what the scanner answered; "unexpected LCR
// reply" left the operator and the assistant with nothing to act on.
func TestLCRErrorCarriesTheReply(t *testing.T) {
	if msg := lcrError("LCR,NG\r").Error(); !strings.Contains(msg, "LCR,NG") || !strings.Contains(msg, "scan mode") {
		t.Errorf("refusal not explained: %q", msg)
	}
	if msg := lcrError("").Error(); !strings.Contains(msg, "(nothing)") {
		t.Errorf("empty reply not reported: %q", msg)
	}
}

func TestSetLocationValidatesBeforeTouchingTheScanner(t *testing.T) {
	a := &App{}
	f := func(v float64) *float64 { return &v }
	for _, c := range []locationChange{{Latitude: f(91)}, {Longitude: f(-181)}, {Range: f(1000)}, {Range: f(-1)}} {
		if _, _, _, err := a.setLocation(c); err == nil {
			t.Errorf("setLocation(%+v) accepted an out-of-range value", c)
		}
	}
}

func TestParseMHz(t *testing.T) {
	for _, c := range []struct {
		in   string
		want float64
	}{{"446.012500MHz", 446.0125}, {"453.675000 MHz", 453.675}, {"4460125", 446.0125}, {"121.5", 121.5}} {
		if got, ok := parseMHz(c.in); !ok || got != c.want {
			t.Errorf("parseMHz(%q) = %v, %v", c.in, got, ok)
		}
	}
	for _, bad := range []string{"", "—", "<nil>", "0"} {
		if _, ok := parseMHz(bad); ok {
			t.Errorf("parseMHz(%q) was accepted", bad)
		}
	}
}
