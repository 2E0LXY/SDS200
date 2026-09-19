package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var keyCodes = map[string]string{"M": "Menu", "F": "Function", "L": "Avoid", "0": "0", "1": "1", "2": "2", "3": "3", "4": "4", "5": "5", "6": "6", "7": "7", "8": "8", "9": "9", ".": "No / Decimal", "E": "Enter / Yes", ">": "Rotary right", "<": "Rotary left", "^": "Rotary push", "V": "Volume knob push", "Q": "Squelch knob push", "Y": "Replay", "A": "Soft 1 / System", "B": "Soft 2 / Department", "C": "Soft 3 / Channel", "Z": "Zip", "T": "Service Type", "R": "Range"}
var navTargets = map[string]bool{"SYS": true, "DEPT": true, "SITE": true, "CFREQ": true, "TGID": true, "STGID": true, "WX": true, "FTO": true, "CCHIT": true, "CS_FREQ": true, "QS_FREQ": true}

func (a *App) apiConfig(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "GET") {
		return
	}
	c := a.configSnapshot()
	writeJSON(w, 200, map[string]any{"host": c.Host, "udp_port": c.UDPPort, "http_port": c.HTTPPort, "rtsp_port": c.RTSPPort, "rtsp_path": c.RTSPPath, "control_token_required": c.ControlToken != "", "lease_ttl": c.LeaseTTL, "version": appVersion, "real_radio_only": true, "log_level": a.log.Level(), "log_dir": filepath.Join(appDataDir(), "logs")})
}
func (a *App) apiCapabilities(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "GET") {
		return
	}
	targets := []string{}
	for k := range navTargets {
		targets = append(targets, k)
	}
	writeJSON(w, 200, map[string]any{"key_codes": keyCodes, "navigation_targets": targets, "volume": map[string]int{"min": 0, "max": 29}, "squelch": map[string]int{"min": 0, "max": 19}, "favorites_quick_keys": true, "waterfall": true, "analysis": []string{"SYSTEM_STATUS", "CURRENT_ACTIVITY", "LCN_MONITOR", "RF_POWER_PLOT"}, "scanner_recording": true, "remote_recording": true, "service_type_direct_write": true, "clock": true, "logging": []string{"ERROR", "WARN", "INFO", "DEBUG", "TRACE"}})
}
func (a *App) apiState(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "GET") {
		return
	}
	writeJSON(w, 200, a.readState(r.URL.Query().Get("force") == "1"))
}
func (a *App) apiDisplay(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "GET") {
		return
	}
	res, err := a.command("STS", 1500*time.Millisecond)
	if err != nil {
		errJSON(w, err)
		return
	}
	d := parseSTS(res.Raw, res.ElapsedMS)
	a.log.Debug("STS", "display read", map[string]any{"display_form": d.DisplayForm, "lines": len(d.Lines), "elapsed_ms": d.ElapsedMS})
	if d.ParseError != "" {
		a.log.Warn("STS", "display parse issue", map[string]any{"error": d.ParseError, "raw": safeRawForLog(d.Raw, 1200)})
	}
	writeJSON(w, 200, d)
}

func (a *App) apiLeaseAcquire(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "POST") {
		return
	}
	var b struct {
		ClientID string `json:"client_id"`
	}
	if err := readJSON(r, &b); err != nil {
		errJSON(w, err)
		return
	}
	if len(b.ClientID) < 8 {
		errJSON(w, &httpError{422, "client_id too short"})
		return
	}
	cfg := a.configSnapshot()
	if cfg.ControlToken != "" && r.Header.Get("X-Control-Token") != cfg.ControlToken {
		errJSON(w, &httpError{403, "invalid control token"})
		return
	}
	now := time.Now()
	a.leaseMu.Lock()
	defer a.leaseMu.Unlock()
	if a.leaseOwner != "" && a.leaseOwner != b.ClientID && now.Before(a.leaseExpires) {
		writeJSON(w, 200, map[string]any{"acquired": false, "busy": true, "expires_in": int(time.Until(a.leaseExpires).Seconds())})
		return
	}
	changed := a.leaseOwner != b.ClientID
	a.leaseOwner = b.ClientID
	a.leaseExpires = now.Add(time.Duration(cfg.LeaseTTL) * time.Second)
	if changed {
		a.log.Info("LEASE", "Operator control acquired", map[string]any{"client": shorten(b.ClientID, 8)})
	}
	writeJSON(w, 200, map[string]any{"acquired": true, "busy": false, "expires_in": cfg.LeaseTTL})
}
func (a *App) apiLeaseRelease(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "POST") {
		return
	}
	var b struct {
		ClientID string `json:"client_id"`
	}
	if err := readJSON(r, &b); err != nil {
		errJSON(w, err)
		return
	}
	a.leaseMu.Lock()
	released := a.leaseOwner == b.ClientID
	if released {
		a.leaseOwner = ""
		a.leaseExpires = time.Time{}
	}
	a.leaseMu.Unlock()
	if released {
		a.log.Info("LEASE", "Operator control released", map[string]any{"client": shorten(b.ClientID, 8)})
	}
	writeJSON(w, 200, map[string]any{"released": released})
}

