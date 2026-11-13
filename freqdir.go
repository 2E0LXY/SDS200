package main

// The frequency directory: a searchable list of who uses which frequency.
//
// The scanner's own memory only holds what has been programmed into it. A
// directory answers the other question - "what frequency is Leeds Bradford
// Tower on?" - from lists the operator already has: Radio Reference UK
// exports, a live log, a repeater channel list. The assistant and the Search
// page look names up here and then tune or scan what they find.
//
// The lists themselves are not part of this program. They are the operator's
// own exports, usually from a site whose terms do not allow republishing, so
// they are read at run time from <data>/frequencies/*.csv and never embedded
// or committed.
//
// Any CSV with a header row is accepted; columns are found by name:
//
//	name       User, Name, Channel, Description
//	frequency  Frequency, Freq, TX (a repeater's output), RX as a last resort
//	mode       Mode, Modulation
//	and, when present: Licence, Place, County, Postcode, Group, Band, Lat,
//	Long, CTCSS/DCS, Last Heard

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// FreqEntry is one line of the directory.
type FreqEntry struct {
	Name      string  `json:"name"`
	MHz       float64 `json:"mhz"`
	Mode      string  `json:"mode,omitempty"`
	Licence   string  `json:"licence,omitempty"`
	Place     string  `json:"place,omitempty"`
	County    string  `json:"county,omitempty"`
	Postcode  string  `json:"postcode,omitempty"`
	Group     string  `json:"group,omitempty"`
	Tone      string  `json:"tone,omitempty"`
	LastHeard string  `json:"last_heard,omitempty"`
	Note      string  `json:"note,omitempty"`
	Lat       float64 `json:"lat,omitempty"`
	Lon       float64 `json:"lon,omitempty"`
	Source    string  `json:"source,omitempty"`
}

// FreqDir holds every entry from every file in the frequencies folder.
type FreqDir struct {
	mu      sync.RWMutex
	dir     string
	entries []FreqEntry
	files   map[string]int // file name -> entries it contributed
}

func NewFreqDir(dataDir string) *FreqDir {
	d := &FreqDir{dir: filepath.Join(dataDir, "frequencies")}
	_ = os.MkdirAll(d.dir, 0o755)
	d.Reload()
	return d
}

// Reload re-reads every CSV in the folder. Entries that repeat a name on the
// same frequency are kept once; the first file to mention one wins.
func (d *FreqDir) Reload() (entries, files int) {
	names, _ := filepath.Glob(filepath.Join(d.dir, "*.csv"))
	sort.Strings(names)
	all := []FreqEntry{}
	seen := map[string]bool{}
	counts := map[string]int{}
	for _, path := range names {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		es, _ := parseFrequencyCSV(f, filepath.Base(path))
		f.Close()
		n := 0
		for _, e := range es {
			key := fmt.Sprintf("%s|%.5f", strings.ToLower(e.Name), e.MHz)
			if seen[key] {
				continue
			}
			seen[key] = true
			all = append(all, e)
			n++
		}
		counts[filepath.Base(path)] = n
	}
	d.mu.Lock()
	d.entries, d.files = all, counts
	d.mu.Unlock()
	return len(all), len(counts)
}

// Status reports what is loaded.
func (d *FreqDir) Status() map[string]any {
	d.mu.RLock()
	defer d.mu.RUnlock()
	files := make([]map[string]any, 0, len(d.files))
	for name, n := range d.files {
		files = append(files, map[string]any{"file": name, "entries": n})
	}
	sort.Slice(files, func(i, j int) bool { return files[i]["file"].(string) < files[j]["file"].(string) })
	groups := map[string]int{}
	for _, e := range d.entries {
		if e.Group != "" {
			groups[e.Group]++
		}
	}
	return map[string]any{"entries": len(d.entries), "files": files, "groups": groups, "folder": d.dir}
}

// Import saves an uploaded CSV into the folder and reloads. The name is
// reduced to a safe file name; a file that yields no entries is refused, so a
// wrong upload does not sit there silently.
func (d *FreqDir) Import(name string, body []byte) (added int, saved string, err error) {
	es, perr := parseFrequencyCSV(strings.NewReader(string(body)), name)
	if perr != nil {
		return 0, "", &httpError{422, perr.Error()}
	}
	if len(es) == 0 {
		return 0, "", &httpError{422, "no frequencies were found in that file; it needs a header row with a name column and a frequency column"}
	}
	saved = safeCSVName(name)
	if err := os.WriteFile(filepath.Join(d.dir, saved), body, 0o644); err != nil {
		return 0, "", err
	}
	d.Reload()
	d.mu.RLock()
	added = d.files[saved]
	d.mu.RUnlock()
	return added, saved, nil
}

