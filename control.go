package main

// Scanner operations shared by the HTTP API and the assistant.
//
// Anything that both a button and an assistant tool can do lives here once, so
// the two cannot drift apart: the handler and the tool each validate their own
// input shape and then call the same method.

import (
	"context"
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
// is sent as 4060000 (confirmed on an SDS200E: the radio tuned what was sent,
// to within its band step). Channels on a 6.25 kHz raster do not land on a
// 100 Hz boundary (446.00625 MHz), so the value is rounded to the nearest.
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
	st := a.readState(true)
	st["requested_mhz"] = float64(units) / 1e4
	// The scanner does not tune QSH's frequency exactly: quick search snaps to
	// the step set for that band, so 446.00625 on a 12.5 kHz band lands on
	// 446.0125. Say so rather than let the operator think they are on channel.
	if got, ok := parseMHz(fmt.Sprint(st["frequency"])); ok {
		st["tuned_mhz"] = got
		if math.Abs(got-float64(units)/1e4) > 0.00011 {
			st["note"] = fmt.Sprintf("the scanner tuned %.5f MHz, not %.5f: quick search uses the step set for this band. To reach the exact frequency, set a finer step for the band under Settings > Band Defaults on the scanner, or program it as a channel", got, float64(units)/1e4)
		}
	}
	return st, nil
}

// parseMHz reads a frequency as the scanner reports it: "446.012500MHz", or a
// bare count of 100 Hz units.
func parseMHz(s string) (float64, bool) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(strings.ToUpper(s)), "MHZ"))
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return 0, false
	}
	if !strings.Contains(s, ".") && f >= 250000 {
		f /= 1e4
	}
	return f, true
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

// ---------------------------------------------------------- location & range
//
// LCR reads or writes the scanner's latitude, longitude and scan range. The
// scanner does not answer it in every state: straight after a mode change, or
// while a menu or direct entry is open, the reply can be missing fields or a
// bare NG. So a read is retried, and when it still fails the error carries the
// reply itself rather than the word "unexpected".

func parseLCR(raw string) (lat, lon, rng float64, ok bool) {
	f := packetFields(raw, "LCR")
	if len(f) < 3 {
		return 0, 0, 0, false
	}
	var e1, e2, e3 error
	lat, e1 = strconv.ParseFloat(strings.TrimSpace(f[0]), 64)
	lon, e2 = strconv.ParseFloat(strings.TrimSpace(f[1]), 64)
	rng, e3 = strconv.ParseFloat(strings.TrimSpace(f[2]), 64)
	return lat, lon, rng, e1 == nil && e2 == nil && e3 == nil
}

// lcrError explains a reply that could not be read as a location.
func lcrError(raw string) error {
	reply := strings.TrimSpace(raw)
	if reply == "" {
		reply = "(nothing)"
	}
	if len(reply) > 80 {
		reply = reply[:80] + "…"
	}
	up := strings.ToUpper(reply)
	if strings.Contains(up, "NG") || strings.Contains(up, "ERR") {
		return &httpError{409, fmt.Sprintf("the scanner will not report its location in its current state (it answered %q). Close any open menu, or return to scan mode, and try again", reply)}
	}
	return &httpError{502, fmt.Sprintf("the scanner's location reply could not be read (it answered %q)", reply)}
}

func (a *App) location() (lat, lon, rng float64, err error) {
	var raw string
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(350 * time.Millisecond)
		}
		res, cerr := a.command("LCR", 2500*time.Millisecond)
		if cerr != nil {
			err = cerr
			continue
		}
		raw, err = res.Raw, nil
		if la, lo, r, ok := parseLCR(res.Raw); ok {
			return la, lo, r, nil
		}
	}
	if err != nil {
		return 0, 0, 0, err
	}
	return 0, 0, 0, lcrError(raw)
}

// locationChange is what to alter; nil fields keep the scanner's value.
type locationChange struct {
	Latitude, Longitude, Range *float64
}

// setLocation writes the scanner's position and range. The current values are
// only read when something is being kept, so setting all three does not
// depend on the scanner being able to report them.
func (a *App) setLocation(c locationChange) (lat, lon, rng float64, err error) {
	if c.Latitude != nil && (*c.Latitude < -90 || *c.Latitude > 90) {
		return 0, 0, 0, &httpError{422, "latitude must be -90 to 90"}
	}
	if c.Longitude != nil && (*c.Longitude < -180 || *c.Longitude > 180) {
		return 0, 0, 0, &httpError{422, "longitude must be -180 to 180"}
	}
	if c.Range != nil && (*c.Range < 0 || *c.Range > 999) {
		return 0, 0, 0, &httpError{422, "range must be 0-999 miles"}
	}
	if c.Latitude == nil || c.Longitude == nil || c.Range == nil {
		if lat, lon, rng, err = a.location(); err != nil {
			return 0, 0, 0, err
		}
	}
	if c.Latitude != nil {
		lat = *c.Latitude
	}
	if c.Longitude != nil {
		lon = *c.Longitude
	}
	if c.Range != nil {
		rng = *c.Range
	}
	wire := fmt.Sprintf("LCR,%.6f,%.6f,%.1f", lat, lon, rng)
	a.log.Info("CONTROL", "Location/range write", map[string]any{"command": wire})
	if _, err = a.execWrite(wire, 2*time.Second); err != nil {
		return 0, 0, 0, err
	}
	a.invalidateState()
	return lat, lon, rng, nil
}

// setLocationFromAddress resolves a UK postcode (and optional house number)
// and writes it, with the range too if one is given.
func (a *App) setLocationFromAddress(ctx context.Context, house, postcode string, rangeMiles *float64) (*GeoResult, float64, error) {
	geo, err := newGeocoder(a.log).Lookup(ctx, house, postcode)
	if err != nil {
		return nil, 0, &httpError{422, err.Error()}
	}
	_, _, rng, err := a.setLocation(locationChange{Latitude: &geo.Latitude, Longitude: &geo.Longitude, Range: rangeMiles})
	if err != nil {
		return nil, 0, err
	}
	return geo, rng, nil
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
