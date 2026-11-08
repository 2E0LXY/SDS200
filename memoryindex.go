package main

// A searchable index of what is programmed in the scanner.
//
// The scanner can list its memory one level at a time (GLT), but it cannot
// search: finding "Leeds United" means walking favourites → systems →
// departments → channels. That walk is slow over UDP, so it runs in the
// background, is capped, and is cached on disk so a restart is instant.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemEntry is one programmed channel or talkgroup, with the path that leads to
// it so the operator can be told where it lives.
type MemEntry struct {
	Kind        string `json:"kind"` // CFREQ or TGID
	Name        string `json:"name"`
	Index       string `json:"index"`
	Frequency   string `json:"frequency,omitempty"`
	TGID        string `json:"tgid,omitempty"`
	Modulation  string `json:"modulation,omitempty"`
	ServiceType string `json:"service_type,omitempty"`
	Department  string `json:"department,omitempty"`
	DeptIndex   string `json:"department_index,omitempty"`
	System      string `json:"system,omitempty"`
	SystemIndex string `json:"system_index,omitempty"`
	List        string `json:"list,omitempty"`
	ListIndex   string `json:"list_index,omitempty"`
}

// Path is the human-readable location of an entry.
func (e MemEntry) Path() string {
	return strings.Join(nonEmpty(e.List, e.System, e.Department, e.Name), " › ")
}

type MemIndexStatus struct {
	Building     bool      `json:"building"`
	Built        time.Time `json:"built,omitempty"`
	Entries      int       `json:"entries"`
	Systems      int       `json:"systems"`
	Departments  int       `json:"departments"`
	Lists        int       `json:"lists"`
	Partial      bool      `json:"partial"`
	Stopped      string    `json:"stopped,omitempty"`
	Progress     string    `json:"progress,omitempty"`
	FullDatabase bool      `json:"included_full_database"`
	Error        string    `json:"error,omitempty"`
}

type MemIndex struct {
	mu      sync.RWMutex
	app     *App
	path    string
	entries []MemEntry
	status  MemIndexStatus
	cancel  context.CancelFunc
}

type memIndexFile struct {
	Status  MemIndexStatus `json:"status"`
	Entries []MemEntry     `json:"entries"`
}

const (
	memIndexMaxEntries = 40000
	memIndexBudget     = 12 * time.Minute
)

func NewMemIndex(a *App, dir string) *MemIndex {
	m := &MemIndex{app: a, path: filepath.Join(dir, "memory-index.json")}
	m.load()
	return m
}

func (m *MemIndex) load() {
	b, err := os.ReadFile(m.path)
	if err != nil {
		return
	}
	var f memIndexFile
	if json.Unmarshal(b, &f) != nil {
		return
	}
	m.mu.Lock()
	m.entries, m.status = f.Entries, f.Status
	m.status.Building = false
	m.mu.Unlock()
}

func (m *MemIndex) save() {
	m.mu.RLock()
	f := memIndexFile{Status: m.status, Entries: m.entries}
	m.mu.RUnlock()
	_ = saveJSON(m.path, f)
}

func (m *MemIndex) Status() MemIndexStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

func (m *MemIndex) Stop() {
	m.mu.Lock()
	c := m.cancel
	m.mu.Unlock()
	if c != nil {
		c()
	}
}

// Build walks the scanner's memory. It returns at once; progress is reported
// through Status.
func (m *MemIndex) Build(includeFullDatabase bool) error {
	m.mu.Lock()
	if m.status.Building {
		m.mu.Unlock()
		return fmt.Errorf("the index is already being built")
	}
	ctx, cancel := context.WithTimeout(context.Background(), memIndexBudget)
	m.cancel = cancel
	m.status = MemIndexStatus{Building: true, FullDatabase: includeFullDatabase, Progress: "Reading favourites lists…"}
	m.mu.Unlock()
	go func() {
		defer cancel()
		m.build(ctx, includeFullDatabase)
	}()
	return nil
}

func (m *MemIndex) setProgress(s string) {
	m.mu.Lock()
	m.status.Progress = s
	m.mu.Unlock()
}

// records reads one GLT level, tolerating the occasional timeout.
func (m *MemIndex) records(kind, parent string) ([]map[string]any, error) {
	wire := "GLT," + kind
	if parent != "" {
		wire += "," + parent
	}
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		res, err := m.app.command(wire, 4*time.Second)
		if err != nil {
			last = err
			continue
		}
		d, err := xmlRecords(res.Raw)
		if err != nil {
			last = err
			continue
		}
		recs, _ := d["records"].([]map[string]any)
		return recs, nil
	}
	return nil, last
}

