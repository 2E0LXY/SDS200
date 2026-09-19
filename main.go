package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const appVersion = "0.7.1-native"

//go:embed static/*
var staticFS embed.FS

type Config struct {
	Host         string `json:"host"`
	UDPPort      int    `json:"udp_port"`
	HTTPPort     int    `json:"http_port"`
	RTSPPort     int    `json:"rtsp_port"`
	RTSPPath     string `json:"rtsp_path"`
	ControlToken string `json:"control_token,omitempty"`
	LeaseTTL     int    `json:"lease_ttl"`
	StateCacheMS int    `json:"state_cache_ms"`
	LogLevel     string `json:"log_level"`
}

func defaultConfig() Config {
	return Config{UDPPort: 50536, HTTPPort: 8765, RTSPPort: 554, RTSPPath: "/au:scanner.au", LeaseTTL: 60, StateCacheMS: 800, LogLevel: "INFO"}
}
func appDataDir() string {
	if v := os.Getenv("LOCALAPPDATA"); v != "" {
		return filepath.Join(v, "SDS200-WebApp")
	}
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "SDS200-WebApp")
	}
	return filepath.Join(os.TempDir(), "SDS200-WebApp")
}
func loadJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
func saveJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err = os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

type App struct {
	mu         sync.RWMutex
	cfg        Config
	cfgPath    string
	log        *AppLogger
	commandMu  sync.Mutex
	stateMu    sync.Mutex
	stateCache map[string]any
	stateAt    time.Time
	latencyMS  float64
	model      string
	firmware   string

	leaseMu      sync.Mutex
	leaseOwner   string
	leaseExpires time.Time

	discoveryMu      sync.Mutex
	discovering      bool
	discoveryMessage string

	waterfall *WaterfallHub
	audio     *AudioHub
	recorder  *Recorder
}

func NewApp() (*App, error) {
	dir := appDataDir()
	_ = os.MkdirAll(dir, 0o755)
	cfg := defaultConfig()
	cfgPath := filepath.Join(dir, "config.json")
	if err := loadJSON(cfgPath, &cfg); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load config: %w", err)
	}
	d := defaultConfig()
	if cfg.UDPPort == 0 {
		cfg.UDPPort = d.UDPPort
	}
	if cfg.HTTPPort == 0 {
		cfg.HTTPPort = d.HTTPPort
	}
	if cfg.RTSPPort == 0 {
		cfg.RTSPPort = d.RTSPPort
	}
	if cfg.RTSPPath == "" {
		cfg.RTSPPath = d.RTSPPath
	}
	if cfg.LeaseTTL < 20 {
		cfg.LeaseTTL = d.LeaseTTL
	}
	if cfg.StateCacheMS < 250 {
		cfg.StateCacheMS = d.StateCacheMS
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = d.LogLevel
	}
	lg, err := NewAppLogger(filepath.Join(dir, "logs"), cfg.LogLevel)
	if err != nil {
		return nil, err
	}
	a := &App{cfg: cfg, cfgPath: cfgPath, log: lg, stateCache: map[string]any{}, discoveryMessage: "Idle"}
	a.audio = NewAudioHub(a)
	a.waterfall = NewWaterfallHub(a)
	a.recorder = NewRecorder(a, filepath.Join(dir, "recordings"))
	a.log.Info("APP", "SDS200 WebApp starting", map[string]any{"version": appVersion, "data_dir": dir, "real_radio_only": true})
	if cfg.Host != "" {
		a.log.Info("APP", "Configured scanner target", map[string]any{"host": cfg.Host, "udp_port": cfg.UDPPort})
	} else {
		a.log.Info("DISCOVERY", "No saved scanner target", nil)
	}
	return a, nil
}
func (a *App) Close() {
	if a.recorder != nil {
		_ = a.recorder.Stop()
	}
	if a.audio != nil {
		a.audio.ForceStop("application shutdown")
	}
	if a.waterfall != nil {
		a.waterfall.Stop()
	}
	a.log.Info("APP", "Application shutdown", nil)
	a.log.Close()
}
func (a *App) configSnapshot() Config { a.mu.RLock(); defer a.mu.RUnlock(); return a.cfg }
func (a *App) currentHost() string    { a.mu.RLock(); defer a.mu.RUnlock(); return a.cfg.Host }
func (a *App) udpPort() int           { a.mu.RLock(); defer a.mu.RUnlock(); return a.cfg.UDPPort }
func (a *App) rtspSettings() (string, int, string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg.Host, a.cfg.RTSPPort, a.cfg.RTSPPath
}
func (a *App) setHost(host, model string) error {
	a.mu.Lock()
	a.cfg.Host = host
	cfg := a.cfg
	a.mu.Unlock()
	if model != "" {
		a.model = model
	}
	if err := saveJSON(a.cfgPath, cfg); err != nil {
		return err
	}
	a.stateMu.Lock()
	a.stateCache = map[string]any{}
	a.stateAt = time.Time{}
	a.stateMu.Unlock()
	a.log.Info("DISCOVERY", "Scanner selected", map[string]any{"host": host, "model": model})
	return nil
}