func (a *App) apiVolume(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "POST") {
		return
	}
	if err := a.requireControl(r); err != nil {
		errJSON(w, err)
		return
	}
	var b struct {
		Level int `json:"level"`
	}
	if err := readJSON(r, &b); err != nil {
		errJSON(w, err)
		return
	}
	if b.Level < 0 || b.Level > 29 {
		errJSON(w, &httpError{422, "SDS200 volume must be 0..29"})
		return
	}
	a.log.Info("CONTROL", "Volume requested", map[string]any{"level": b.Level})
	d, err := a.execWrite(fmt.Sprintf("VOL,%d", b.Level), 1250*time.Millisecond)
	if err != nil {
		errJSON(w, err)
		return
	}
	a.invalidateState()
	writeJSON(w, 200, d)
}
func (a *App) apiSquelch(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "POST") {
		return
	}
	if err := a.requireControl(r); err != nil {
		errJSON(w, err)
		return
	}
	var b struct {
		Level int `json:"level"`
	}
	if err := readJSON(r, &b); err != nil {
		errJSON(w, err)
		return
	}
	if b.Level < 0 || b.Level > 19 {
		errJSON(w, &httpError{422, "SDS200 squelch must be 0..19"})
		return
	}
	a.log.Info("CONTROL", "Squelch requested", map[string]any{"level": b.Level})
	d, err := a.execWrite(fmt.Sprintf("SQL,%d", b.Level), 1250*time.Millisecond)
	if err != nil {
		errJSON(w, err)
		return
	}
	a.invalidateState()
	writeJSON(w, 200, d)
}
func (a *App) apiRecord(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "POST") {
		return
	}
	if err := a.requireControl(r); err != nil {
		errJSON(w, err)
		return
	}
	var b struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &b); err != nil {
		errJSON(w, err)
		return
	}
	a.log.Info("CONTROL", "Scanner SD recording requested", map[string]any{"enabled": b.Enabled})
	v := 0
	if b.Enabled {
		v = 1
	}
	d, err := a.execWrite(fmt.Sprintf("URC,%d", v), 1250*time.Millisecond)
	if err != nil {
		errJSON(w, err)
		return
	}
	a.invalidateState()
	writeJSON(w, 200, d)
}
func (a *App) apiKey(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "POST") {
		return
	}
	if err := a.requireControl(r); err != nil {
		errJSON(w, err)
		return
	}
	var b struct {
		Key string `json:"key"`
	}
	if err := readJSON(r, &b); err != nil {
		errJSON(w, err)
		return
	}
	key := strings.TrimSpace(b.Key)
	if key != "." {
		key = strings.ToUpper(key)
	}
	label, ok := keyCodes[key]
	if !ok {
		errJSON(w, &httpError{422, "key code not allowed"})
		return
	}
	wire := fmt.Sprintf("KEY,%s,P", key)
	a.log.Info("CONTROL", "Key press", map[string]any{"button": label, "key": key, "command": wire})
	d, err := a.execWrite(wire, 1250*time.Millisecond)
	if err != nil {
		errJSON(w, err)
		return
	}
	// The scanner blanks its display briefly while redrawing after a key, so
	// retry the STS confirmation a few times until a non-empty frame arrives.
	var disp STSDisplay
	var e error
	for attempt := 0; attempt < 4; attempt++ {
		time.Sleep(time.Duration(90+attempt*110) * time.Millisecond)
		var sts CommandResult
		if sts, e = a.command("STS", 1200*time.Millisecond); e != nil {
			continue
		}
		disp = parseSTS(sts.Raw, sts.ElapsedMS)
		if !displayBlank(disp) {
			break
		}
	}
	if e == nil {
		d["display"] = disp
		a.log.Info("CONTROL", "Key confirmed by STS", map[string]any{"button": label, "key": key, "display": displaySummary(disp)})
	} else {
		a.log.Warn("CONTROL", "Key acknowledgement received but STS confirmation failed", map[string]any{"button": label, "key": key, "error": e.Error()})
	}
	a.invalidateState()
	writeJSON(w, 200, d)
}
func displayBlank(d STSDisplay) bool {
	for _, l := range d.Lines {
		if strings.TrimSpace(l.Text) != "" {
			return false
		}
	}
	return true
}
func displaySummary(d STSDisplay) string {
	parts := []string{}
	for _, l := range d.Lines {
		if strings.TrimSpace(l.Text) != "" {
			parts = append(parts, strings.TrimSpace(l.Text))
		}
	}
	if len(parts) > 4 {
		parts = parts[len(parts)-4:]
	}
	return strings.Join(parts, " | ")
}
func (a *App) invalidateState() { a.stateMu.Lock(); a.stateAt = time.Time{}; a.stateMu.Unlock() }