func attr(r map[string]any, k string) string {
	if v, ok := r[k]; ok {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}

func (m *MemIndex) build(ctx context.Context, includeFullDatabase bool) {
	started := time.Now()
	out := []MemEntry{}
	stopped := ""
	var lists, systems, departments int

	finish := func(err string) {
		m.mu.Lock()
		m.entries = out
		m.status = MemIndexStatus{
			Built: time.Now(), Entries: len(out), Systems: systems, Departments: departments,
			Lists: lists, Partial: stopped != "", Stopped: stopped, FullDatabase: includeFullDatabase, Error: err,
			Progress: fmt.Sprintf("%d channels from %d departments in %s", len(out), departments, time.Since(started).Round(time.Second)),
		}
		m.mu.Unlock()
		m.save()
		m.app.log.Info("MEMORY", "Index built", map[string]any{"entries": len(out), "systems": systems, "departments": departments, "partial": stopped != "", "elapsed_s": int(time.Since(started).Seconds())})
	}
	done := func() bool {
		select {
		case <-ctx.Done():
			stopped = "time limit reached"
			return true
		default:
		}
		if len(out) >= memIndexMaxEntries {
			stopped = "entry limit reached"
			return true
		}
		return false
	}

	fls, err := m.records("FL", "")
	if err != nil {
		finish("could not read the favourites lists: " + err.Error())
		return
	}
	for _, fl := range fls {
		if done() {
			break
		}
		listName, listIdx := attr(fl, "Name"), attr(fl, "Index")
		// The full UK database holds tens of thousands of channels and takes a
		// very long time to walk, so it is opt-in.
		if !includeFullDatabase && strings.EqualFold(listName, "Full Database") {
			continue
		}
		lists++
		m.setProgress(fmt.Sprintf("Reading %s…", listName))
		syss, err := m.records("SYS", listIdx)
		if err != nil {
			continue
		}
		for _, sys := range syss {
			if done() {
				break
			}
			sysName, sysIdx := attr(sys, "Name"), attr(sys, "Index")
			systems++
			m.setProgress(fmt.Sprintf("%s › %s (%d channels so far)", listName, sysName, len(out)))
			depts, _ := m.records("DEPT", sysIdx)
			for _, dep := range depts {
				if done() {
					break
				}
				depName, depIdx := attr(dep, "Name"), attr(dep, "Index")
				departments++
				base := MemEntry{Department: depName, DeptIndex: depIdx, System: sysName, SystemIndex: sysIdx, List: listName, ListIndex: listIdx}
				if cf, err := m.records("CFREQ", depIdx); err == nil {
					for _, c := range cf {
						e := base
						e.Kind, e.Name, e.Index = "CFREQ", attr(c, "Name"), attr(c, "Index")
						e.Frequency, e.Modulation, e.ServiceType = attr(c, "Freq"), attr(c, "Mod"), attr(c, "SvcType")
						if e.Index != "" {
							out = append(out, e)
						}
					}
				}
				if tg, err := m.records("TGID", depIdx); err == nil {
					for _, c := range tg {
						e := base
						e.Kind, e.Name, e.Index = "TGID", attr(c, "Name"), attr(c, "Index")
						e.TGID, e.ServiceType = attr(c, "TGID"), attr(c, "SvcType")
						if e.Index != "" {
							out = append(out, e)
						}
					}
				}
			}
		}
	}
	finish("")
}

// Search ranks entries against a free-text query: every word must appear
// somewhere in the entry, and a hit on the channel name outranks a hit on the
// system or department.
func (m *MemIndex) Search(q string, limit int) []MemEntry {
	return m.search(q, limit, true)
}

// SearchAny is the fallback for a query where nothing matches every word, as
// happens when the operator names a place the programming spells differently
// ("Elland Road" against a channel called "Leeds Utd Stewards"). Entries that
// match more of the words rank higher.
func (m *MemIndex) SearchAny(q string, limit int) []MemEntry {
	return m.search(q, limit, false)
}

func (m *MemIndex) search(q string, limit int, all bool) []MemEntry {
	words := strings.Fields(strings.ToLower(strings.TrimSpace(q)))
	if len(words) == 0 {
		return nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	// Loose mode still needs most of the words, and only matches them at word
	// boundaries: without that, "road" matches "Broadway" and "ROADPHONE" and
	// the operator is handed nonsense.
	need := len(words)
	if !all {
		need = 1
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	type scored struct {
		e MemEntry
		s int
	}
	hits := []scored{}
	for _, e := range m.entries {
		name := strings.ToLower(e.Name)
		rest := strings.ToLower(strings.Join([]string{e.Department, e.System, e.List, e.ServiceType, e.Frequency, e.TGID}, " "))
		score := 0
		matched := 0
		for _, w := range words {
			switch {
			case wordHit(name, w, all):
				score += 10
				matched++
				if strings.HasPrefix(name, w) {
					score += 5
				}
			case wordHit(rest, w, all):
				score += 3
				matched++
			}
		}
		if matched < need {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(e.Name), strings.TrimSpace(q)) {
			score += 50
		}
		// Matching more of the words beats matching one of them well, which is
		// what decides the order of a loose search.
		score += matched * 100
		hits = append(hits, scored{e, score})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].s != hits[j].s {
			return hits[i].s > hits[j].s
		}
		return hits[i].e.Name < hits[j].e.Name
	})
	out := []MemEntry{}
	for i, h := range hits {
		if i >= limit {
			break
		}
		out = append(out, h.e)
	}
	return out
}

// wordHit reports whether word appears in text. Exact search matches anywhere,
// which is what an operator typing part of a name expects. Loose search needs
// the whole word: it only has to match one word of the query, so "road" must
// not pull in "ROADPHONE" or "Broadway".
func wordHit(text, word string, anywhere bool) bool {
	if anywhere {
		return strings.Contains(text, word)
	}
	for _, tok := range strings.FieldsFunc(text, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	}) {
		if tok == word {
			return true
		}
	}
	return false
}
