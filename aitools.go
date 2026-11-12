package main

// The scanner operations exposed to the assistant. Every tool is a thin
// wrapper over a verified command (see docs/PROTOCOL.md); nothing here invents
// scanner behaviour, and a tool that cannot be carried out returns an error
// the model can read and explain.

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const aiSystemPrompt = `You control a Uniden SDS200E scanner (firmware 1.23.15) on the operator's local network, through the tools provided. You are talking to a licensed UK amateur radio operator (callsign 2E0LXY), so use UK terms and British English.

Rules:
- Use the tools for anything about the scanner's current state; never guess what it is doing.
- Read the state first when a request depends on it (which channel, which system, what mode).
- Volume is 0-29, squelch 0-19. Frequencies are given in MHz by the operator; tools take MHz unless stated.
- Holds toggle a specific item: hold the channel to stay on it, release to resume scanning.
- To go to a named channel, call find_channel with the name, then go_to_channel with the index it returns. browse_memory walks the tree level by level when you need the structure instead.\n- If find_channel says the memory is not indexed, call index_memory with start=true, tell the operator it is running, and try the search again afterwards.
- The SDS200 has no Service, Range or Squelch-push key. Service Types is func_key with key "Z"; range is set with set_range or set_location_from_address.
- Long presses cannot be sent over the network, so the three-second functions (global attenuator, weather mode, colour mode) are unavailable; say so if asked.
- Waterfall and analyse modes take the radio out of normal scanning. Say what you are about to do, do it, then report what the scanner shows.
- Programming favourites (new systems, departments, channels) needs the microSD card and is not available through these tools yet. Custom search ranges and quick keys are.
- Keep answers short and factual. Give frequencies as 453.7250 MHz. If a tool fails, say what failed and what the scanner reported.

Interpreting the operator:
- "Airband", "aircraft", "ATC" mean the Aircraft service type; civil airband is 118-137 MHz AM, military 225-400 MHz AM.
- "Blue light" means police, fire and ambulance together. In the UK these are mostly encrypted TETRA, so the scanner will hear little; say so rather than pretending.
- "PMR446" is 446.00625-446.19375 MHz NFM; "marine" is 156-162 MHz; "2 metres" is 144-146 MHz and "70 centimetres" 430-440 MHz in the UK.
- Service-type names need not be exact: set_service_types understands everyday words and switches every matching type.
- "Scan range" (set_range) is how far from the scanner's location the database is used; it is not a frequency range. A frequency range to sweep is a custom search.
- An address or postcode means set_location_from_address; the operator rarely knows the coordinates.
- Programmed names are mostly licence holders, so they read as company names: Elland Road is "Leeds United Football Club Limited", the bin lorries are "Leeds City Council". If a place name finds nothing, search again for the organisation that operates it, then the town, then the service; say which name you found before you jump to it.`

