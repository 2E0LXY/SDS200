package main

// Scanner operations shared by the HTTP API and the assistant.
//
// Anything that both a button and an assistant tool can do lives here once, so
// the two cannot drift apart: the handler and the tool each validate their own
// input shape and then call the same method.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
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
	if a.dirScan != nil && a.dirScan.Running() && target != "QSH_MODE" {
		// Called from DirScan.Stop itself once the loop has ended, so this
		// only fires for an operator's own mode change.
		a.dirScan.Stop(false)
	}
	a.prepareForWrite()
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

// Quick search hold, as GSI reports it.
var (
	gsiSrchFreq = regexp.MustCompile(`<SrchFrequency[^>]*\bFreq="([^"]*)"[^>]*\bMod="([^"]*)"`)
	gsiMute     = regexp.MustCompile(`<Property[^>]*\bMute="([^"]*)"`)
	gsiSig      = regexp.MustCompile(`<Property[^>]*\bSig="([^"]*)"`)
)

// searchHold is what the scanner says it is tuned to in quick search hold.
type searchHold struct {
	MHz  float64
	Mod  string // AM, NFM, FM, WFM or FMB
	Open bool   // squelch open: something is being received
}

func (a *App) readSearchHold() (searchHold, error) {
	res, err := a.command("GSI", 1500*time.Millisecond)
	if err != nil {
		return searchHold{}, err
	}
	var h searchHold
	m := gsiSrchFreq.FindStringSubmatch(res.Raw)
	if m == nil {
		return h, &httpError{409, "the scanner is not in quick search hold"}
	}
	h.MHz, _ = parseMHz(m[1])
	h.Mod = strings.ToUpper(strings.TrimSpace(m[2]))
	if h.Mod == "WFMB" { // GSI's name for what the menu calls FMB
		h.Mod = "FMB"
	}
	if mm := gsiMute.FindStringSubmatch(res.Raw); mm != nil && strings.EqualFold(mm[1], "Unmute") {
		h.Open = true
	}
	if sm := gsiSig.FindStringSubmatch(res.Raw); sm != nil {
		if n, err := strconv.Atoi(strings.TrimSpace(sm[1])); err == nil && n > 0 {
			h.Open = true
		}
	}
	return h, nil
}

// setSearchModulation makes quick search use one modulation. The only remote
// way to change it is the front panel's own: FUNC + soft key 3 steps through
// AM, NFM, FM, WFM, FMB and AUTO, with a popup of about three seconds after
// each press. So this is slow (up to twenty seconds), and is skipped when the
// scanner is already on the right one.
func (a *App) setSearchModulation(want string) error {
	want = normaliseMode(want)
	if want == "" {
		return nil
	}
	for press := 0; press < 7; press++ {
		h, err := a.readSearchHold()
		if err != nil {
			return err
		}
		if h.Mod == want {
			return nil
		}
		if err := a.funcSequence("C"); err != nil {
			return err
		}
		time.Sleep(350 * time.Millisecond)
	}
	return &httpError{502, "the scanner would not switch quick search to " + want}
}

// tuneFrequency puts the scanner into quick search hold on one frequency, in
// the given modulation if one is named.
func (a *App) tuneFrequency(mhz float64, mode string) (map[string]any, error) {
	units, err := qshUnits(mhz)
	if err != nil {
		return nil, err
	}
	if a.dirScan != nil {
		a.dirScan.Stop(false) // a manual tune takes over from a directory scan
	}
	a.prepareForWrite()
	a.log.Info("CONTROL", "Tune requested", map[string]any{"mhz": mhz, "qsh": units, "mode": mode})
	wire := fmt.Sprintf("QSH,%d", units)
	if _, err := a.execWrite(wire, 2500*time.Millisecond); err != nil {
		return nil, err
	}
	time.Sleep(400 * time.Millisecond)
	if normaliseMode(mode) != "" {
		before, _ := a.readSearchHold()
		if err := a.setSearchModulation(mode); err != nil {
			return nil, err
		}
		// A different modulation can mean a different step, so tune again.
		if after, _ := a.readSearchHold(); after.Mod != before.Mod {
			if _, err := a.execWrite(wire, 2500*time.Millisecond); err != nil {
				return nil, err
			}
			time.Sleep(400 * time.Millisecond)
		}
	}
	a.invalidateState()
	st := a.readState(true)
	want := float64(units) / 1e4
	st["requested_mhz"] = want
	if h, err := a.readSearchHold(); err == nil {
		st["tuned_mhz"], st["tuned_mode"] = h.MHz, h.Mod
		if note := tuneNote(want, normaliseMode(mode), h); note != "" {
			st["note"] = note
		}
	}
	return st, nil
}