func (a *App) apiHold(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "POST") {
		return
	}
	if err := a.requireControl(r); err != nil {
		errJSON(w, err)
		return
	}
	scope := strings.TrimPrefix(r.URL.Path, "/api/control/hold/")
	var b struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &b); err != nil {
		errJSON(w, err)
		return
	}
	seq := map[string][]string{"system": {"A"}, "department": {"B"}, "site": {"F", "B"}, "channel": {"C"}}[scope]
	if len(seq) == 0 {
		errJSON(w, &httpError{404, "unknown hold scope"})
		return
	}
	before := a.readState(true)
	cur := heldValue(before[scope+"_hold"])
	if cur != nil && *cur == b.Enabled {
		writeJSON(w, 200, map[string]any{"scope": scope, "enabled": b.Enabled, "changed": false, "confirmed": true})
		return
	}
	a.log.Info("CONTROL", "Hold change requested", map[string]any{"scope": scope, "enabled": b.Enabled})
	for _, k := range seq {
		if _, err := a.execWrite("KEY,"+k+",P", 1250*time.Millisecond); err != nil {
			errJSON(w, err)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	a.invalidateState()
	time.Sleep(120 * time.Millisecond)
	after := a.readState(true)
	v := heldValue(after[scope+"_hold"])
	if v == nil || *v != b.Enabled {
		a.log.Warn("CONTROL", "Hold not confirmed", map[string]any{"scope": scope, "requested": b.Enabled, "reported": after[scope+"_hold"]})
		errJSON(w, &httpError{409, "scanner did not confirm hold state"})
		return
	}
	writeJSON(w, 200, map[string]any{"scope": scope, "enabled": b.Enabled, "changed": true, "confirmed": true})
}
func heldValue(v any) *bool {
	s := strings.ToLower(strings.TrimSpace(fmt.Sprint(v)))
	if s == "on" || s == "1" || s == "true" || s == "held" {
		b := true
		return &b
	}
	if s == "off" || s == "0" || s == "false" || s == "released" {
		b := false
		return &b
	}
	return nil
}
func (a *App) apiNav(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "POST") {
		return
	}
	if err := a.requireControl(r); err != nil {
		errJSON(w, err)
		return
	}
	dir := strings.TrimPrefix(r.URL.Path, "/api/nav/")
	var b struct {
		Target string `json:"target"`
		First  any    `json:"first"`
		Second any    `json:"second"`
		Count  int    `json:"count"`
	}
	if err := readJSON(r, &b); err != nil {
		errJSON(w, err)
		return
	}
	t := strings.ToUpper(strings.TrimSpace(b.Target))
	if !navTargets[t] {
		errJSON(w, &httpError{422, "unsupported navigation target"})
		return
	}
	cmd := ""
	switch dir {
	case "next":
		cmd = "NXT"
	case "previous":
		cmd = "PRV"
	case "hold":
		cmd = "HLD"
	default:
		errJSON(w, &httpError{404, "unknown navigation direction"})
		return
	}
	first := wireValue(b.First)
	second := wireValue(b.Second)
	wire := ""
	if cmd == "HLD" {
		wire = fmt.Sprintf("HLD,%s,%s,%s", t, first, second)
	} else {
		if b.Count == 0 {
			b.Count = 1
		}
		if b.Count < 1 || b.Count > 8 {
			errJSON(w, &httpError{422, "count must be 1..8"})
			return
		}
		wire = fmt.Sprintf("%s,%s,%s,%s,%d", cmd, t, first, second, b.Count)
	}
	a.log.Info("CONTROL", "Navigation", map[string]any{"direction": dir, "target": t, "first": first, "second": second, "count": b.Count})
	d, err := a.execWrite(wire, 1250*time.Millisecond)
	if err != nil {
		errJSON(w, err)
		return
	}
	a.invalidateState()
	writeJSON(w, 200, d)
}
func wireValue(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case float64:
		return strconv.Itoa(int(x))
	case string:
		return x
	default:
		return fmt.Sprint(x)
	}
}

