package main

// Scanner operations shared by the HTTP API and the assistant.
//
// Anything that both a button and an assistant tool can do lives here once, so
// the two cannot drift apart: the handler and the tool each validate their own
// input shape and then call the same method.

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// scannerModes maps the names used by the UI and the assistant to JPM targets.
var scannerModes = map[string]string{
	"scan":                   "SCN_MODE",
	"close_call":             "CC_MODE",
	"weather":                "WX_MODE",
	"tone_out":               "FTO_MODE",
	"custom_search":          "CTM_MODE",
	"quick_search":           "QSH_MODE",
	"waterfall":              "WF_MODE",
	"conventional_discovery": "CDIS_MODE",
	"trunking_discovery":     "TDIS_MODE",
}

func scannerModeNames() []string {
	return []string{"scan", "close_call", "weather", "tone_out", "custom_search", "quick_search", "waterfall", "conventional_discovery", "trunking_discovery"}
}

// setMode jumps the scanner to an operating mode (JPM). index is the optional
// extra field some modes take: a weather sub-mode, or a discovery session name.
func (a *App) setMode(mode, index string) (map[string]any, error) {
	target := scannerModes[strings.ToLower(strings.TrimSpace(mode))]
	if target == "" {
		return nil, &httpError{422, "unknown mode; use one of " + strings.Join(scannerModeNames(), ", ")}
	}
	index = strings.TrimSpace(index)
	if strings.ContainsAny(index, ",\r\n") {
		return nil, &httpError{422, "the mode index cannot contain a comma or a line break"}
	}
	a.log.Info("CONTROL", "Mode change requested", map[string]any{"mode": target, "index": index})
	if _, err := a.execWrite("JPM,"+target+","+index, 2500*time.Millisecond); err != nil {
		return nil, err
	}
	a.invalidateState()
	time.Sleep(600 * time.Millisecond)
	return a.readState(true), nil
}

// qshUnits converts MHz to the unit QSH takes, which is 100 Hz: 406.0000 MHz
// is sent as 4060000. Channels on a 6.25 kHz raster do not land on a 100 Hz
// boundary (446.00625 MHz), so the value is rounded to the nearest step.
func qshUnits(mhz float64) (int, error) {
	if math.IsNaN(mhz) || mhz < 25 || mhz > 1300 {
		return 0, &httpError{422, "frequency must be between 25 and 1300 MHz"}
	}
	return int(math.Round(mhz * 1e4)), nil
}

// tuneFrequency puts the scanner into quick search hold on one frequency.
func (a *App) tuneFrequency(mhz float64) (map[string]any, error) {
	units, err := qshUnits(mhz)
	if err != nil {
		return nil, err
	}
	a.log.Info("CONTROL", "Tune requested", map[string]any{"mhz": mhz, "qsh": units})
	if _, err := a.execWrite(fmt.Sprintf("QSH,%d", units), 2500*time.Millisecond); err != nil {
		return nil, err
	}
	a.invalidateState()
	time.Sleep(500 * time.Millisecond)
	return a.readState(true), nil
}

// ---------------------------------------------------------------- quick keys
//
// Three levels share one reply shape: a run of 100 states, 0 = no key,
// 1 = disabled, 2 = enabled.
//
//	FQK                      favourites lists
//	SQK,<fav>                systems inside favourites quick key <fav>
//	DQK,<fav>,<sys>          departments inside system quick key <sys>
//
// The SQK reply carries one extra field before the states that the spec does
// not explain, so the states are always taken as the last 100 fields.

type quickKeySet struct {
	Kind    string `json:"kind"` // fqk, sqk or dqk
	Fav     int    `json:"fav,omitempty"`
	Sys     int    `json:"sys,omitempty"`
	States  []int  `json:"states"`
	Raw     string `json:"raw"`
	Elapsed any    `json:"elapsed_ms"`
}

func quickKeyWire(kind string, fav, sys int) (cmd, wire string, err error) {
	inRange := func(n int) bool { return n >= 0 && n <= 99 }
	switch strings.ToLower(kind) {
	case "", "fqk":
		return "FQK", "FQK", nil
	case "sqk":
		if !inRange(fav) {
			return "", "", &httpError{422, "fav must be a favourites quick key, 0-99"}
		}
		return "SQK", fmt.Sprintf("SQK,%d", fav), nil
	case "dqk":
		if !inRange(fav) || !inRange(sys) {
			return "", "", &httpError{422, "fav and sys must be quick keys, 0-99"}
		}
		return "DQK", fmt.Sprintf("DQK,%d,%d", fav, sys), nil
	}
	return "", "", &httpError{422, "kind must be fqk, sqk or dqk"}
}

