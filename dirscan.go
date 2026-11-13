package main

// Scanning a list of directory frequencies.
//
// The scanner scans what is programmed into it, and programming needs the
// microSD card. To listen across frequencies that are only in the directory -
// every channel at an airport, say - this steps the radio through them itself:
// tune one in quick search hold, listen briefly, stay while the squelch is
// open, move on. It is slower than the scanner's own scan (about three
// channels a second) and it holds the radio, so it is for a handful of
// channels, not hundreds.

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"
)

const (
	dirScanMax    = 60                      // channels in one scan
	dirScanDwell  = 300 * time.Millisecond  // listen this long before moving on
	dirScanHang   = 2500 * time.Millisecond // stay this long after a transmission ends
	dirScanPoll   = 250 * time.Millisecond
	dirScanSettle = 120 * time.Millisecond
)

// dirScanChannel is one channel in a scan, with what has been heard on it.
type dirScanChannel struct {
	Name      string    `json:"name"`
	MHz       float64   `json:"mhz"`
	Mode      string    `json:"mode,omitempty"`
	TunedMHz  float64   `json:"tuned_mhz,omitempty"`
	Hits      int       `json:"hits"`
	LastHeard time.Time `json:"last_heard,omitempty"`
}

type DirScan struct {
	mu       sync.Mutex
	app      *App
	cancel   context.CancelFunc
	done     chan struct{}
	running  bool
	label    string
	channels []dirScanChannel
	current  int
	active   bool // squelch open on the current channel
	passes   int
	warning  string
	stopped  string
}

func NewDirScan(a *App) *DirScan { return &DirScan{app: a, current: -1} }

// Start begins scanning the given channels, replacing any scan in progress.
func (s *DirScan) Start(label string, chans []dirScanChannel) error {
	if len(chans) == 0 {
		return &httpError{422, "there are no frequencies to scan"}
	}
	if len(chans) > dirScanMax {
		return &httpError{422, fmt.Sprintf("that is %d frequencies; a directory scan takes at most %d. Narrow the search", len(chans), dirScanMax)}
	}
	for _, c := range chans {
		if _, err := qshUnits(c.MHz); err != nil {
			return err
		}
	}
	s.Stop(false)
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancel, s.done = cancel, make(chan struct{})
	s.running, s.label, s.channels = true, label, append([]dirScanChannel(nil), chans...)
	s.current, s.active, s.passes, s.warning, s.stopped = -1, false, 0, "", ""
	done := s.done
	s.mu.Unlock()
	s.app.log.Info("DIRSCAN", "Directory scan started", map[string]any{"label": label, "channels": len(chans)})
	go func() {
		defer close(done)
		s.run(ctx)
	}()
	return nil
}

// Stop ends the scan. With resume set the scanner goes back to its own scan;
// otherwise it is left where it is, for a caller that is about to retune it.
func (s *DirScan) Stop(resume bool) bool {
	s.mu.Lock()
	cancel, done, was := s.cancel, s.done, s.running
	s.cancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	if was && resume {
		if _, err := s.app.setMode("scan", ""); err != nil {
			s.app.log.Warn("DIRSCAN", "Could not return the scanner to scan mode", map[string]any{"error": err.Error()})
		}
	}
	return was
}

func (s *DirScan) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *DirScan) Status() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]any{
		"running": s.running, "label": s.label, "passes": s.passes,
		"channels": append([]dirScanChannel(nil), s.channels...), "receiving": s.active,
	}
	if s.current >= 0 && s.current < len(s.channels) {
		out["current"] = s.channels[s.current]
	}
	if s.warning != "" {
		out["warning"] = s.warning
	}
	if s.stopped != "" {
		out["stopped"] = s.stopped
	}
	return out
}

func (s *DirScan) finish(reason string) {
	s.mu.Lock()
	s.running, s.active, s.stopped = false, false, reason
	s.mu.Unlock()
	s.app.log.Info("DIRSCAN", "Directory scan ended", map[string]any{"reason": reason})
}