func (a *App) apiFQK(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		res, err := a.command("FQK", 1500*time.Millisecond)
		if err != nil {
			errJSON(w, err)
			return
		}
		f := packetFields(res.Raw, "FQK")
		states := make([]int, 0, 100)
		for _, s := range f {
			if len(states) >= 100 {
				break
			}
			n, e := strconv.Atoi(strings.TrimSpace(s))
			if e != nil {
				n = 0
			}
			states = append(states, n)
		}
		for len(states) < 100 {
			states = append(states, 0)
		}
		writeJSON(w, 200, map[string]any{"command": "FQK", "raw": res.Raw, "elapsed_ms": res.ElapsedMS, "states": states})
		return
	}
	if r.Method == "POST" {
		if err := a.requireControl(r); err != nil {
			errJSON(w, err)
			return
		}
		var b struct {
			States []int `json:"states"`
		}
		if err := readJSON(r, &b); err != nil {
			errJSON(w, err)
			return
		}
		if len(b.States) != 100 {
			errJSON(w, &httpError{422, "FQK requires exactly 100 states"})
			return
		}
		for _, v := range b.States {
			if v < 0 || v > 2 {
				errJSON(w, &httpError{422, "FQK states must be 0/1/2"})
				return
			}
		}
		a.log.Info("CONTROL", "Favorites Quick Keys write", map[string]any{"count": 100})
		d, err := a.execWrite("FQK,"+joinInts(b.States), 2*time.Second)
		if err != nil {
			errJSON(w, err)
			return
		}
		writeJSON(w, 200, d)
		return
	}
	w.Header().Set("Allow", "GET, POST")
	writeJSON(w, 405, map[string]any{"detail": "method not allowed"})
}
func joinInts(xs []int) string {
	p := make([]string, len(xs))
	for i, v := range xs {
		p[i] = strconv.Itoa(v)
	}
	return strings.Join(p, ",")
}
func (a *App) apiFavorites(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "GET") {
		return
	}
	res, err := a.command("GLT,FL", 2500*time.Millisecond)
	if err != nil {
		errJSON(w, err)
		return
	}
	d, e := xmlRecords(res.Raw)
	if e != nil {
		errJSON(w, e)
		return
	}
	d["elapsed_ms"] = res.ElapsedMS
	writeJSON(w, 200, d)
}
func (a *App) apiMemory(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "GET") {
		return
	}
	kind := strings.ToUpper(r.URL.Query().Get("kind"))
	allowed := map[string]bool{"FL": true, "SYS": true, "DEPT": true, "SITE": true, "CFREQ": true, "TGID": true, "SFREQ": true}
	if !allowed[kind] {
		errJSON(w, &httpError{422, "memory kind must be FL, SYS, DEPT, SITE, CFREQ, TGID or SFREQ"})
		return
	}
	parent := r.URL.Query().Get("parent")
	wire := "GLT," + kind
	if kind != "FL" {
		if parent == "" {
			errJSON(w, &httpError{422, "parent is required for this memory level"})
			return
		}
		wire += "," + parent
	}
	a.log.Debug("MEMORY", "GLT read", map[string]any{"kind": kind, "parent": parent, "command": wire})
	res, err := a.command(wire, 2500*time.Millisecond)
	if err != nil {
		errJSON(w, err)
		return
	}
	d, e := xmlRecords(res.Raw)
	if e != nil {
		errJSON(w, e)
		return
	}
	d["elapsed_ms"] = res.ElapsedMS
	writeJSON(w, 200, d)
}
func (a *App) apiMenu(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "GET") {
		return
	}
	d, err := a.execWrite("MSI", 2*time.Second)
	if err != nil {
		errJSON(w, err)
		return
	}
	writeJSON(w, 200, d)
}

func (a *App) apiWaterfallStatus(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "GET") {
		return
	}
	res, err := a.command("GST", 1500*time.Millisecond)
	if err != nil {
		errJSON(w, err)
		return
	}
	d := parseGST(res.Raw)
	d["elapsed_ms"] = res.ElapsedMS
	writeJSON(w, 200, d)
}
func (a *App) apiWaterfallStart(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "POST") {
		return
	}
	if err := a.requireControl(r); err != nil {
		errJSON(w, err)
		return
	}
	if err := a.waterfall.Start(); err != nil {
		errJSON(w, err)
		return
	}
	writeJSON(w, 200, a.waterfall.Snapshot())
}
func (a *App) apiWaterfallStop(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "POST") {
		return
	}
	if err := a.requireControl(r); err != nil {
		errJSON(w, err)
		return
	}
	d := a.waterfall.Stop()
	writeJSON(w, 200, d)
}
func (a *App) apiWaterfallLatest(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "GET") {
		return
	}
	writeJSON(w, 200, a.waterfall.Snapshot())
}