// parseQuickKeyStates takes the last 100 fields of a reply as states.
func parseQuickKeyStates(fields []string) []int {
	if len(fields) > 100 {
		fields = fields[len(fields)-100:]
	}
	states := make([]int, 100)
	for i, s := range fields {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n >= 0 && n <= 2 {
			states[i] = n
		}
	}
	return states
}

// quickKeys reads one level of quick keys.
func (a *App) quickKeys(kind string, fav, sys int) (*quickKeySet, error) {
	cmd, wire, err := quickKeyWire(kind, fav, sys)
	if err != nil {
		return nil, err
	}
	res, err := a.command(wire, 1500*time.Millisecond)
	if err != nil {
		return nil, err
	}
	fields := packetFields(res.Raw, cmd)
	if len(fields) < 100 {
		return nil, &httpError{502, fmt.Sprintf("the scanner answered %s with %d fields, not the 100 states expected", cmd, len(fields))}
	}
	return &quickKeySet{Kind: strings.ToLower(cmd), Fav: fav, Sys: sys, States: parseQuickKeyStates(fields), Raw: res.Raw, Elapsed: res.ElapsedMS}, nil
}

// writeFavouriteQuickKeys sets all 100 favourites quick keys at once.
func (a *App) writeFavouriteQuickKeys(states []int) (map[string]any, error) {
	if len(states) != 100 {
		return nil, &httpError{422, "FQK requires exactly 100 states"}
	}
	for _, v := range states {
		if v < 0 || v > 2 {
			return nil, &httpError{422, "FQK states must be 0/1/2"}
		}
	}
	a.log.Info("CONTROL", "Favorites Quick Keys write", map[string]any{"count": 100})
	return a.execWrite("FQK,"+joinInts(states), 2*time.Second)
}

// setFavouriteQuickKey switches one favourites quick key, leaving the rest as
// the scanner reports them. A key with no list assigned cannot be switched.
func (a *App) setFavouriteQuickKey(key int, enabled bool) error {
	if key < 0 || key > 99 {
		return &httpError{422, "quick key must be 0-99"}
	}
	set, err := a.quickKeys("fqk", 0, 0)
	if err != nil {
		return err
	}
	if set.States[key] == 0 {
		return &httpError{409, fmt.Sprintf("quick key %02d is not assigned to any favourites list", key)}
	}
	set.States[key] = 1
	if enabled {
		set.States[key] = 2
	}
	_, err = a.writeFavouriteQuickKeys(set.States)
	return err
}

// ------------------------------------------------------------- HTTP surface

func (a *App) apiMode(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		writeJSON(w, 200, map[string]any{"modes": scannerModeNames()})
		return
	}
	if !requireMethod(w, r, "POST") {
		return
	}
	if err := a.requireControl(r); err != nil {
		errJSON(w, err)
		return
	}
	var b struct {
		Mode  string `json:"mode"`
		Index string `json:"index"`
	}
	if err := readJSON(r, &b); err != nil {
		errJSON(w, err)
		return
	}
	d, err := a.setMode(b.Mode, b.Index)
	if err != nil {
		errJSON(w, err)
		return
	}
	writeJSON(w, 200, d)
}

func (a *App) apiTune(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "POST") {
		return
	}
	if err := a.requireControl(r); err != nil {
		errJSON(w, err)
		return
	}
	var b struct {
		MHz float64 `json:"mhz"`
	}
	if err := readJSON(r, &b); err != nil {
		errJSON(w, err)
		return
	}
	d, err := a.tuneFrequency(b.MHz)
	if err != nil {
		errJSON(w, err)
		return
	}
	writeJSON(w, 200, d)
}

// apiQuickKeys reads system or department quick keys. Favourites quick keys
// keep their own endpoint (/api/fqk), which also writes; the lower levels are
// read-only here until their write form has been checked against a radio.
func (a *App) apiQuickKeys(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "GET") {
		return
	}
	q := r.URL.Query()
	fav, _ := strconv.Atoi(q.Get("fav"))
	sys, _ := strconv.Atoi(q.Get("sys"))
	set, err := a.quickKeys(q.Get("kind"), fav, sys)
	if err != nil {
		errJSON(w, err)
		return
	}
	writeJSON(w, 200, set)
}