func safeCSVName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.TrimSuffix(strings.TrimSuffix(name, ".csv"), ".CSV")
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '.':
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		out = "frequencies"
	}
	if len(out) > 80 {
		out = out[:80]
	}
	return out + ".csv"
}

// parseFrequencyCSV reads one list. Rows without a usable name and frequency
// are skipped rather than failing the file.
func parseFrequencyCSV(r io.Reader, source string) ([]FreqEntry, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	cr.TrimLeadingSpace = true
	head, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("the file is empty or is not a CSV")
	}
	col := map[string]int{}
	for i, h := range head {
		k := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))
		if _, dup := col[k]; !dup && k != "" {
			col[k] = i
		}
	}
	pick := func(names ...string) int {
		for _, n := range names {
			if i, ok := col[n]; ok {
				return i
			}
		}
		return -1
	}
	cName := pick("user", "name", "channel", "description")
	cFreq := pick("frequency", "freq", "frequency (mhz)", "tx")
	cRX := pick("rx")
	if cFreq < 0 {
		cFreq, cRX = cRX, -1
	}
	if cName < 0 || cFreq < 0 {
		return nil, fmt.Errorf("the file needs a header row with a name column (User, Name or Channel) and a frequency column (Frequency or TX)")
	}
	cMode, cLic := pick("mode", "modulation"), pick("licence", "license", "callsign")
	cPlace, cCounty, cPost := pick("place", "location", "town"), pick("county"), pick("postcode")
	cGroup, cBand := pick("group", "category", "service"), pick("band")
	cLat, cLon := pick("lat", "latitude"), pick("long", "lon", "longitude")
	cTone, cHeard := pick("ctcss/dcs", "ctcss", "tone"), pick("last heard")
	fromTX := col["tx"] == cFreq && cFreq >= 0 && pick("frequency", "freq", "frequency (mhz)") < 0

	get := func(row []string, i int) string {
		if i < 0 || i >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[i])
	}
	out := []FreqEntry{}
	for {
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		mhz, ok := parseDirectoryMHz(get(row, cFreq))
		name := strings.Join(strings.Fields(get(row, cName)), " ")
		if !ok || name == "" {
			continue
		}
		e := FreqEntry{
			Name: name, MHz: mhz, Mode: normaliseMode(get(row, cMode)), Licence: get(row, cLic),
			Place: get(row, cPlace), County: get(row, cCounty), Postcode: get(row, cPost),
			Group: get(row, cGroup), Tone: get(row, cTone), LastHeard: get(row, cHeard), Source: source,
		}
		if v, err := strconv.ParseFloat(get(row, cLat), 64); err == nil {
			e.Lat = v
		}
		if v, err := strconv.ParseFloat(get(row, cLon), 64); err == nil {
			e.Lon = v
		}
		if band := get(row, cBand); band != "" {
			if e.Group == "" {
				e.Group = "Amateur repeaters " + strings.ToLower(band)
			}
		}
		// A repeater list gives the output (TX) and input (RX); the output is
		// the one to listen to, the input is worth knowing.
		if fromTX {
			e.Name += " repeater"
			if in, ok := parseDirectoryMHz(get(row, cRX)); ok && in != mhz {
				e.Note = fmt.Sprintf("repeater output; input %.4f MHz", in)
			}
			if e.Mode == "" {
				e.Mode = "NFM"
			}
		}
		out = append(out, e)
	}
	return out, nil
}

func parseDirectoryMHz(s string) (float64, bool) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.ToUpper(strings.TrimSpace(s)), "MHZ"))
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 25 || f > 1300 {
		return 0, false
	}
	return f, true
}

// normaliseMode maps what lists call a mode onto what the scanner calls it.
func normaliseMode(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "AM":
		return "AM"
	case "NFM", "FMN", "NBFM":
		return "NFM"
	case "FM":
		return "FM"
	case "WFM", "WBFM":
		return "WFM"
	case "FMB":
		return "FMB"
	}
	return ""
}

// distanceMiles is the great-circle distance between two points.
func distanceMiles(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 3958.8
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dp, dl := (lat2-lat1)*math.Pi/180, (lon2-lon1)*math.Pi/180
	a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(a)))
}