func (a *App) apiAnalysisStart(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "POST") {
		return
	}
	if err := a.requireControl(r); err != nil {
		errJSON(w, err)
		return
	}
	var b struct {
		Mode         string `json:"mode"`
		SiteIndex    *int   `json:"site_index"`
		Frequency    *int   `json:"frequency"`
		Modulation   string `json:"modulation"`
		SamplingRate int    `json:"sampling_rate"`
	}
	if err := readJSON(r, &b); err != nil {
		errJSON(w, err)
		return
	}
	mode := strings.ToUpper(strings.TrimSpace(b.Mode))
	wire := ""
	switch mode {
	case "SYSTEM_STATUS", "CURRENT_ACTIVITY", "LCN_MONITOR":
		if b.SiteIndex == nil {
			errJSON(w, &httpError{422, "site_index is required"})
			return
		}
		wire = fmt.Sprintf("AST,%s,%d", mode, *b.SiteIndex)
	case "RF_POWER_PLOT":
		if b.Frequency == nil {
			errJSON(w, &httpError{422, "frequency is required"})
			return
		}
		mod := b.Modulation
		if mod == "" {
			mod = "Auto"
		}
		rate := b.SamplingRate
		if rate == 0 {
			rate = 100
		}
		wire = fmt.Sprintf("AST,%s,%d,%s,%d", mode, *b.Frequency, mod, rate)
	default:
		errJSON(w, &httpError{422, "unsupported analysis mode"})
		return
	}
	d, err := a.execWrite(wire, 2*time.Second)
	if err != nil {
		errJSON(w, err)
		return
	}
	writeJSON(w, 200, d)
}
func (a *App) apiAnalysisPause(w http.ResponseWriter, r *http.Request) {
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
	mode := strings.ToUpper(strings.TrimSpace(b.Mode))
	allowed := map[string]bool{"SYSTEM_STATUS": true, "RF_POWER_PLOT": true, "CURRENT_ACTIVITY": true, "LCN_MONITOR": true, "ACTIVITY_LOG": true, "RAW_DATA_OUTPUT": true}
	if !allowed[mode] {
		errJSON(w, &httpError{422, "unsupported analysis mode"})
		return
	}
	d, err := a.execWrite("APR,"+mode, 2*time.Second)
	if err != nil {
		errJSON(w, err)
		return
	}
	writeJSON(w, 200, d)
}

func (a *App) apiAudioStatus(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "GET") {
		return
	}
	writeJSON(w, 200, a.audio.Status())
}
func (a *App) apiAudioStart(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "POST") {
		return
	}
	if err := a.requireControl(r); err != nil {
		errJSON(w, err)
		return
	}
	d, err := a.audio.Acquire("web")
	if err != nil {
		errJSON(w, err)
		return
	}
	writeJSON(w, 200, d)
}
func (a *App) apiAudioStop(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "POST") {
		return
	}
	if err := a.requireControl(r); err != nil {
		errJSON(w, err)
		return
	}
	a.audio.Release("web")
	writeJSON(w, 200, a.audio.Status())
}
func (a *App) apiAudioLive(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeJSON(w, 405, map[string]any{"detail": "method not allowed"})
		return
	}
	if err := a.audio.StreamWAV(w, r); err != nil {
		a.log.Warn("AUDIO", "Browser audio stream failed", map[string]any{"error": err.Error()})
		if !headerWritten(w) {
			errJSON(w, err)
		}
	}
}
func headerWritten(w http.ResponseWriter) bool {
	if sw, ok := w.(*statusWriter); ok {
		return sw.status != 0
	}
	return false
}