// sleep waits, or returns false if the scan was cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func (s *DirScan) run(ctx context.Context) {
	a := s.app
	a.prepareForWrite()
	failures := 0
	for {
		for i := range s.channels {
			if ctx.Err() != nil {
				s.finish("stopped")
				return
			}
			s.mu.Lock()
			ch := s.channels[i]
			s.current, s.active = i, false
			s.mu.Unlock()

			units, _ := qshUnits(ch.MHz)
			if _, err := a.execWrite(fmt.Sprintf("QSH,%d", units), 2*time.Second); err != nil {
				failures++
				if failures >= 5 {
					s.finish("the scanner stopped accepting tune commands: " + err.Error())
					return
				}
				if !sleepCtx(ctx, 500*time.Millisecond) {
					s.finish("stopped")
					return
				}
				continue
			}
			failures = 0
			if !sleepCtx(ctx, dirScanSettle) {
				s.finish("stopped")
				return
			}
			h, err := a.readSearchHold()
			if err != nil {
				// Something else took the scanner out of quick search: a key
				// on the radio, or another client. Give way to it.
				s.finish("the scanner left quick search, so the scan gave way: " + err.Error())
				return
			}
			// The first time round, put the scanner on the list's modulation.
			// It is sticky, so a list in one mode pays for this once.
			if want := normaliseMode(ch.Mode); want != "" && h.Mod != want {
				if err := a.setSearchModulation(want); err == nil {
					_, _ = a.execWrite(fmt.Sprintf("QSH,%d", units), 2*time.Second)
					sleepCtx(ctx, dirScanSettle)
					h, _ = a.readSearchHold()
				}
			}
			s.mu.Lock()
			s.channels[i].TunedMHz = h.MHz
			if math.Abs(h.MHz-ch.MHz) > 0.0011 && s.warning == "" {
				s.warning = tuneNote(ch.MHz, "", h)
			}
			s.mu.Unlock()

			// Listen. Move on after the dwell if nothing is heard; if the
			// squelch opens, stay until it has been closed for the hang time.
			deadline := time.Now().Add(dirScanDwell)
			heard := false
			for time.Now().Before(deadline) {
				if !sleepCtx(ctx, dirScanPoll) {
					s.finish("stopped")
					return
				}
				h, err := a.readSearchHold()
				if err != nil {
					s.finish("the scanner left quick search, so the scan gave way: " + err.Error())
					return
				}
				if h.Open {
					if !heard {
						heard = true
						s.mu.Lock()
						s.channels[i].Hits++
						s.mu.Unlock()
						a.log.Info("DIRSCAN", "Activity", map[string]any{"name": ch.Name, "mhz": ch.MHz})
					}
					s.mu.Lock()
					s.active = true
					s.channels[i].LastHeard = time.Now()
					s.mu.Unlock()
					deadline = time.Now().Add(dirScanHang)
				} else {
					s.mu.Lock()
					s.active = false
					s.mu.Unlock()
				}
			}
		}
		s.mu.Lock()
		s.passes++
		s.mu.Unlock()
	}
}

// ------------------------------------------------------------- HTTP surface

// directoryChannels turns search hits into scan channels.
func directoryChannels(hits []FreqHit) []dirScanChannel {
	out := make([]dirScanChannel, 0, len(hits))
	seen := map[int]bool{}
	for _, h := range hits {
		u, err := qshUnits(h.MHz)
		if err != nil || seen[u] {
			continue // one channel per frequency, however many names share it
		}
		seen[u] = true
		out = append(out, dirScanChannel{Name: h.Name, MHz: h.MHz, Mode: h.Mode})
	}
	return out
}

// startDirectoryScan scans what a directory search finds.
func (a *App) startDirectoryScan(query string, limit int) (map[string]any, error) {
	if limit <= 0 || limit > dirScanMax {
		limit = dirScanMax
	}
	hits, partial := a.freqDir.Search(query, limit, a.scannerPosition())
	if len(hits) == 0 {
		return nil, &httpError{404, fmt.Sprintf("nothing in the frequency directory matches %q", query)}
	}
	if partial {
		return nil, &httpError{409, fmt.Sprintf("nothing in the directory matches every word of %q, so there is no clear list to scan. Search first and be more specific", query)}
	}
	if err := a.dirScan.Start(query, directoryChannels(hits)); err != nil {
		return nil, err
	}
	time.Sleep(700 * time.Millisecond)
	return a.dirScan.Status(), nil
}

func (a *App) apiDirectoryScan(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		writeJSON(w, 200, a.dirScan.Status())
	case "POST":
		if err := a.requireControl(r); err != nil {
			errJSON(w, err)
			return
		}
		var b struct {
			Query    string           `json:"query"`
			Limit    int              `json:"limit"`
			Channels []dirScanChannel `json:"channels"`
		}
		if err := readJSON(r, &b); err != nil {
			errJSON(w, err)
			return
		}
		if len(b.Channels) > 0 {
			if err := a.dirScan.Start("selected frequencies", b.Channels); err != nil {
				errJSON(w, err)
				return
			}
			time.Sleep(700 * time.Millisecond)
			writeJSON(w, 200, a.dirScan.Status())
			return
		}
		d, err := a.startDirectoryScan(b.Query, b.Limit)
		if err != nil {
			errJSON(w, err)
			return
		}
		writeJSON(w, 200, d)
	case "DELETE":
		if err := a.requireControl(r); err != nil {
			errJSON(w, err)
			return
		}
		was := a.dirScan.Stop(true)
		st := a.dirScan.Status()
		st["was_running"] = was
		writeJSON(w, 200, st)
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		writeJSON(w, 405, map[string]any{"detail": "method not allowed"})
	}
}