func (a *App) command(wire string, timeout time.Duration) (CommandResult, error) {
	host := a.currentHost()
	port := a.udpPort()
	if host == "" {
		return CommandResult{}, errors.New("no scanner selected")
	}
	a.commandMu.Lock()
	defer a.commandMu.Unlock()
	a.log.Debug("UDP", "command send", map[string]any{"command": wire, "host": host, "port": port})
	a.log.Trace("UDP", "wire tx", map[string]any{"wire": wire + "\\r"})
	r, err := udpCommand(host, port, wire, timeout)
	if err != nil {
		a.log.Error("UDP", "command failed", map[string]any{"command": wire, "error": err.Error()})
		return CommandResult{}, err
	}
	a.latencyMS = r.ElapsedMS
	a.log.Debug("UDP", "command complete", map[string]any{"command": wire, "elapsed_ms": r.ElapsedMS, "response": safeRawForLog(r.Raw, 300)})
	a.log.Trace("UDP", "wire rx", map[string]any{"command": wire, "raw": safeRawForLog(r.Raw, 12000)})
	return r, nil
}

func (a *App) execWrite(wire string, timeout time.Duration) (map[string]any, error) {
	r, err := a.command(wire, timeout)
	if err != nil {
		return nil, err
	}
	return map[string]any{"command": r.Command, "raw": r.Raw, "elapsed_ms": r.ElapsedMS}, nil
}

func isSDS200FamilyModel(s string) bool {
	u := strings.ToUpper(strings.TrimSpace(s))
	return u == "SDS200" || u == "SDS200E" || strings.Contains(u, "SDS200E") || strings.Contains(u, "SDS200")
}
func probeModel(host string, port int, timeout time.Duration) (string, error) {
	r, err := udpCommand(host, port, "MDL", timeout)
	if err != nil {
		return "", err
	}
	f := packetFields(r.Raw, "MDL")
	if len(f) == 0 {
		return "", fmt.Errorf("MDL did not identify model")
	}
	model := strings.TrimSpace(f[0])
	if !isSDS200FamilyModel(model) {
		return "", fmt.Errorf("device identified as %q, not SDS200/SDS200E", model)
	}
	return model, nil
}