func (a *App) apiRemoteRecordState(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeJSON(w, 405, map[string]any{"detail": "method not allowed"})
		return
	}
	writeJSON(w, 200, a.recorder.State())
}
func (a *App) apiRemoteRecordStart(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "POST") {
		return
	}
	if err := a.requireControl(r); err != nil {
		errJSON(w, err)
		return
	}
	d, err := a.recorder.Start()
	if err != nil {
		errJSON(w, err)
		return
	}
	writeJSON(w, 200, d)
}
func (a *App) apiRemoteRecordStop(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "POST") {
		return
	}
	if err := a.requireControl(r); err != nil {
		errJSON(w, err)
		return
	}
	if err := a.recorder.Stop(); err != nil {
		errJSON(w, err)
		return
	}
	writeJSON(w, 200, a.recorder.State())
}
func (a *App) serveRecording(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(strings.TrimPrefix(r.URL.Path, "/recordings/"))
	if name == "." || name == "" {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(a.recorder.dir, name)
	http.ServeFile(w, r, path)
}

func (a *App) apiDiagnostics(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "GET") {
		return
	}
	s := a.readState(false)
	a.leaseMu.Lock()
	lease := map[string]any{"active": a.leaseOwner != "" && time.Now().Before(a.leaseExpires), "expires_in": maxInt(0, int(time.Until(a.leaseExpires).Seconds()))}
	a.leaseMu.Unlock()
	writeJSON(w, 200, map[string]any{"scanner_online": s["online"], "udp_control": map[bool]string{true: "ok", false: "offline"}[s["online"] == true], "audio": a.audio.Status(), "waterfall": a.waterfall.Snapshot(), "operator_lease": lease, "version": appVersion, "log_level": a.log.Level(), "log_file": a.log.CurrentPath(), "scanner_host": a.currentHost(), "local_ip": routeLocalIPv4(a.currentHost(), a.udpPort())})
}

func (a *App) apiLogs(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "GET") {
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	writeJSON(w, 200, map[string]any{"level": a.log.Level(), "entries": a.log.Entries(limit, q.Get("level"), q.Get("component"), q.Get("q")), "file": a.log.CurrentPath()})
}
func (a *App) apiLogDownload(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "GET") {
		return
	}
	path := a.log.CurrentPath()
	if path == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filepath.Base(path)))
	http.ServeFile(w, r, path)
}
func (a *App) apiLogOpenFolder(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "POST") {
		return
	}
	if err := a.requireControl(r); err != nil {
		errJSON(w, err)
		return
	}
	dir := filepath.Join(appDataDir(), "logs")
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer.exe", dir)
	case "darwin":
		cmd = exec.Command("open", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	if err := cmd.Start(); err != nil {
		a.log.Warn("APP", "Could not open log folder", map[string]any{"path": dir, "error": err.Error()})
		errJSON(w, err)
		return
	}
	a.log.Info("APP", "Opened log folder", map[string]any{"path": dir})
	writeJSON(w, 200, map[string]any{"opened": true, "path": dir})
}

func (a *App) apiClientLog(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "POST") {
		return
	}
	var b struct {
		Level     string `json:"level"`
		Message   string `json:"message"`
		Component string `json:"component"`
	}
	if err := readJSON(r, &b); err != nil {
		errJSON(w, err)
		return
	}
	if b.Component == "" {
		b.Component = "BROWSER"
	}
	a.log.Log(parseLogLevel(b.Level), b.Component, shorten(b.Message, 2000), nil)
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (a *App) apiLogging(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		writeJSON(w, 200, map[string]any{"level": a.log.Level(), "file": a.log.CurrentPath()})
		return
	}
	if r.Method != "POST" {
		writeJSON(w, 405, map[string]any{"detail": "method not allowed"})
		return
	}
	if err := a.requireControl(r); err != nil {
		errJSON(w, err)
		return
	}
	var b struct {
		Level string `json:"level"`
	}
	if err := readJSON(r, &b); err != nil {
		errJSON(w, err)
		return
	}
	lvl := strings.ToUpper(strings.TrimSpace(b.Level))
	if !map[string]bool{"ERROR": true, "WARN": true, "INFO": true, "DEBUG": true, "TRACE": true}[lvl] {
		errJSON(w, &httpError{422, "level must be ERROR/WARN/INFO/DEBUG/TRACE"})
		return
	}
	got := a.log.SetLevel(lvl)
	a.mu.Lock()
	a.cfg.LogLevel = got
	cfg := a.cfg
	a.mu.Unlock()
	_ = saveJSON(a.cfgPath, cfg)
	a.log.Info("APP", "Log level changed", map[string]any{"level": got})
	writeJSON(w, 200, map[string]any{"level": got})
}
func (a *App) apiDiagnosticBundle(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "GET") {
		return
	}
	state := a.readState(false)
	var display any
	if res, err := a.command("STS", 1200*time.Millisecond); err == nil {
		display = parseSTS(res.Raw, res.ElapsedMS)
	} else {
		display = map[string]any{"error": err.Error()}
	}
	writeJSON(w, 200, map[string]any{"generated_at": time.Now().Format(time.RFC3339), "version": appVersion, "scanner": map[string]any{"host": a.currentHost(), "model": a.model, "firmware": a.firmware}, "state": state, "audio": a.audio.Status(), "waterfall": a.waterfall.Snapshot(), "display": display, "log_tail": a.log.TailText(80)})
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (a *App) apiDiscoveryStatus(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "GET") {
		return
	}
	a.discoveryMu.Lock()
	d := a.discovering
	m := a.discoveryMessage
	a.discoveryMu.Unlock()
	writeJSON(w, 200, map[string]any{"discovering": d, "message": m, "selected": a.currentHost(), "model": a.model})
}
func (a *App) apiDiscoveryManual(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "POST") {
		return
	}
	var b struct {
		Host string `json:"host"`
	}
	if err := readJSON(r, &b); err != nil {
		errJSON(w, err)
		return
	}
	ip := netParsePrivateIPv4(b.Host)
	if ip == "" {
		errJSON(w, &httpError{422, "enter a private IPv4 scanner address"})
		return
	}
	a.log.Info("DISCOVERY", "Manual scanner verification", map[string]any{"host": ip})
	model, err := probeModel(ip, a.udpPort(), 1800*time.Millisecond)
	if err != nil {
		a.log.Warn("DISCOVERY", "Manual scanner verification failed", map[string]any{"host": ip, "error": err.Error()})
		errJSON(w, err)
		return
	}
	if err := a.setHost(ip, model); err != nil {
		errJSON(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"selected": ip, "model": model})
}
func (a *App) apiDiscoveryScan(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, "POST") {
		return
	}
	go a.startDiscovery()
	writeJSON(w, 202, map[string]any{"started": true})
}