// FreqHit is a search result, with the distance from the scanner when both
// positions are known.
type FreqHit struct {
	FreqEntry
	Miles *float64 `json:"miles,omitempty"`
}

// Search finds entries matching every word of the query; if none do, it falls
// back to entries matching any whole word. A query that is a frequency finds
// who is on it. With here set, results near the scanner rank higher and each
// carries its distance.
func (d *FreqDir) Search(q string, limit int, here *[2]float64) (hits []FreqHit, partial bool) {
	if limit <= 0 || limit > 200 {
		limit = 25
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if f, ok := parseDirectoryMHz(q); ok {
		for _, e := range d.entries {
			if math.Abs(e.MHz-f) < 0.0026 {
				hits = append(hits, d.hit(e, here))
			}
		}
		return capHits(hits, limit), false
	}
	words := strings.Fields(strings.ToLower(q))
	if len(words) == 0 {
		return nil, false
	}
	type scored struct {
		h FreqHit
		s float64
	}
	run := func(all bool) []scored {
		out := []scored{}
		for _, e := range d.entries {
			name := strings.ToLower(e.Name)
			rest := strings.ToLower(strings.Join([]string{e.Licence, e.Place, e.County, e.Group, e.Postcode, e.Mode}, " "))
			score, matched := 0.0, 0
			for _, w := range words {
				switch {
				case wordHit(name, w, all):
					score += 10
					matched++
					if strings.HasPrefix(name, w) {
						score += 5
					}
				case wordHit(rest, w, all):
					score += 4
					matched++
				}
			}
			if matched == 0 || (all && matched < len(words)) {
				continue
			}
			score += float64(matched) * 100
			h := d.hit(e, here)
			// Nearer is better, but never enough to outrank a better match.
			if h.Miles != nil {
				score += math.Max(0, 20-*h.Miles/10)
			}
			out = append(out, scored{h, score})
		}
		return out
	}
	res := run(true)
	if len(res) == 0 {
		res, partial = run(false), true
		if len(res) == 0 {
			partial = false
		}
	}
	sort.SliceStable(res, func(i, j int) bool {
		if res[i].s != res[j].s {
			return res[i].s > res[j].s
		}
		return res[i].h.MHz < res[j].h.MHz
	})
	for _, r := range res {
		hits = append(hits, r.h)
	}
	return capHits(hits, limit), partial
}

func capHits(h []FreqHit, limit int) []FreqHit {
	if len(h) > limit {
		return h[:limit]
	}
	return h
}

func (d *FreqDir) hit(e FreqEntry, here *[2]float64) FreqHit {
	h := FreqHit{FreqEntry: e}
	// Lists use one placeholder position for "nationwide"; a distance to it
	// would mean nothing.
	nationwide := strings.EqualFold(e.Place, "Nationwide") || strings.HasPrefix(strings.ToLower(e.Place), "area ")
	if here != nil && (e.Lat != 0 || e.Lon != 0) && !nationwide {
		m := math.Round(distanceMiles(here[0], here[1], e.Lat, e.Lon)*10) / 10
		h.Miles = &m
	}
	return h
}

// ------------------------------------------------------------- HTTP surface

// scannerPosition is the scanner's location, for distances. It is not an
// error for it to be unavailable.
func (a *App) scannerPosition() *[2]float64 {
	lat, lon, _, err := a.location()
	if err != nil || (lat == 0 && lon == 0) {
		return nil
	}
	return &[2]float64{lat, lon}
}

func (a *App) apiFrequencies(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if q == "" {
			writeJSON(w, 200, a.freqDir.Status())
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		hits, partial := a.freqDir.Search(q, limit, a.scannerPosition())
		if hits == nil {
			hits = []FreqHit{}
		}
		writeJSON(w, 200, map[string]any{"query": q, "results": hits, "partial": partial, "directory": a.freqDir.Status()["entries"]})
	case "POST":
		// Import one CSV: the body is the file, ?name= its file name.
		body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
		if err != nil {
			errJSON(w, err)
			return
		}
		added, saved, err := a.freqDir.Import(r.URL.Query().Get("name"), body)
		if err != nil {
			errJSON(w, err)
			return
		}
		a.log.Info("DIRECTORY", "Frequency list imported", map[string]any{"file": saved, "entries": added})
		st := a.freqDir.Status()
		st["imported"], st["added"] = saved, added
		writeJSON(w, 200, st)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeJSON(w, 405, map[string]any{"detail": "method not allowed"})
	}
}