// tuneNote says how the scanner fell short of what was asked, if it did. The
// scanner does not tune QSH's frequency exactly: quick search snaps to the
// step its Band Defaults give that band, and uses their modulation unless
// told otherwise. A radio whose band defaults are wrong for the UK - airband
// set to FMB in 100 kHz steps, as found on one SDS200E - cannot reach most
// airband channels this way until they are corrected.
func tuneNote(want float64, wantMode string, h searchHold) string {
	off := math.Abs(h.MHz-want) > 0.0011
	wrongMode := wantMode != "" && h.Mod != wantMode
	switch {
	case off:
		return fmt.Sprintf("the scanner tuned %.5f MHz, not %.5f: quick search uses the step its Band Defaults give this band. On the scanner, open Menu > Settings > Band Defaults and give this band the right step (8.33 kHz for civil airband, 25 kHz for military airband, 12.5 or 6.25 kHz for PMR), or program the frequency as a channel", h.MHz, want)
	case wrongMode:
		return fmt.Sprintf("the scanner is on %.5f MHz but in %s, not %s", h.MHz, h.Mod, wantMode)
	}
	return ""
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
		return nil, a.explainReply(cmd, res.Raw, "its quick keys")
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
	a.prepareForWrite()
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

// ------------------------------------------------- when the scanner says no
//
// The scanner answers a command it will not carry out with "<CMD>,ERR" or
// "<CMD>,NG". The commonest reason is that a menu is open on the radio: in
// menu mode it refuses almost everything except the menu commands. Nothing
// used to check for this, so a refused write was reported as done and a
// refused read surfaced as "returned 1 fields, expected 47".

// refusal is an error for a command the scanner declined. It unwraps to an
// httpError, so handlers report it like any other.
type refusal struct{ *httpError }

func (r refusal) Unwrap() error { return r.httpError }

func isRefusal(err error) bool {
	var r refusal
	return errors.As(err, &r)
}

// replyRefused reports whether raw is "<CMD>,ERR" or "<CMD>,NG".
func replyRefused(raw string) bool {
	line := strings.TrimSpace(raw)
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = line[:i]
	}
	parts := strings.Split(line, ",")
	if len(parts) != 2 {
		return false
	}
	v := strings.ToUpper(strings.TrimSpace(parts[1]))
	return v == "ERR" || v == "NG"
}

var (
	gsiMenuMode = regexp.MustCompile(`Mode="Menu tree"`)
	gsiMenuName = regexp.MustCompile(`<MenuSummary[^>]*\bname="([^"]*)"`)
)

// openMenu reports whether the scanner is in its menu, and which one.
func (a *App) openMenu() (name string, open bool) {
	res, err := a.command("GSI", 1500*time.Millisecond)
	if err != nil || !gsiMenuMode.MatchString(res.Raw) {
		return "", false
	}
	if m := gsiMenuName.FindStringSubmatch(res.Raw); m != nil {
		// The top level reports itself as "       --- M E N U ---".
		name = strings.Trim(m[1], " -")
		if strings.EqualFold(strings.ReplaceAll(name, " ", ""), "MENU") {
			name = ""
		}
	}
	return name, true
}

// refusalFor builds the error for a refused command, saying why if it can.
func (a *App) refusalFor(cmd, raw string) error {
	reply := strings.TrimSpace(raw)
	if name, open := a.openMenu(); open {
		what := "a menu"
		if name != "" {
			what = fmt.Sprintf("the %q menu", name)
		}
		return refusal{&httpError{409, fmt.Sprintf("the scanner has %s open, and while it is in a menu it refuses %s (it answered %q). Press Exit menu on the Control page, or ask the assistant to close the menu, then try again", what, cmd, reply)}}
	}
	return refusal{&httpError{409, fmt.Sprintf("the scanner refused %s in its current state (it answered %q). Returning it to scan mode usually clears this", cmd, reply)}}
}