func (a *App) startDiscovery() {
	a.discoveryMu.Lock()
	if a.discovering {
		a.discoveryMu.Unlock()
		return
	}
	a.discovering = true
	a.discoveryMessage = "Searching local network…"
	a.discoveryMu.Unlock()
	defer func() {
		a.discoveryMu.Lock()
		a.discovering = false
		if a.currentHost() == "" && strings.Contains(a.discoveryMessage, "Searching") {
			a.discoveryMessage = "No SDS200/SDS200E found"
		}
		a.discoveryMu.Unlock()
	}()
	a.log.Info("DISCOVERY", "Network discovery started", nil)
	cands := localSubnetCandidates()
	if len(cands) == 0 {
		a.discoveryMu.Lock()
		a.discoveryMessage = "No private IPv4 interface available"
		a.discoveryMu.Unlock()
		return
	}
	type found struct{ host, model string }
	ch := make(chan found, 1)
	sem := make(chan struct{}, 32)
	done := make(chan struct{})
	for _, h := range cands {
		h := h
		go func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			m, e := probeModel(h, a.udpPort(), 900*time.Millisecond)
			if e == nil {
				select {
				case ch <- found{h, m}:
					close(done)
				case <-done:
				}
			}
		}()
	}
	select {
	case f := <-ch:
		_ = a.setHost(f.host, f.model)
		a.discoveryMu.Lock()
		a.discoveryMessage = fmt.Sprintf("Found %s at %s", f.model, f.host)
		a.discoveryMu.Unlock()
		a.log.Info("DISCOVERY", "Network discovery found scanner", map[string]any{"host": f.host, "model": f.model})
	case <-time.After(4 * time.Second):
		a.discoveryMu.Lock()
		a.discoveryMessage = "No SDS200/SDS200E found on local /24 networks"
		a.discoveryMu.Unlock()
		a.log.Warn("DISCOVERY", "Network discovery completed without a scanner", map[string]any{"candidates": len(cands)})
	}
}