// aiTools builds the tool set bound to this App.
func (a *App) aiTools() []AITool {
	return []AITool{
		{
			Name:        "get_status",
			Description: "Current scanner state: mode, system, department, site, channel, frequency, TGID, unit ID, modulation, service type, signal, RSSI, volume, squelch, hold states and the indexes used by other tools.",
			Parameters:  schema(map[string]any{}),
			Run: func(args map[string]any) (any, error) {
				return a.readState(true), nil
			},
		},
		{
			Name:        "get_display",
			Description: "The scanner's screen as text, line by line, exactly as shown on the radio. Use this for menus and for anything the state does not cover.",
			Parameters:  schema(map[string]any{}),
			Run: func(args map[string]any) (any, error) {
				res, err := a.command("STS", 1500*time.Millisecond)
				if err != nil {
					return nil, err
				}
				d := parseSTS(res.Raw, res.ElapsedMS)
				lines := []string{}
				for _, l := range d.Lines {
					if strings.TrimSpace(l.Text) != "" {
						lines = append(lines, strings.TrimSpace(l.Text))
					}
				}
				return map[string]any{"lines": lines}, nil
			},
		},
		{
			Name:        "set_volume",
			Description: "Set the scanner's volume, 0 to 29.",
			Parameters:  schema(map[string]any{"level": intProp("Volume level 0-29", 0, 29)}, "level"),
			Writes:      true,
			Run: func(args map[string]any) (any, error) {
				n, ok := argInt(args, "level")
				if !ok || n < 0 || n > 29 {
					return nil, fmt.Errorf("volume must be 0-29")
				}
				if _, err := a.execWrite(fmt.Sprintf("VOL,%d", n), 1250*time.Millisecond); err != nil {
					return nil, err
				}
				a.invalidateState()
				return map[string]any{"volume": n}, nil
			},
		},
		{
			Name:        "set_squelch",
			Description: "Set the squelch, 0 (open) to 19 (tight).",
			Parameters:  schema(map[string]any{"level": intProp("Squelch level 0-19", 0, 19)}, "level"),
			Writes:      true,
			Run: func(args map[string]any) (any, error) {
				n, ok := argInt(args, "level")
				if !ok || n < 0 || n > 19 {
					return nil, fmt.Errorf("squelch must be 0-19")
				}
				if _, err := a.execWrite(fmt.Sprintf("SQL,%d", n), 1250*time.Millisecond); err != nil {
					return nil, err
				}
				a.invalidateState()
				return map[string]any{"squelch": n}, nil
			},
		},
		{
			Name:        "press_key",
			Description: "Press one front-panel key. Valid: M (menu/back), F (function), E (enter/yes), . (decimal/no), 0-9, < > (scroll left/right), ^ (scroll push), A B C (soft keys: system, department, channel), L (avoid), Y (replay), V (backlight), Z (zip/location).",
			Parameters:  schema(map[string]any{"key": strProp("Key code")}, "key"),
			Writes:      true,
			Run: func(args map[string]any) (any, error) {
				k := argString(args, "key")
				if k != "." {
					k = strings.ToUpper(k)
				}
				if _, ok := keyCodes[k]; !ok {
					return nil, fmt.Errorf("key %q is not an SDS200 key", k)
				}
				if _, err := a.execWrite("KEY,"+k+",P", 1250*time.Millisecond); err != nil {
					return nil, err
				}
				a.invalidateState()
				time.Sleep(250 * time.Millisecond)
				return a.displaySnapshot()
			},
		},
		{
			Name:        "func_key",
			Description: "Press FUNC followed by a key: Z = Service Types, 1-3 = search keys, 4 = channel attenuator, 5 = dimmer, 6 = weather priority, 7 = IF exchange, 8 = repeater reverse, 9 = simple/detail display, 0 = channel volume offset, . = priority, E = quick search / ID search.",
			Parameters:  schema(map[string]any{"key": strProp("Key pressed after FUNC")}, "key"),
			Writes:      true,
			Run: func(args map[string]any) (any, error) {
				k := argString(args, "key")
				if k != "." {
					k = strings.ToUpper(k)
				}
				if _, ok := keyCodes[k]; !ok || k == "F" {
					return nil, fmt.Errorf("key %q cannot follow FUNC", k)
				}
				if err := a.funcSequence(k); err != nil {
					return nil, err
				}
				return a.displaySnapshot()
			},
		},
		{
			Name:        "set_hold",
			Description: "Hold or release the current system, department, site or channel. Holding a channel stops scanning and stays on it.",
			Parameters:  schema(map[string]any{"scope": strProp("What to hold", "system", "department", "site", "channel"), "hold": boolProp("true to hold, false to resume scanning")}, "scope", "hold"),
			Writes:      true,
			Run: func(args map[string]any) (any, error) {
				scope := strings.ToLower(argString(args, "scope"))
				want, ok := argBool(args, "hold")
				if !ok {
					return nil, fmt.Errorf("hold must be true or false")
				}
				st, err := a.setHoldState(scope, want)
				if err != nil {
					return nil, err
				}
				return st, nil
			},
		},
		{
			Name:        "step_channel",
			Description: "Move to the next or previous channel in the current list.",
			Parameters:  schema(map[string]any{"direction": strProp("next or previous", "next", "previous")}, "direction"),
			Writes:      true,
			Run: func(args map[string]any) (any, error) {
				dir := strings.ToLower(argString(args, "direction"))
				if dir != "next" && dir != "previous" {
					return nil, fmt.Errorf("direction must be next or previous")
				}
				if err := a.stepChannel(dir == "next"); err != nil {
					return nil, err
				}
				time.Sleep(400 * time.Millisecond)
				return a.readState(true), nil
			},
		},
		{
			Name:        "avoid_current",
			Description: "Avoid the current channel. One press is a temporary avoid, two presses makes it permanent.",
			Parameters:  schema(map[string]any{"permanent": boolProp("true to press twice for a permanent avoid")}),
			Writes:      true,
			Run: func(args map[string]any) (any, error) {
				n := 1
				if p, ok := argBool(args, "permanent"); ok && p {
					n = 2
				}
				for i := 0; i < n; i++ {
					if _, err := a.execWrite("KEY,L,P", 1250*time.Millisecond); err != nil {
						return nil, err
					}
					time.Sleep(300 * time.Millisecond)
				}
				a.invalidateState()
				return map[string]any{"presses": n}, nil
			},
		},
		{
			Name:        "set_mode",
			Description: "Switch the scanner's operating mode: scan, close_call, weather, tone_out, custom_search, quick_search, waterfall, conventional_discovery or trunking_discovery.",
			Parameters: schema(map[string]any{
				"mode":  strProp("Mode to enter", "scan", "close_call", "weather", "tone_out", "custom_search", "quick_search", "waterfall", "conventional_discovery", "trunking_discovery"),
				"index": strProp("Optional extra value: weather takes NORMAL, A_ONLY, SAME_1..SAME_5 or ALL_FIPS; discovery takes a session name"),
			}, "mode"),
			Writes: true,
			Run: func(args map[string]any) (any, error) {
				return a.setMode(argString(args, "mode"), argString(args, "index"))
			},
		},
		{
			Name:        "tune_frequency",
			Description: "Tune straight to a frequency in quick search hold, so the radio listens on it. Takes MHz, e.g. 446.00625.",
			Parameters:  schema(map[string]any{"mhz": numProp("Frequency in MHz, 25 to 1300")}, "mhz"),
			Writes:      true,
			Run: func(args map[string]any) (any, error) {
				f, ok := argFloat(args, "mhz")
				if !ok {
					return nil, fmt.Errorf("mhz is required")
				}
				return a.tuneFrequency(f)
			},
		},
		{
			Name:        "get_service_types",
			Description: "Which of the 37 preset and 10 custom service types are being scanned.",
			Parameters:  schema(map[string]any{}),
			Run: func(args map[string]any) (any, error) {
				return a.serviceTypes()
			},
		},
		{
			Name:        "set_service_types",
			Description: "Turn service types on or off. Everyday words work: aircraft/airband, police/law, fire, ambulance/EMS, rail, military, ham, utilities, council, media, schools, security, hospital, business, transport, federal, corrections, interop, emergency ops, other, or 'custom 1'-'custom 10'. A word that covers several types (police, fire, EMS) switches all of them.",
			Parameters: schema(map[string]any{
				"enable":  map[string]any{"type": "array", "description": "Service types to switch on", "items": map[string]any{"type": "string"}},
				"disable": map[string]any{"type": "array", "description": "Service types to switch off", "items": map[string]any{"type": "string"}},
			}),
			Writes: true,
			Run: func(args map[string]any) (any, error) {
				cur, err := a.serviceTypes()
				if err != nil {
					return nil, err
				}
				states := make([]int, 47)
				for i, it := range cur {
					if it.Enabled {
						states[i] = 1
					}
				}
				changed := map[string][]string{"enabled": {}, "disabled": {}}
				apply := func(key string, on int, bucket string) error {
					list, _ := args[key].([]any)
					for _, v := range list {
						want := fmt.Sprint(v)
						idxs, err := matchServiceTypes(want, cur)
						if err != nil {
							return err
						}
						for _, i := range idxs {
							states[i] = on
							changed[bucket] = append(changed[bucket], cur[i].Name)
						}
					}
					return nil
				}
				if err := apply("enable", 1, "enabled"); err != nil {
					return nil, err
				}
				if err := apply("disable", 0, "disabled"); err != nil {
					return nil, err
				}
				if _, err := a.execWrite("SVC,"+joinInts(states), 2*time.Second); err != nil {
					return nil, err
				}
				after, err := a.serviceTypes()
				if err != nil {
					return nil, err
				}
				on := []string{}
				for _, it := range after {
					if it.Enabled {
						on = append(on, it.Name)
					}
				}
				return map[string]any{"enabled": changed["enabled"], "disabled": changed["disabled"], "now_scanning": on}, nil
			},
		},
		{
			Name:        "get_quick_keys",
			Description: "Favourites quick keys 00-99 and whether each exists and is enabled.",
			Parameters:  schema(map[string]any{}),
			Run: func(args map[string]any) (any, error) {
				set, err := a.quickKeys("fqk", 0, 0)
				if err != nil {
					return nil, err
				}
				out := []map[string]any{}
				for i, n := range set.States {
					if n != 0 {
						out = append(out, map[string]any{"key": fmt.Sprintf("%02d", i), "enabled": n == 2})
					}
				}
				return map[string]any{"assigned": out}, nil
			},
		},
		{
			Name:        "set_quick_key",
			Description: "Enable or disable one favourites quick key (00-99). Only keys that already exist can be changed.",
			Parameters:  schema(map[string]any{"key": intProp("Quick key number 0-99", 0, 99), "enabled": boolProp("true to scan this list, false to skip it")}, "key", "enabled"),
			Writes:      true,
			Run: func(args map[string]any) (any, error) {
				k, ok := argInt(args, "key")
				on, ok2 := argBool(args, "enabled")
				if !ok || !ok2 {
					return nil, fmt.Errorf("key must be 0-99 and enabled true/false")
				}
				if err := a.setFavouriteQuickKey(k, on); err != nil {
					return nil, err
				}
				return map[string]any{"key": fmt.Sprintf("%02d", k), "enabled": on}, nil
			},
		},
		{
			Name:        "browse_memory",
			Description: "List what is programmed in the scanner: favourites lists (FL), systems (SYS, parent = favourites index), departments (DEPT) or sites (SITE) (parent = system index), conventional channels (CFREQ) or talkgroups (TGID) (parent = department index).",
			Parameters:  schema(map[string]any{"kind": strProp("List to read", "FL", "SYS", "DEPT", "SITE", "CFREQ", "TGID"), "parent": strProp("Parent index, required for everything except FL")}, "kind"),
			Run: func(args map[string]any) (any, error) {
				kind := strings.ToUpper(argString(args, "kind"))
				parent := argString(args, "parent")
				if !map[string]bool{"FL": true, "SYS": true, "DEPT": true, "SITE": true, "CFREQ": true, "TGID": true}[kind] {
					return nil, fmt.Errorf("kind must be FL, SYS, DEPT, SITE, CFREQ or TGID")
				}
				wire := "GLT," + kind
				if kind != "FL" {
					if parent == "" {
						return nil, fmt.Errorf("%s needs a parent index", kind)
					}
					wire += "," + parent
				}
				res, err := a.command(wire, 3*time.Second)
				if err != nil {
					return nil, err
				}
				d, err := xmlRecords(res.Raw)
				if err != nil {
					return nil, err
				}
				delete(d, "raw")
				return d, nil
			},
		},
		{
			Name:        "get_location_range",
			Description: "The scanner's location (latitude and longitude) and the scan range in miles.",
			Parameters:  schema(map[string]any{}),
			Run: func(args map[string]any) (any, error) {
				lat, lon, rng, err := a.location()
				if err != nil {
					return nil, err
				}
				return map[string]any{"latitude": lat, "longitude": lon, "range_miles": rng}, nil
			},
		},
		{
			Name:        "set_range",
			Description: "Set how far from the scanner's location it scans, in miles (the Range setting). Optionally move the location too.",
			Parameters:  schema(map[string]any{"range_miles": numProp("Range in miles, 0-999"), "latitude": numProp("Optional new latitude in degrees"), "longitude": numProp("Optional new longitude in degrees")}, "range_miles"),
			Writes:      true,
			Run: func(args map[string]any) (any, error) {
				rng, ok := argFloat(args, "range_miles")
				if !ok || rng < 0 || rng > 999 {
					return nil, fmt.Errorf("range must be 0-999 miles")
				}
				lat, lon, _, err := a.location()
				if err != nil {
					return nil, err
				}
				if v, ok := argFloat(args, "latitude"); ok {
					lat = v
				}
				if v, ok := argFloat(args, "longitude"); ok {
					lon = v
				}
				wire := fmt.Sprintf("LCR,%.6f,%.6f,%.1f", lat, lon, rng)
				if _, err := a.execWrite(wire, 2*time.Second); err != nil {
					return nil, err
				}
				return map[string]any{"latitude": lat, "longitude": lon, "range_miles": rng}, nil
			},
		},
		{
			Name:        "find_channel",
			Description: "Search everything programmed in the scanner by name, e.g. 'Leeds United', 'Elland Road', 'airport', 'fire control'. Returns matching channels and talkgroups with the index that go_to_channel needs. Use this before tune_frequency when the operator names a place or service rather than a frequency.",
			Parameters:  schema(map[string]any{"query": strProp("What to look for"), "limit": intProp("How many results, default 10", 1, 50)}, "query"),
			Run: func(args map[string]any) (any, error) {
				q := argString(args, "query")
				if q == "" {
					return nil, fmt.Errorf("query is required")
				}
				limit, ok := argInt(args, "limit")
				if !ok {
					limit = 10
				}
				st := a.memIndex.Status()
				res := a.memIndex.Search(q, limit)
				loose := false
				if len(res) == 0 {
					// Nothing matches every word; fall back to the best partial
					// matches, because the programming rarely spells a place the
					// way the operator says it.
					if res = a.memIndex.SearchAny(q, limit); len(res) > 0 {
						loose = true
					}
				}
				out := make([]map[string]any, 0, len(res))
				for _, e := range res {
					out = append(out, map[string]any{"kind": e.Kind, "index": e.Index, "name": e.Name, "frequency": e.Frequency, "tgid": e.TGID, "modulation": e.Modulation, "path": e.Path()})
				}
				if len(out) == 0 {
					if st.Entries == 0 {
						return nil, fmt.Errorf("the scanner's memory has not been indexed yet; call index_memory first, then search again")
					}
					note := "nothing programmed matches that name"
					if !st.FullDatabase {
						note += "; only the favourites lists are indexed, so try index_memory with include_full_database true, or ask the operator for the frequency"
					}
					return map[string]any{"results": out, "searched": st.Entries, "note": note}, nil
				}
				res2 := map[string]any{"results": out, "searched": st.Entries, "indexed_at": st.Built}
				if loose {
					res2["note"] = "no entry matches every word, so these are the closest partial matches; check the names before jumping"
				}
				return res2, nil
			},
		},
		{
			Name:        "index_memory",
			Description: "Read everything programmed in the scanner into a searchable index, so find_channel works. Takes a few minutes and runs in the background; call again with no arguments to see progress. The full UK database is skipped unless include_full_database is true, because it is very large.",
			Parameters:  schema(map[string]any{"start": boolProp("true to start a build; omit to just report progress"), "include_full_database": boolProp("Also walk the full database (slow)")}),
			Writes:      true,
			Run: func(args map[string]any) (any, error) {
				if start, ok := argBool(args, "start"); ok && start {
					full, _ := argBool(args, "include_full_database")
					if err := a.memIndex.Build(full); err != nil {
						return nil, err
					}
					time.Sleep(600 * time.Millisecond)
				}
				return a.memIndex.Status(), nil
			},
		},
		{
			Name:        "go_to_channel",
			Description: "Jump straight to a programmed item and hold it. Use browse_memory first to get the index. Kind is CFREQ (conventional channel), TGID (talkgroup), DEPT, SITE or SYS.",
			Parameters:  schema(map[string]any{"kind": strProp("Item kind", "CFREQ", "TGID", "DEPT", "SITE", "SYS"), "index": strProp("The item's index from browse_memory")}, "kind", "index"),
			Writes:      true,
			Run: func(args map[string]any) (any, error) {
				kind := strings.ToUpper(argString(args, "kind"))
				idx := argString(args, "index")
				if !map[string]bool{"CFREQ": true, "TGID": true, "DEPT": true, "SITE": true, "SYS": true}[kind] || idx == "" {
					return nil, fmt.Errorf("kind must be CFREQ, TGID, DEPT, SITE or SYS, with an index")
				}
				if _, err := a.execWrite(fmt.Sprintf("HLD,%s,%s,", kind, idx), 2*time.Second); err != nil {
					return nil, err
				}
				a.invalidateState()
				time.Sleep(500 * time.Millisecond)
				return a.readState(true), nil
			},
		},
		{
			Name:        "set_location_from_address",
			Description: "Set the scanner's location from a UK postcode, optionally with a house number for a more exact position, and optionally the scan range in miles. Use this when the operator gives an address rather than coordinates.",
			Parameters:  schema(map[string]any{"postcode": strProp("UK postcode, e.g. WF3 1AB"), "house_number": strProp("Optional house number or name"), "range_miles": numProp("Optional scan range in miles, 0-999")}, "postcode"),
			Writes:      true,
			Run: func(args map[string]any) (any, error) {
				geo, err := newGeocoder(a.log).Lookup(context.Background(), argString(args, "house_number"), argString(args, "postcode"))
				if err != nil {
					return nil, err
				}
				_, _, rng, err := a.location()
				if err != nil {
					return nil, err
				}
				if v, ok := argFloat(args, "range_miles"); ok {
					if v < 0 || v > 999 {
						return nil, fmt.Errorf("range must be 0-999 miles")
					}
					rng = v
				}
				wire := fmt.Sprintf("LCR,%.6f,%.6f,%.1f", geo.Latitude, geo.Longitude, rng)
				if _, err := a.execWrite(wire, 2*time.Second); err != nil {
					return nil, err
				}
				return map[string]any{"latitude": geo.Latitude, "longitude": geo.Longitude, "range_miles": rng, "matched": geo.Label, "precision": geo.Precision, "source": geo.Source}, nil
			},
		},
		{
			Name:        "get_clock",
			Description: "The scanner's clock and how far it differs from this computer.",
			Parameters:  schema(map[string]any{}),
			Run: func(args map[string]any) (any, error) {
				res, err := a.command("DTM", 1500*time.Millisecond)
				if err != nil {
					return nil, err
				}
				f := packetFields(res.Raw, "DTM")
				if len(f) < 7 {
					return nil, fmt.Errorf("unexpected DTM reply")
				}
				n := make([]int, 7)
				for i := 0; i < 7; i++ {
					n[i], _ = strconv.Atoi(strings.TrimSpace(f[i]))
				}
				t := time.Date(n[1], time.Month(n[2]), n[3], n[4], n[5], n[6], 0, time.Local)
				return map[string]any{"scanner_time": t.Format("2006-01-02 15:04:05"), "offset_seconds": int(time.Until(t).Seconds())}, nil
			},
		},
		{
			Name:        "sync_clock",
			Description: "Set the scanner's clock to this computer's time.",
			Parameters:  schema(map[string]any{}),
			Writes:      true,
			Run: func(args map[string]any) (any, error) {
				now := time.Now()
				wire := fmt.Sprintf("DTM,0,%d,%d,%d,%d,%d,%d", now.Year(), int(now.Month()), now.Day(), now.Hour(), now.Minute(), now.Second())
				if _, err := a.execWrite(wire, 1500*time.Millisecond); err != nil {
					return nil, err
				}
				return map[string]any{"set_to": now.Format("2006-01-02 15:04:05")}, nil
			},
		},
		{
			Name:        "waterfall",
			Description: "Start or stop the waterfall. Starting puts the radio into waterfall mode; stopping returns it to scanning. While running, the latest 240-point spectrum frame and the centre frequency are returned.",
			Parameters:  schema(map[string]any{"action": strProp("start, stop or status", "start", "stop", "status")}, "action"),
			Writes:      true,
			Run: func(args map[string]any) (any, error) {
				switch strings.ToLower(argString(args, "action")) {
				case "start":
					if err := a.waterfall.Start(); err != nil {
						return nil, err
					}
					time.Sleep(700 * time.Millisecond)
				case "stop":
					return a.waterfall.Stop(), nil
				case "status":
				default:
					return nil, fmt.Errorf("action must be start, stop or status")
				}
				s := a.waterfall.Snapshot()
				delete(s, "status")
				return s, nil
			},
		},
		{
			Name:        "live_audio",
			Description: "Start or stop streaming the scanner's audio to the web app, or report whether it is streaming. The scanner allows only one audio session.",
			Parameters:  schema(map[string]any{"action": strProp("start, stop or status", "start", "stop", "status")}, "action"),
			Writes:      true,
			Run: func(args map[string]any) (any, error) {
				switch strings.ToLower(argString(args, "action")) {
				case "start":
					return a.audio.Acquire("web")
				case "stop":
					a.audio.Release("web")
					return a.audio.Status(), nil
				case "status", "":
					return a.audio.Status(), nil
				}
				return nil, fmt.Errorf("action must be start, stop or status")
			},
		},
		{
			Name:        "scanner_recording",
			Description: "Start or stop recording on the scanner's own microSD card.",
			Parameters:  schema(map[string]any{"record": boolProp("true to start, false to stop")}, "record"),
			Writes:      true,
			Run: func(args map[string]any) (any, error) {
				on, ok := argBool(args, "record")
				if !ok {
					return nil, fmt.Errorf("record must be true or false")
				}
				v := 0
				if on {
					v = 1
				}
				d, err := a.execWrite(fmt.Sprintf("URC,%d", v), 1500*time.Millisecond)
				if err != nil {
					return nil, err
				}
				return d, nil
			},
		},
		{
			Name:        "open_menu",
			Description: "Open one of the scanner's menus and read it: TOP, MONITOR_LIST, SRCH_RANGE (custom search bank, needs index), SRCH_OPT, CC, CC_BAND, WX, SETTINGS, SCAN_SYSTEM/SCAN_DEPARTMENT/SCAN_SITE/SCAN_CHANNEL (need an index), FTO_CHANNEL.",
			Parameters:  schema(map[string]any{"menu": strProp("Menu id"), "index": strProp("Index where the menu needs one")}, "menu"),
			Writes:      true,
			Run: func(args map[string]any) (any, error) {
				id := strings.ToUpper(argString(args, "menu"))
				if !map[string]bool{"TOP": true, "MONITOR_LIST": true, "SCAN_SYSTEM": true, "SCAN_DEPARTMENT": true, "SCAN_SITE": true, "SCAN_CHANNEL": true, "SRCH_RANGE": true, "SRCH_OPT": true, "CC": true, "CC_BAND": true, "WX": true, "FTO_CHANNEL": true, "SETTINGS": true, "BRDCST_SCREEN": true}[id] {
					return nil, fmt.Errorf("unknown menu id %q", id)
				}
				if _, err := a.execWrite("MNU,"+id+","+argString(args, "index"), 2500*time.Millisecond); err != nil {
					return nil, err
				}
				time.Sleep(300 * time.Millisecond)
				return a.menuSnapshot()
			},
		},
		{
			Name:        "read_menu",
			Description: "Read the menu currently on screen: its title, the items and the value of each.",
			Parameters:  schema(map[string]any{}),
			Run: func(args map[string]any) (any, error) {
				return a.menuSnapshot()
			},
		},
		{
			Name:        "set_menu_value",
			Description: "Open or set a menu item: the item's index for a selection menu (read_menu lists them), or the text for an input menu. Read the menu first.",
			Parameters:  schema(map[string]any{"value": strProp("Option index, or text to enter")}, "value"),
			Writes:      true,
			Run: func(args map[string]any) (any, error) {
				v := argString(args, "value")
				if v == "" {
					return nil, fmt.Errorf("value is required")
				}
				if _, err := a.execWrite("MSV,,"+strings.ReplaceAll(v, ",", "\t"), 2500*time.Millisecond); err != nil {
					return nil, err
				}
				time.Sleep(300 * time.Millisecond)
				return a.menuSnapshot()
			},
		},
		{
			Name:        "close_menu",
			Description: "Leave the menu: one level back, or all the way out to normal operation.",
			Parameters:  schema(map[string]any{"exit": boolProp("true to leave menu mode entirely, false for one level back")}),
			Writes:      true,
			Run: func(args map[string]any) (any, error) {
				level := ""
				if e, ok := argBool(args, "exit"); ok && e {
					level = "RETURN_PREVOUS_MODE"
				}
				if _, err := a.execWrite("MSB,,"+level, 2500*time.Millisecond); err != nil {
					return nil, err
				}
				a.invalidateState()
				time.Sleep(300 * time.Millisecond)
				return a.displaySnapshot()
			},
		},
	}
}