func (a *App) readState(force bool) map[string]any {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	cfg := a.configSnapshot()
	if !force && !a.stateAt.IsZero() && time.Since(a.stateAt) < time.Duration(cfg.StateCacheMS)*time.Millisecond {
		return copyMap(a.stateCache)
	}
	if cfg.Host == "" {
		return map[string]any{"online": false, "error": "No scanner selected"}
	}
	r, err := a.command("GSI", 3500*time.Millisecond)
	if err != nil {
		return map[string]any{"online": false, "error": err.Error(), "host": cfg.Host}
	}
	info := parseScannerInfo(r.Raw)
	out := map[string]any{"online": true, "simulated": false, "host": cfg.Host, "latency_ms": r.ElapsedMS, "mode": info.Mode, "screen": info.Screen, "monitor_list": info.MonitorList, "system": info.System, "department": info.Department, "site": info.Site, "channel": info.Channel, "channel_kind": info.ChannelKind, "frequency": info.Frequency, "tgid": info.TGID, "unit_id": info.UnitID, "modulation": info.Modulation, "service_type": info.ServiceType, "system_hold": info.SystemHold, "department_hold": info.DepartmentHold, "site_hold": info.SiteHold, "channel_hold": info.ChannelHold, "system_index": info.SystemIndex, "department_index": info.DepartmentIndex, "site_index": info.SiteIndex, "channel_index": info.ChannelIndex, "volume": info.Volume, "squelch": info.Squelch, "rssi": info.RSSI, "signal": info.Signal, "recording": info.Recording, "mute": info.Mute, "attenuator": info.Attenuator, "p25_status": info.P25Status}
	if info.ParseError != "" {
		out["parse_error"] = info.ParseError
		a.log.Warn("GSI", "ScannerInfo parse issue", map[string]any{"error": info.ParseError})
	}
	if a.model == "" {
		if rr, e := a.command("MDL", 900*time.Millisecond); e == nil {
			if f := packetFields(rr.Raw, "MDL"); len(f) > 0 {
				a.model = strings.TrimSpace(f[0])
			}
		}
	}
	if a.firmware == "" {
		if rr, e := a.command("VER", 900*time.Millisecond); e == nil {
			if f := packetFields(rr.Raw, "VER"); len(f) > 0 {
				a.firmware = strings.TrimSpace(f[0])
			}
		}
	}
	out["model"] = a.model
	out["firmware"] = a.firmware
	a.stateCache = copyMap(out)
	a.stateAt = time.Now()
	return copyMap(out)
}
func copyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (a *App) requireControl(r *http.Request) error {
	cfg := a.configSnapshot()
	if cfg.ControlToken != "" && r.Header.Get("X-Control-Token") != cfg.ControlToken {
		return &httpError{403, "invalid control token"}
	}
	id := r.Header.Get("X-Client-Id")
	if id == "" {
		return &httpError{409, "no operator lease"}
	}
	now := time.Now()
	a.leaseMu.Lock()
	defer a.leaseMu.Unlock()
	if a.leaseOwner == "" || now.After(a.leaseExpires) {
		a.leaseOwner = id
		a.leaseExpires = now.Add(time.Duration(cfg.LeaseTTL) * time.Second)
		a.log.Info("LEASE", "Operator control auto-acquired", map[string]any{"client": shorten(id, 8)})
	}
	if a.leaseOwner != id {
		return &httpError{409, "operator lease is held by another client"}
	}
	a.leaseExpires = now.Add(time.Duration(cfg.LeaseTTL) * time.Second)
	return nil
}
func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return e.msg }
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func errJSON(w http.ResponseWriter, err error) {
	var he *httpError
	if errors.As(err, &he) {
		writeJSON(w, he.status, map[string]any{"detail": he.msg})
		return
	}
	writeJSON(w, 504, map[string]any{"detail": err.Error()})
}
func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return &httpError{400, "bad JSON: " + err.Error()}
	}
	return nil
}
func requireMethod(w http.ResponseWriter, r *http.Request, m string) bool {
	if r.Method != m {
		w.Header().Set("Allow", m)
		writeJSON(w, 405, map[string]any{"detail": "method not allowed"})
		return false
	}
	return true
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	staticSub, _ := fs.Sub(staticFS, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))
	mux.HandleFunc("/", a.serveIndex)
	mux.HandleFunc("/api/config", a.apiConfig)
	mux.HandleFunc("/api/capabilities", a.apiCapabilities)
	mux.HandleFunc("/api/state", a.apiState)
	mux.HandleFunc("/api/display", a.apiDisplay)
	mux.HandleFunc("/api/diagnostics", a.apiDiagnostics)
	mux.HandleFunc("/api/lease/acquire", a.apiLeaseAcquire)
	mux.HandleFunc("/api/lease/release", a.apiLeaseRelease)
	mux.HandleFunc("/api/control/volume", a.apiVolume)
	mux.HandleFunc("/api/control/squelch", a.apiSquelch)
	mux.HandleFunc("/api/control/record", a.apiRecord)
	mux.HandleFunc("/api/control/key", a.apiKey)
	mux.HandleFunc("/api/control/hold/", a.apiHold)
	mux.HandleFunc("/api/nav/", a.apiNav)
	mux.HandleFunc("/api/fqk", a.apiFQK)
	mux.HandleFunc("/api/favorites", a.apiFavorites)
	mux.HandleFunc("/api/memory", a.apiMemory)
	mux.HandleFunc("/api/menu", a.apiMenu)
	mux.HandleFunc("/api/waterfall/status", a.apiWaterfallStatus)
	mux.HandleFunc("/api/waterfall/start", a.apiWaterfallStart)
	mux.HandleFunc("/api/waterfall/stop", a.apiWaterfallStop)
	mux.HandleFunc("/api/waterfall/latest", a.apiWaterfallLatest)
	mux.HandleFunc("/api/analysis/start", a.apiAnalysisStart)
	mux.HandleFunc("/api/analysis/pause", a.apiAnalysisPause)
	mux.HandleFunc("/api/audio/status", a.apiAudioStatus)
	mux.HandleFunc("/api/audio/start", a.apiAudioStart)
	mux.HandleFunc("/api/audio/stop", a.apiAudioStop)
	mux.HandleFunc("/api/audio/live.wav", a.apiAudioLive)
	mux.HandleFunc("/api/recording/remote", a.apiRemoteRecordState)
	mux.HandleFunc("/api/recording/remote/start", a.apiRemoteRecordStart)
	mux.HandleFunc("/api/recording/remote/stop", a.apiRemoteRecordStop)
	mux.HandleFunc("/recordings/", a.serveRecording)
	mux.HandleFunc("/api/discovery/status", a.apiDiscoveryStatus)
	mux.HandleFunc("/api/discovery/manual", a.apiDiscoveryManual)
	mux.HandleFunc("/api/discovery/scan", a.apiDiscoveryScan)
	mux.HandleFunc("/api/logs", a.apiLogs)
	mux.HandleFunc("/api/logs/download", a.apiLogDownload)
	mux.HandleFunc("/api/logs/open-folder", a.apiLogOpenFolder)
	mux.HandleFunc("/api/logs/client", a.apiClientLog)
	mux.HandleFunc("/api/logging", a.apiLogging)
	mux.HandleFunc("/api/diagnostics/bundle", a.apiDiagnosticBundle)
	return a.loggingMiddleware(mux)
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusWriter) WriteHeader(n int) { s.status = n; s.ResponseWriter.WriteHeader(n) }
func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.WriteHeader(200)
	}
	n, e := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, e
}
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
func (a *App) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w}
		start := time.Now()
		next.ServeHTTP(sw, r)
		if sw.status == 0 {
			sw.status = 200
		}
		if !strings.HasPrefix(r.URL.Path, "/api/audio/live.wav") {
			a.log.Debug("HTTP", "request", map[string]any{"method": r.Method, "path": r.URL.Path, "status": sw.status, "bytes": sw.bytes, "elapsed_ms": float64(time.Since(start).Microseconds()) / 1000, "remote": r.RemoteAddr})
		}
	})
}

func (a *App) serveIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}
func openBrowser(raw string) {
	if runtime.GOOS == "windows" {
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", raw).Start()
		return
	}
	if runtime.GOOS == "darwin" {
		_ = exec.Command("open", raw).Start()
		return
	}
	_ = exec.Command("xdg-open", raw).Start()
}

func main() {
	a, err := NewApp()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer a.Close()
	cfg := a.configSnapshot()
	srv := &http.Server{Addr: fmt.Sprintf("0.0.0.0:%d", cfg.HTTPPort), Handler: a.routes(), ReadHeaderTimeout: 5 * time.Second}
	if cfg.Host == "" {
		go a.startDiscovery()
	}
	go func() {
		time.Sleep(450 * time.Millisecond)
		openBrowser(fmt.Sprintf("http://127.0.0.1:%d", cfg.HTTPPort))
	}()
	a.log.Info("HTTP", "Web server listening", map[string]any{"address": srv.Addr})
	err = srv.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		a.log.Error("HTTP", "server failed", map[string]any{"error": err.Error()})
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = srv.Shutdown(context.Background())
}

// Keep url imported in the binary for future URI validation and avoid accidental
// acceptance of malformed user-entered scanner hosts.
var _ = url.URL{}
var _ = strconv.IntSize
