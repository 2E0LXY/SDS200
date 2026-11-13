package main

import (
	"strings"
	"testing"
)

// Made-up rows in the three layouts the directory has to read.
const (
	rrukCSV = `"","Licence","User","Frequency","Mode","TX/RX","NGR","Lat","Long","Place","Radio Horizon","County","Postcode","CC/RAN/NAC","CTCSS/DCS","Distance","Bearing","Group"
"","EGZZ","Testfield Tower EGZZ 120.305","120.300000","AM","R","SE 1 1","53.8659","-1.6606","Testfield","9.9","West Yorkshire","LS19 7TU","","","-","-","Airports"
"","EGZZ","Testfield Radar EGZZ 134.580","134.575000","AM","R","SE 1 1","53.8659","-1.6606","Testfield","9.9","West Yorkshire","LS19 7TU","","","-","-","Airports"
"","EGTT","London Control Testfield Approach","134.575000","AM","R","","54.0263","-2.55157","Nationwide","16.2","","","","","-","-","Air Control"
"","0000001/1","Example Haulage Limited","453.637500","NFM","R","SN 1 1","52.3192","-4.81888","Area Wales","3.1","","","","","-","-","Nationwide"
"","BAD","No frequency here","","AM","R","","","","","","","","","","-","-","Airports"
`
	logCSV = `"Licence","User","Frequency","Mode","Last Heard"
"EGZZ","Testfield Tower EGZZ 120.305","120.300000","AM","2026-11-13 17:49:18 UTC"
"EGXX","Otherfield Ops","240.650000","AM","2026-11-13 17:10:17 UTC"
`
	repeaterCSV = `"band","channel","tx","rx",
2M,RV48,145.600000,145.000000,
70CM,RB14,433.350000,434.950000,
`
)

func TestParseRadioReferenceExport(t *testing.T) {
	es, err := parseFrequencyCSV(strings.NewReader(rrukCSV), "a.csv")
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 4 {
		t.Fatalf("got %d entries, want 4 (the row without a frequency is skipped)", len(es))
	}
	e := es[0]
	if e.Name != "Testfield Tower EGZZ 120.305" || e.MHz != 120.3 || e.Mode != "AM" || e.Licence != "EGZZ" ||
		e.Place != "Testfield" || e.County != "West Yorkshire" || e.Group != "Airports" || e.Lat != 53.8659 || e.Lon != -1.6606 {
		t.Fatalf("columns misread: %+v", e)
	}
}

func TestParseLiveLogAndRepeaterList(t *testing.T) {
	es, err := parseFrequencyCSV(strings.NewReader(logCSV), "log.csv")
	if err != nil || len(es) != 2 || es[1].LastHeard == "" || es[1].MHz != 240.65 {
		t.Fatalf("live log: %v %+v", err, es)
	}
	rs, err := parseFrequencyCSV(strings.NewReader(repeaterCSV), "rpt.csv")
	if err != nil || len(rs) != 2 {
		t.Fatalf("repeater list: %v %+v", err, rs)
	}
	// The output (TX) is the frequency to listen to; the input is a note.
	r := rs[0]
	if r.MHz != 145.6 || r.Mode != "NFM" || !strings.Contains(r.Name, "RV48") || !strings.Contains(r.Note, "145.0000") || r.Group != "Amateur repeaters 2m" {
		t.Fatalf("repeater row misread: %+v", r)
	}
}

func TestParseRejectsAFileWithNoUsableColumns(t *testing.T) {
	if _, err := parseFrequencyCSV(strings.NewReader("a,b,c\n1,2,3\n"), "x.csv"); err == nil {
		t.Fatal("a CSV with no name or frequency column was accepted")
	}
	if _, err := parseFrequencyCSV(strings.NewReader(""), "x.csv"); err == nil {
		t.Fatal("an empty file was accepted")
	}
}

