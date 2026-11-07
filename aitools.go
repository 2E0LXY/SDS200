package main

// The scanner operations exposed to the assistant. Every tool is a thin
// wrapper over a verified command (see docs/PROTOCOL.md); nothing here invents
// scanner behaviour, and a tool that cannot be carried out returns an error
// the model can read and explain.

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const aiSystemPrompt = `You control a Uniden SDS200E scanner (firmware 1.23.15) on the operator's local network, through the tools provided. You are talking to a licensed UK amateur radio operator.

Rules:
- Use the tools for anything about the scanner's current state; never guess what it is doing.
- Read the state first when a request depends on it (which channel, which system, what mode).
- Volume is 0-29, squelch 0-19. Frequencies are given in MHz by the operator; tools take MHz unless stated.
- Holds toggle a specific item: hold the channel to stay on it, release to resume scanning.
- The SDS200 has no Service, Range or Squelch-push key. Service Types is func_key with key "Z"; range is set with set_range.
- Long presses cannot be sent over the network, so the three-second functions (global attenuator, weather mode, colour mode) are unavailable; say so if asked.
- Waterfall and analyse modes take the radio out of normal scanning. Say what you are about to do, do it, then report what the scanner shows.
- Programming favourites (new systems, departments, channels) needs the microSD card and is not available through these tools yet. Custom search ranges and quick keys are.
- Keep answers short and factual, in British English. Give frequencies as 453.7250 MHz. If a tool fails, say what failed and what the scanner reported.`

// aiTools builds the tool set bound to this App.
func (a *App) aiTools() []AITool {
	hz := func(mhz float64) int { return int(mhz*1e6 + 0.5) }

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
				m := map[string]string{
					"scan": "SCN_MODE", "close_call": "CC_MODE", "weather": "WX_MODE", "tone_out": "FTO_MODE",
					"custom_search": "CTM_MODE", "quick_search": "QSH_MODE", "waterfall": "WF_MODE",
					"conventional_discovery": "CDIS_MODE", "trunking_discovery": "TDIS_MODE",
				}[strings.ToLower(argString(args, "mode"))]
				if m == "" {
					return nil, fmt.Errorf("unknown mode")
				}
				wire := "JPM," + m + "," + argString(args, "index")
				if _, err := a.execWrite(wire, 2500*time.Millisecond); err != nil {
					return nil, err
				}
				a.invalidateState()
				time.Sleep(600 * time.Millisecond)
				return a.readState(true), nil
			},
		},
		{
			Name:        "tune_frequency",
			Description: "Tune straight to a frequency in quick search hold, so the radio listens on it. Takes MHz, e.g. 446.00625.",
			Parameters:  schema(map[string]any{"mhz": numProp("Frequency in MHz, 25 to 1300")}, "mhz"),
			Writes:      true,
			Run: func(args map[string]any) (any, error) {
				f, ok := argFloat(args, "mhz")
				if !ok || f < 25 || f > 1300 {
					return nil, fmt.Errorf("frequency must be 25-1300 MHz")
				}
				if _, err := a.execWrite(fmt.Sprintf("QSH,%d", hz(f)), 2500*time.Millisecond); err != nil {
					return nil, err
				}
				a.invalidateState()
				time.Sleep(500 * time.Millisecond)
				return a.readState(true), nil
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
			Description: "Turn service types on or off by name, e.g. enable ['Fire Dispatch','EMS Dispatch'] or disable ['Aircraft']. Names must match get_service_types.",
			Parameters: schema(map[string]any{
				"enable":  map[string]any{"type": "array", "description": "Service type names to switch on", "items": map[string]any{"type": "string"}},
				"disable": map[string]any{"type": "array", "description": "Service type names to switch off", "items": map[string]any{"type": "string"}},
			}),
			Writes: true,
			Run: func(args map[string]any) (any, error) {
				cur, err := a.serviceTypes()
				if err != nil {
					return nil, err
				}
				states := make([]int, 47)
				byName := map[string]int{}
				for i, it := range cur {
					if it.Enabled {
						states[i] = 1
					}
					if it.Name != "" {
						byName[strings.ToLower(it.Name)] = i
					}
				}
				apply := func(key string, on int) error {
					list, _ := args[key].([]any)
					for _, v := range list {
						n := strings.ToLower(strings.TrimSpace(fmt.Sprint(v)))
						idx, ok := byName[n]
						if !ok {
							return fmt.Errorf("no service type called %q", v)
						}
						states[idx] = on
					}
					return nil
				}
				if err := apply("enable", 1); err != nil {
					return nil, err
				}
				if err := apply("disable", 0); err != nil {
					return nil, err
				}
				if _, err := a.execWrite("SVC,"+joinInts(states), 2*time.Second); err != nil {
					return nil, err
				}
				return a.serviceTypes()
			},
		},
		{
			Name:        "get_quick_keys",
			Description: "Favourites quick keys 00-99 and whether each exists and is enabled.",
			Parameters:  schema(map[string]any{}),
			Run: func(args map[string]any) (any, error) {
				res, err := a.command("FQK", 1500*time.Millisecond)
				if err != nil {
					return nil, err
				}
				f := packetFields(res.Raw, "FQK")
				out := []map[string]any{}
				for i, s := range f {
					n, _ := strconv.Atoi(strings.TrimSpace(s))
					if n == 0 {
						continue
					}
					out = append(out, map[string]any{"key": fmt.Sprintf("%02d", i), "enabled": n == 2})
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
				if !ok || !ok2 || k < 0 || k > 99 {
					return nil, fmt.Errorf("key must be 0-99 and enabled true/false")
				}
				res, err := a.command("FQK", 1500*time.Millisecond)
				if err != nil {
					return nil, err
				}
				f := packetFields(res.Raw, "FQK")
				if len(f) < 100 {
					return nil, fmt.Errorf("unexpected FQK reply")
				}
				states := make([]int, 100)
				for i := 0; i < 100; i++ {
					states[i], _ = strconv.Atoi(strings.TrimSpace(f[i]))
				}
				if states[k] == 0 {
					return nil, fmt.Errorf("quick key %02d is not assigned to any favourites list", k)
				}
				if on {
					states[k] = 2
				} else {
					states[k] = 1
				}
				if _, err := a.execWrite("FQK,"+joinInts(states), 2*time.Second); err != nil {
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
			Description: "Set the value of the menu item currently selected: the index of an option for a selection menu, or the text for an input menu. Read the menu first.",
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