// explainReply turns a reply that could not be read into an error that says
// what the scanner answered.
func (a *App) explainReply(cmd, raw, what string) error {
	if replyRefused(raw) {
		return a.refusalFor(cmd, raw)
	}
	reply := strings.TrimSpace(raw)
	if reply == "" {
		reply = "(nothing)"
	}
	if len(reply) > 80 {
		reply = reply[:80] + "…"
	}
	return &httpError{502, fmt.Sprintf("the scanner's reply to %s could not be read as %s (it answered %q)", cmd, what, reply)}
}

// leaveMenu takes the scanner out of its menu, back to what it was doing
// before. It reports the menu's name and whether one was open at all.
//
// The scanner often does not answer MSB when leaving the menu altogether, and
// then ignores everything for several seconds while it goes back to scanning
// (about six on an SDS200E leaving Manage Full Database). So the reply is not
// required; leaveMenu waits instead until the scanner answers again and is no
// longer in the menu.
func (a *App) leaveMenu() (name string, wasOpen bool, err error) {
	name, open := a.openMenu()
	if !open {
		return "", false, nil
	}
	a.log.Info("CONTROL", "Leaving the scanner menu", map[string]any{"menu": name})
	_, _ = a.command("MSB,,RETURN_PREVOUS_MODE", 1500*time.Millisecond)
	a.invalidateState()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(400 * time.Millisecond)
		res, cerr := a.command("GSI", 1200*time.Millisecond)
		if cerr == nil && !gsiMenuMode.MatchString(res.Raw) {
			a.invalidateState()
			return name, true, nil
		}
	}
	a.log.Warn("CONTROL", "The scanner did not leave its menu", map[string]any{"menu": name})
	return name, true, &httpError{504, "the scanner did not leave its menu within 12 seconds"}
}

// prepareForWrite gets the scanner ready to accept a setting. If a menu is
// open it is closed first, because the operator has asked for something the
// scanner will refuse while it is in one.
func (a *App) prepareForWrite() {
	_, _, _ = a.leaveMenu()
}

// apiMenuLeave closes the scanner's menu if one is open. The web UI calls it
// when the operator moves away from the pages that show the menu, so the
// radio is not left sitting in a menu nobody is looking at.
func (a *App) apiMenuLeave(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "POST") {
		return
	}
	if err := a.requireControl(r); err != nil {
		errJSON(w, err)
		return
	}
	name, wasOpen, err := a.leaveMenu()
	if err != nil {
		errJSON(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"closed": wasOpen, "menu": name})
}

// ---------------------------------------------------------- location & range
//
// LCR reads or writes the scanner's latitude, longitude and scan range. While
// a menu is open on the radio it answers LCR,ERR (seen on an SDS200E), so a
// read is retried and, if it still fails, explained by explainReply.

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
	return 0, 0, 0, a.explainReply("LCR", raw, "its location")
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
	a.prepareForWrite()
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

// apiModulation changes the modulation of the frequency the scanner is
// holding in quick search, without retuning it.
func (a *App) apiModulation(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "POST") {
		return
	}
	if err := a.requireControl(r); err != nil {
		errJSON(w, err)
		return
	}
	var b struct {
		Mode string `json:"mode"`
	}
	if err := readJSON(r, &b); err != nil {
		errJSON(w, err)
		return
	}
	if normaliseMode(b.Mode) == "" {
		errJSON(w, &httpError{422, "mode must be AM, NFM, FM, WFM or FMB"})
		return
	}
	if _, err := a.readSearchHold(); err != nil {
		errJSON(w, &httpError{409, "modulation can only be changed while the scanner is holding a tuned frequency. Tune to one first; a programmed channel takes its modulation from its programming"})
		return
	}
	if err := a.setSearchModulation(b.Mode); err != nil {
		errJSON(w, err)
		return
	}
	h, _ := a.readSearchHold()
	a.invalidateState()
	writeJSON(w, 200, map[string]any{"tuned_mhz": h.MHz, "tuned_mode": h.Mod})
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
		MHz  float64 `json:"mhz"`
		Mode string  `json:"mode"`
	}
	if err := readJSON(r, &b); err != nil {
		errJSON(w, err)
		return
	}
	d, err := a.tuneFrequency(b.MHz, b.Mode)
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