func testDir(t *testing.T) *FreqDir {
	t.Helper()
	d := NewFreqDir(t.TempDir())
	for name, body := range map[string]string{"a.csv": rrukCSV, "log.csv": logCSV, "rpt.csv": repeaterCSV} {
		if _, _, err := d.Import(name, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func TestDirectoryImportDeduplicatesAcrossFiles(t *testing.T) {
	d := testDir(t)
	// The tower appears in the export and in the live log: one entry.
	if n := d.Status()["entries"].(int); n != 7 {
		t.Fatalf("entries = %d, want 7", n)
	}
	if _, _, err := d.Import("junk.csv", []byte("x,y\n1,2\n")); err == nil {
		t.Fatal("a file with no frequencies was imported")
	}
}

func TestDirectorySearch(t *testing.T) {
	d := testDir(t)
	hits, partial := d.Search("testfield tower", 10, nil)
	if partial || len(hits) != 1 || hits[0].MHz != 120.3 {
		t.Fatalf("name search: partial=%v %+v", partial, hits)
	}
	// "airport" finds the Airports group, which separates the airfield from
	// anything else with the same place name.
	if hits, _ := d.Search("testfield airport", 10, nil); len(hits) != 2 {
		t.Fatalf("group word search returned %d, want the two airport channels", len(hits))
	}
	// A frequency finds who is on it.
	if hits, _ := d.Search("134.575", 10, nil); len(hits) != 2 {
		t.Fatalf("frequency search returned %d, want 2", len(hits))
	}
	// Nothing matches every word: closest whole-word matches, flagged.
	hits, partial = d.Search("testfield helipad", 10, nil)
	if !partial || len(hits) == 0 {
		t.Fatalf("fallback: partial=%v hits=%d", partial, len(hits))
	}
	if hits, partial := d.Search("zzzz", 10, nil); len(hits) != 0 || partial {
		t.Fatalf("no-match search: %v %+v", partial, hits)
	}
}

func TestDirectoryDistanceSkipsNationwidePlaceholders(t *testing.T) {
	d := testDir(t)
	here := &[2]float64{53.7263, -1.5744}
	hits, _ := d.Search("testfield", 10, here)
	var tower, control *FreqHit
	for i := range hits {
		switch {
		case strings.Contains(hits[i].Name, "Tower"):
			tower = &hits[i]
		case strings.Contains(hits[i].Name, "London Control"):
			control = &hits[i]
		}
	}
	if tower == nil || tower.Miles == nil || *tower.Miles < 5 || *tower.Miles > 15 {
		t.Fatalf("tower distance wrong: %+v", tower)
	}
	if control == nil || control.Miles != nil {
		t.Fatalf("a nationwide entry was given a distance: %+v", control)
	}
}

func TestSafeCSVName(t *testing.T) {
	for in, want := range map[string]string{
		"Find UK Scanner Frequencies 1.csv": "Find_UK_Scanner_Frequencies_1.csv",
		`..\..\evil.csv`:                    "evil.csv",
		"../../etc/passwd":                  "passwd.csv",
		"":                                  "frequencies.csv",
	} {
		if got := safeCSVName(in); got != want {
			t.Errorf("safeCSVName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDirectoryChannelsOnePerFrequency(t *testing.T) {
	d := testDir(t)
	hits, _ := d.Search("testfield", 10, nil)
	ch := directoryChannels(hits)
	if len(ch) != 2 {
		t.Fatalf("got %d scan channels, want 2 (two names share 134.575)", len(ch))
	}
}

func TestTuneNote(t *testing.T) {
	// Found on an SDS200E whose airband band default was FMB in 100 kHz steps.
	if n := tuneNote(134.575, "AM", searchHold{MHz: 134.6, Mod: "FMB"}); !strings.Contains(n, "134.60000") || !strings.Contains(n, "Band Defaults") {
		t.Errorf("off-frequency note: %q", n)
	}
	if n := tuneNote(134.575, "AM", searchHold{MHz: 134.575, Mod: "NFM"}); !strings.Contains(n, "NFM") {
		t.Errorf("wrong-mode note: %q", n)
	}
	if n := tuneNote(134.575, "AM", searchHold{MHz: 134.575, Mod: "AM"}); n != "" {
		t.Errorf("a correct tune produced a note: %q", n)
	}
	// 446.00625 is sent as 446.0063; that rounding alone is not a miss.
	if n := tuneNote(446.0063, "", searchHold{MHz: 446.00625, Mod: "NFM"}); n != "" {
		t.Errorf("100 Hz rounding flagged as a miss: %q", n)
	}
}

func TestDirScanRefusesBadLists(t *testing.T) {
	s := NewDirScan(&App{})
	if err := s.Start("x", nil); err == nil {
		t.Error("an empty scan was accepted")
	}
	many := make([]dirScanChannel, dirScanMax+1)
	for i := range many {
		many[i] = dirScanChannel{MHz: 118 + float64(i)*0.025}
	}
	if err := s.Start("x", many); err == nil {
		t.Error("an oversized scan was accepted")
	}
	if err := s.Start("x", []dirScanChannel{{MHz: 5}}); err == nil {
		t.Error("an out-of-range frequency was accepted")
	}
}