// ------------------------------------------------------------- shared logic
// These helpers are used by both the AI tools and the HTTP handlers so the two
// paths cannot drift apart.

type serviceType struct {
	Slot    int    `json:"slot"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Custom  bool   `json:"custom"`
}

func (a *App) serviceTypes() ([]serviceType, error) {
	res, err := a.command("SVC", 1500*time.Millisecond)
	if err != nil {
		return nil, err
	}
	f := packetFields(res.Raw, "SVC")
	if len(f) < 47 {
		return nil, fmt.Errorf("SVC returned %d fields, expected 47", len(f))
	}
	out := make([]serviceType, 0, 47)
	for i := 0; i < 47; i++ {
		name := ""
		if i < 37 {
			name = presetServiceTypes[i]
		} else {
			name = fmt.Sprintf("Custom %d", i-36)
		}
		out = append(out, serviceType{Slot: i, Name: name, Enabled: strings.TrimSpace(f[i]) == "1", Custom: i >= 37})
	}
	return out, nil
}

func (a *App) location() (lat, lon, rng float64, err error) {
	res, err := a.command("LCR", 2500*time.Millisecond)
	if err != nil {
		return 0, 0, 0, err
	}
	f := packetFields(res.Raw, "LCR")
	if len(f) < 3 {
		return 0, 0, 0, fmt.Errorf("unexpected LCR reply")
	}
	lat, _ = strconv.ParseFloat(strings.TrimSpace(f[0]), 64)
	lon, _ = strconv.ParseFloat(strings.TrimSpace(f[1]), 64)
	rng, _ = strconv.ParseFloat(strings.TrimSpace(f[2]), 64)
	return lat, lon, rng, nil
}

func (a *App) displaySnapshot() (map[string]any, error) {
	res, err := a.command("STS", 1500*time.Millisecond)
	if err != nil {
		return nil, err
	}
	d := parseSTS(res.Raw, res.ElapsedMS)
	lines := []string{}
	for _, l := range d.Lines {
		if strings.TrimSpace(l.Text) != "" {
			lines = append(lines, strings.TrimSpace(l.Text))
		}
	}
	return map[string]any{"lines": lines}, nil
}

func (a *App) menuSnapshot() (map[string]any, error) {
	res, err := a.command("MSI", 2500*time.Millisecond)
	if err != nil {
		return nil, err
	}
	d, err := xmlRecords(res.Raw)
	if err != nil {
		return nil, err
	}
	delete(d, "raw")
	return d, nil
}

// funcSequence presses FUNC then key, waiting out any popup first and skipping
// the FUNC press when the modifier is already active.
func (a *App) funcSequence(key string) error {
	for i := 0; i < 15; i++ {
		res, err := a.command("GSI", 2500*time.Millisecond)
		if err != nil {
			return err
		}
		if !strings.Contains(res.Raw, "<PopupScreen") {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	on, err := a.funcActive()
	if err != nil {
		return err
	}
	seq := []string{key}
	if !on {
		seq = []string{"F", key}
	}
	if err := a.pressKeys(seq); err != nil {
		return err
	}
	a.invalidateState()
	return nil
}

// stepChannel moves one channel using the live index, falling back to the
// scroll wheel in modes that have no channel index.
func (a *App) stepChannel(forward bool) error {
	st := a.readState(true)
	cmd := "PRV"
	if forward {
		cmd = "NXT"
	}
	tkw := map[string]string{"ConvFrequency": "CFREQ", "TGID": "TGID", "CcHitsChannel": "CCHIT", "WxChannel": "WX", "ToneOutChannel": "FTO"}[fmt.Sprint(st["channel_kind"])]
	if idx, ok := st["channel_index"].(*int); ok && idx != nil && tkw != "" {
		if _, err := a.execWrite(fmt.Sprintf("%s,%s,%d,,1", cmd, tkw, *idx), 1500*time.Millisecond); err == nil {
			a.invalidateState()
			return nil
		}
	}
	k := "<"
	if forward {
		k = ">"
	}
	_, err := a.execWrite("KEY,"+k+",P", 1250*time.Millisecond)
	a.invalidateState()
	return err
}

// setHoldState holds or releases a scope and waits for the scanner to confirm.
func (a *App) setHoldState(scope string, want bool) (map[string]any, error) {
	if !map[string]bool{"system": true, "department": true, "site": true, "channel": true}[scope] {
		return nil, fmt.Errorf("scope must be system, department, site or channel")
	}
	before := a.readState(true)
	if cur := heldValue(before[scope+"_hold"]); cur != nil && *cur == want {
		return map[string]any{"scope": scope, "hold": want, "changed": false}, nil
	}
	wire := holdWire(scope, before)
	if wire != "" {
		if _, err := a.execWrite(wire, 1500*time.Millisecond); err != nil {
			return nil, err
		}
	} else {
		keys := map[string][]string{"system": {"A"}, "department": {"B"}, "site": {"F", "B"}, "channel": {"C"}}[scope]
		if err := a.pressKeys(keys); err != nil {
			return nil, err
		}
	}
	deadline := time.Now().Add(3500 * time.Millisecond)
	for {
		time.Sleep(200 * time.Millisecond)
		a.invalidateState()
		st := a.readState(true)
		if v := heldValue(st[scope+"_hold"]); v != nil && *v == want {
			return map[string]any{"scope": scope, "hold": want, "changed": true, "channel": st["channel"], "system": st["system"]}, nil
		}
		if time.Now().After(deadline) {
			state := "not reported in this mode"
			if v := heldValue(st[scope+"_hold"]); v != nil {
				state = map[bool]string{true: "held", false: "scanning"}[*v]
			}
			return nil, fmt.Errorf("%s hold unchanged — scanner still %s", scope, state)
		}
	}
}

// serviceTypeAliases maps everyday words to the scanner's service-type names.
// A word may cover several types: "police" switches all three Law types.
var serviceTypeAliases = map[string][]string{
	"aircraft": {"Aircraft"}, "air": {"Aircraft"}, "airband": {"Aircraft"}, "aviation": {"Aircraft"},
	"atc": {"Aircraft"}, "civilian aircraft": {"Aircraft"}, "military aircraft": {"Aircraft", "Military"},
	"police": {"Law Dispatch", "Law Tac", "Law Talk"}, "law": {"Law Dispatch", "Law Tac", "Law Talk"},
	"law enforcement": {"Law Dispatch", "Law Tac", "Law Talk"},
	"fire":            {"Fire Dispatch", "Fire-Tac", "Fire-Talk"}, "fire brigade": {"Fire Dispatch", "Fire-Tac", "Fire-Talk"},
	"fire service": {"Fire Dispatch", "Fire-Tac", "Fire-Talk"},
	"ems":          {"EMS Dispatch", "EMS-Tac", "EMS-Talk"}, "ambulance": {"EMS Dispatch", "EMS-Tac", "EMS-Talk"},
	"medical": {"EMS Dispatch", "EMS-Tac", "EMS-Talk", "Hospital"}, "paramedic": {"EMS Dispatch", "EMS-Tac", "EMS-Talk"},
	"blue light":         {"Law Dispatch", "Law Tac", "Law Talk", "Fire Dispatch", "Fire-Tac", "Fire-Talk", "EMS Dispatch", "EMS-Tac", "EMS-Talk"},
	"emergency services": {"Law Dispatch", "Law Tac", "Law Talk", "Fire Dispatch", "Fire-Tac", "Fire-Talk", "EMS Dispatch", "EMS-Tac", "EMS-Talk"},
	"dispatch":           {"Multi-Dispatch", "Law Dispatch", "Fire Dispatch", "EMS Dispatch"},
	"rail":               {"Railroad"}, "railway": {"Railroad"}, "train": {"Railroad"}, "trains": {"Railroad"},
	"military": {"Military"}, "mod": {"Military"}, "army": {"Military"}, "raf": {"Military"}, "navy": {"Military"},
	"ham": {"Ham"}, "amateur": {"Ham"}, "amateur radio": {"Ham"}, "repeaters": {"Ham"},
	"utilities": {"Utilities"}, "power": {"Utilities"}, "water": {"Utilities"}, "gas": {"Utilities"},
	"council": {"Public Works"}, "public works": {"Public Works"}, "highways": {"Public Works"}, "refuse": {"Public Works"},
	"media": {"Media"}, "press": {"Media"}, "broadcast": {"Media"}, "tv": {"Media"},
	"schools": {"Schools"}, "school": {"Schools"}, "education": {"Schools"},
	"security": {"Security"}, "guards": {"Security"}, "doorstaff": {"Security"},
	"hospital": {"Hospital"}, "nhs": {"Hospital", "EMS Dispatch"},
	"business": {"Business"}, "shops": {"Business"}, "retail": {"Business"}, "industry": {"Business"},
	"transport": {"Transportation"}, "transportation": {"Transportation"}, "bus": {"Transportation"}, "buses": {"Transportation"}, "taxi": {"Transportation"},
	"federal": {"Federal"}, "government": {"Federal"}, "coastguard": {"Federal"},
	"corrections": {"Corrections"}, "prison": {"Corrections"}, "prisons": {"Corrections"},
	"interop": {"Interop"}, "interoperability": {"Interop"}, "multi-agency": {"Interop", "Multi-Dispatch", "Multi-Tac", "Multi-Talk"},
	"emergency ops": {"Emergency Ops"}, "emergency operations": {"Emergency Ops"}, "civil defence": {"Emergency Ops"},
	"other":      {"Other"},
	"everything": {"*"}, "all": {"*"}, "all services": {"*"}, "all service types": {"*"},
}

func normaliseServiceName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_' || r == '/':
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// matchServiceTypes resolves one word to the service-type slots it refers to.
// Exact names, aliases and partial names all work; anything else returns an
// error listing the names the scanner actually has, so the caller can retry.
func matchServiceTypes(want string, all []serviceType) ([]int, error) {
	n := normaliseServiceName(want)
	if n == "" {
		return nil, fmt.Errorf("empty service type name")
	}
	byNorm := map[string]int{}
	names := make([]string, 0, len(all))
	for i, it := range all {
		if it.Name == "" {
			continue
		}
		byNorm[normaliseServiceName(it.Name)] = i
		names = append(names, it.Name)
	}
	if i, ok := byNorm[n]; ok {
		return []int{i}, nil
	}
	if targets, ok := serviceTypeAliases[n]; ok {
		if len(targets) == 1 && targets[0] == "*" {
			idx := make([]int, 0, len(all))
			for i, it := range all {
				if it.Name != "" {
					idx = append(idx, i)
				}
			}
			return idx, nil
		}
		idx := []int{}
		for _, t := range targets {
			if i, ok := byNorm[normaliseServiceName(t)]; ok {
				idx = append(idx, i)
			}
		}
		if len(idx) > 0 {
			return idx, nil
		}
	}
	// Partial match: "law" -> every Law type, "fire dispatch" -> that one.
	idx := []int{}
	for norm, i := range byNorm {
		if strings.Contains(norm, n) || strings.Contains(n, norm) {
			idx = append(idx, i)
		}
	}
	if len(idx) > 0 {
		sort.Ints(idx)
		return idx, nil
	}
	return nil, fmt.Errorf("the scanner has no service type matching %q. It has: %s", strings.TrimSpace(want), strings.Join(names, ", "))
}
