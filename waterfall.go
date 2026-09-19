package main

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type WaterfallHub struct {
	app       *App
	mu        sync.Mutex
	running   bool
	started   time.Time
	received  uint64
	latest    map[string]any
	status    map[string]any
	lastError string
	stop      chan struct{}
	done      chan struct{}
}

func NewWaterfallHub(a *App) *WaterfallHub { return &WaterfallHub{app: a} }
func (h *WaterfallHub) Running() bool      { h.mu.Lock(); defer h.mu.Unlock(); return h.running }
func (h *WaterfallHub) Start() error {
	h.mu.Lock()
	if h.running {
		h.mu.Unlock()
		return nil
	}
	h.mu.Unlock()
	h.app.log.Info("WATERFALL", "Waterfall start requested", nil)
	// Enter scanner Waterfall mode using the documented JPM mode used by the
	// existing v0.6.x application. Failure is returned rather than hidden.
	if _, err := h.app.command("JPM,WF_MODE", 2*time.Second); err != nil {
		h.app.log.Error("WATERFALL", "Unable to enter Waterfall mode", map[string]any{"error": err.Error()})
		return err
	}
	gst, err := h.app.command("GST", 1500*time.Millisecond)
	if err != nil {
		_, _ = h.app.command("JPM,SCN_MODE", 2*time.Second)
		return err
	}
	status := parseGST(gst.Raw)
	pwf, err := h.app.command("PWF,1,ON", 1500*time.Millisecond)
	if err != nil {
		_, _ = h.app.command("JPM,SCN_MODE", 2*time.Second)
		return err
	}
	if !strings.Contains(strings.ToUpper(pwf.Raw), "PWF,OK") {
		h.app.log.Warn("WATERFALL", "Unexpected PWF start response", map[string]any{"response": safeRawForLog(pwf.Raw, 300)})
	}
	first, err := h.app.command("GWF,1,ON", 1500*time.Millisecond)
	if err != nil {
		_, _ = h.app.command("GWF,1,OFF", 800*time.Millisecond)
		_, _ = h.app.command("PWF,1,OFF", 800*time.Millisecond)
		_, _ = h.app.command("JPM,SCN_MODE", 2*time.Second)
		return err
	}
	line := parseWaterfallLine(first.Raw)
	if line == nil {
		_, _ = h.app.command("GWF,1,OFF", 800*time.Millisecond)
		_, _ = h.app.command("PWF,1,OFF", 800*time.Millisecond)
		_, _ = h.app.command("JPM,SCN_MODE", 2*time.Second)
		return fmt.Errorf("GWF did not return a valid 240-value text frame")
	}
	h.mu.Lock()
	h.running = true
	h.started = time.Now()
	h.received = 1
	h.latest = line
	h.status = status
	h.lastError = ""
	h.stop = make(chan struct{})
	h.done = make(chan struct{})
	stop := h.stop
	done := h.done
	h.mu.Unlock()
	h.app.log.Info("WATERFALL", "Waterfall running", map[string]any{"values": 240})
	go h.loop(stop, done)
	return nil
}
func (h *WaterfallHub) loop(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	poll := time.NewTicker(250 * time.Millisecond)
	defer poll.Stop()
	gst := time.NewTicker(time.Second)
	defer gst.Stop()
	misses := 0
	for {
		select {
		case <-stop:
			return
		case <-poll.C:
			r, err := h.app.command("GWF,1,ON", 1100*time.Millisecond)
			if err != nil {
				misses++
				h.mu.Lock()
				h.lastError = err.Error()
				h.mu.Unlock()
				h.app.log.Warn("WATERFALL", "GWF poll failed", map[string]any{"consecutive_failures": misses, "error": err.Error()})
				if misses >= 3 {
					h.mu.Lock()
					h.running = false
					h.mu.Unlock()
					h.app.log.Error("WATERFALL", "Waterfall failed after repeated GWF misses", nil)
					return
				}
				continue
			}
			line := parseWaterfallLine(r.Raw)
			if line == nil {
				misses++
				h.mu.Lock()
				h.lastError = "invalid GWF frame"
				h.mu.Unlock()
				continue
			}
			misses = 0
			h.mu.Lock()
			h.latest = line
			h.received++
			h.lastError = ""
			h.mu.Unlock()
		case <-gst.C:
			r, err := h.app.command("GST", 1100*time.Millisecond)
			if err != nil {
				h.app.log.Debug("WATERFALL", "GST refresh failed", map[string]any{"error": err.Error()})
				continue
			}
			h.mu.Lock()
			h.status = parseGST(r.Raw)
			h.mu.Unlock()
		}
	}
}
func (h *WaterfallHub) Stop() map[string]any {
	h.mu.Lock()
	was := h.running
	stop := h.stop
	done := h.done
	h.running = false
	h.mu.Unlock()
	if stop != nil {
		select {
		case <-stop:
		default:
			close(stop)
		}
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(1500 * time.Millisecond):
		}
	}
	_, e1 := h.app.command("GWF,1,OFF", 900*time.Millisecond)
	_, e2 := h.app.command("PWF,1,OFF", 900*time.Millisecond)
	_, e3 := h.app.command("JPM,SCN_MODE", 2*time.Second)
	restored := e3 == nil
	warning := ""
	if e1 != nil || e2 != nil || e3 != nil {
		warning = fmt.Sprintf("cleanup: GWF=%v PWF=%v scan=%v", e1, e2, e3)
	}
	h.mu.Lock()
	h.stop = nil
	h.done = nil
	if warning != "" {
		h.lastError = warning
	}
	h.mu.Unlock()
	h.app.log.Info("WATERFALL", "Waterfall stopped", map[string]any{"was_running": was, "scan_restore": restored, "warning": warning})
	return map[string]any{"running": false, "scan_restore": restored, "warning": warning}
}
func (h *WaterfallHub) Snapshot() map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	up := 0.0
	if !h.started.IsZero() {
		up = time.Since(h.started).Seconds()
	}
	return map[string]any{"running": h.running, "received": h.received, "latest": h.latest, "status": h.status, "last_error": h.lastError, "uptime_s": up}
}