func netParsePrivateIPv4(s string) string {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil || ip.To4() == nil {
		return ""
	}
	v := ip.To4()
	if v[0] == 10 || (v[0] == 172 && v[1] >= 16 && v[1] <= 31) || (v[0] == 192 && v[1] == 168) {
		return v.String()
	}
	return ""
}
func localSubnetCandidates() []string {
	seen := map[string]bool{}
	ifs, _ := net.Interfaces()
	for _, inf := range ifs {
		addrs, _ := inf.Addrs()
		for _, ad := range addrs {
			ipnet, ok := ad.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if netParsePrivateIPv4(ip.String()) == "" {
				continue
			}
			for i := 1; i < 255; i++ {
				if byte(i) == ip[3] {
					continue
				}
				h := fmt.Sprintf("%d.%d.%d.%d", ip[0], ip[1], ip[2], i)
				seen[h] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	return out
}
func routeLocalIPv4(host string, port int) string {
	if host == "" {
		return ""
	}
	c, err := net.Dial("udp4", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return ""
	}
	defer c.Close()
	if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
		return a.IP.String()
	}
	return ""
}

var _ = json.Valid
var _ = os.ErrNotExist

// Service types: SVC returns 37 preset + 10 custom 0/1 flags (spec v2.00 p.6).
var presetServiceTypes = []string{"Multi-Dispatch", "Law Dispatch", "Fire Dispatch", "EMS Dispatch", "", "Multi-Tac", "Law Tac", "Fire-Tac", "EMS-Tac", "", "Interop", "Hospital", "Ham", "Public Works", "Aircraft", "Federal", "Business", "", "", "Railroad", "Other", "Multi-Talk", "Law Talk", "Fire-Talk", "EMS-Talk", "Transportation", "", "", "Emergency Ops", "Military", "Media", "Schools", "Security", "Utilities", "", "", "Corrections"}

func (a *App) apiServiceTypes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		res, err := a.command("SVC", 1500*time.Millisecond)
		if err != nil {
			errJSON(w, err)
			return
		}
		f := packetFields(res.Raw, "SVC")
		if len(f) < 47 {
			errJSON(w, &httpError{502, fmt.Sprintf("SVC returned %d fields, expected 47", len(f))})
			return
		}
		items := make([]map[string]any, 0, 47)
		for i := 0; i < 47; i++ {
			name := ""
			if i < 37 {
				name = presetServiceTypes[i]
			} else {
				name = fmt.Sprintf("Custom %d", i-36)
			}
			items = append(items, map[string]any{"slot": i, "name": name, "enabled": strings.TrimSpace(f[i]) == "1", "custom": i >= 37})
		}
		writeJSON(w, 200, map[string]any{"items": items, "raw": res.Raw})
	case "POST":
		if err := a.requireControl(r); err != nil {
			errJSON(w, err)
			return
		}
		var b struct {
			States []int `json:"states"`
		}
		if err := readJSON(r, &b); err != nil {
			errJSON(w, err)
			return
		}
		if len(b.States) != 47 {
			errJSON(w, &httpError{422, "SVC requires exactly 47 states (37 preset + 10 custom)"})
			return
		}
		for _, v := range b.States {
			if v != 0 && v != 1 {
				errJSON(w, &httpError{422, "service type states must be 0 or 1"})
				return
			}
		}
		a.log.Info("CONTROL", "Service types write", nil)
		d, err := a.execWrite("SVC,"+joinInts(b.States), 2*time.Second)
		if err != nil {
			errJSON(w, err)
			return
		}
		writeJSON(w, 200, d)
	default:
		writeJSON(w, 405, map[string]any{"detail": "method not allowed"})
	}
}

// Clock: DTM,[DST],[YYYY],[MM],[DD],[hh],[mm],[ss],[RTC status].
func (a *App) apiClock(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		res, err := a.command("DTM", 1200*time.Millisecond)
		if err != nil {
			errJSON(w, err)
			return
		}
		f := packetFields(res.Raw, "DTM")
		if len(f) < 7 {
			errJSON(w, &httpError{502, "unexpected DTM response"})
			return
		}
		n := make([]int, 7)
		for i := 0; i < 7; i++ {
			n[i], _ = strconv.Atoi(strings.TrimSpace(f[i]))
		}
		scanner := time.Date(n[1], time.Month(n[2]), n[3], n[4], n[5], n[6], 0, time.Local)
		now := time.Now()
		writeJSON(w, 200, map[string]any{"scanner_time": scanner.Format("2006-01-02 15:04:05"), "host_time": now.Format("2006-01-02 15:04:05"), "dst": n[0] == 1, "rtc_ok": len(f) > 7 && strings.TrimSpace(f[7]) == "1", "offset_s": int(scanner.Sub(now).Seconds()), "raw": res.Raw})
	case "POST":
		if err := a.requireControl(r); err != nil {
			errJSON(w, err)
			return
		}
		// The scanner displays DTM time as-is; write host local wall-clock time
		// with the DST flag cleared so no further offset is applied.
		now := time.Now()
		wire := fmt.Sprintf("DTM,0,%d,%d,%d,%d,%d,%d", now.Year(), int(now.Month()), now.Day(), now.Hour(), now.Minute(), now.Second())
		a.log.Info("CONTROL", "Scanner clock sync", map[string]any{"command": wire})
		d, err := a.execWrite(wire, 1500*time.Millisecond)
		if err != nil {
			errJSON(w, err)
			return
		}
		writeJSON(w, 200, d)
	default:
		writeJSON(w, 405, map[string]any{"detail": "method not allowed"})
	}
}
