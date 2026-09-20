package main

import (
	"bytes"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type CommandResult struct {
	Command   string  `json:"command"`
	Raw       string  `json:"raw"`
	ElapsedMS float64 `json:"elapsed_ms"`
}

var footerRE = regexp.MustCompile(`(?is)<(?:Foot|Footer)\b[^>]*\bNo="([0-9]+)"[^>]*\bEOT="([01])"[^>]*/?>`)
var rootRE = regexp.MustCompile(`(?s)<([A-Za-z_:][A-Za-z0-9_.:-]*)(?:\s[^>]*)?>`)

func commandName(wire string) string {
	p := strings.SplitN(wire, ",", 2)
	return strings.ToUpper(strings.TrimSpace(p[0]))
}
func safeRawForLog(raw string, max int) string {
	if max <= 0 {
		max = 800
	}
	s := strings.ReplaceAll(raw, "\r", "\\r")
	s = strings.ReplaceAll(s, "\n", "\\n")
	if len(s) > max {
		return s[:max] + fmt.Sprintf("…(%d bytes)", len(s))
	}
	return s
}

// udpCommand sends one CR-terminated SDS virtual-serial command and waits for the
// matching ordinary response or a complete bounded XML document. The scanner has
// no transaction ids, so callers serialize access above this function.
func udpCommand(host string, port int, wire string, timeout time.Duration) (CommandResult, error) {
	if strings.TrimSpace(host) == "" {
		return CommandResult{}, errors.New("no scanner selected")
	}
	if strings.ContainsAny(wire, "\r\n") {
		return CommandResult{}, errors.New("command must be one CR-free line")
	}
	if timeout <= 0 {
		timeout = 1250 * time.Millisecond
	}
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return CommandResult{}, err
	}
	c, err := net.DialUDP("udp4", nil, addr)
	if err != nil {
		return CommandResult{}, err
	}
	defer c.Close()
	started := time.Now()
	_ = c.SetDeadline(started.Add(timeout))
	if _, err = c.Write([]byte(wire + "\r")); err != nil {
		return CommandResult{}, err
	}
	expected := commandName(wire)
	var ordinary []string
	fragments := map[int]string{}
	eotNo := -1
	firstNo := -1
	var rootOpen, rootClose string
	buf := make([]byte, 65535)
	for {
		n, err := c.Read(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				if len(fragments) > 0 {
					if merged, ok := mergeFragments(fragments, eotNo, firstNo, rootOpen, rootClose); ok {
						return CommandResult{wire, merged, float64(time.Since(started).Microseconds()) / 1000}, nil
					}
				}
				if len(ordinary) > 0 {
					return CommandResult{wire, strings.Join(ordinary, "\r"), float64(time.Since(started).Microseconds()) / 1000}, nil
				}
				return CommandResult{}, fmt.Errorf("no response to %q", wire)
			}
			return CommandResult{}, fmt.Errorf("scanner network error: %w", err)
		}
		packet := append([]byte(nil), buf[:n]...)
		text := strings.TrimRight(string(packet), "\x00")
		// Numbered XML fragmentation. Each fragment can be prefixed by CMD,<XML>,.
		if m := footerRE.FindStringSubmatch(text); len(m) == 3 {
			no, _ := strconv.Atoi(m[1])
			if firstNo < 0 || no < firstNo {
				firstNo = no
			}
			if m[2] == "1" {
				eotNo = no
			}
			xmlText := extractXML(text)
			open, close, inner := xmlParts(xmlText)
			if rootOpen == "" && open != "" {
				rootOpen, rootClose = open, close
			}
			if inner != "" {
				fragments[no] = inner
			} else {
				fragments[no] = footerRE.ReplaceAllString(xmlText, "")
			}
			if merged, ok := mergeFragments(fragments, eotNo, firstNo, rootOpen, rootClose); ok {
				return CommandResult{wire, merged, float64(time.Since(started).Microseconds()) / 1000}, nil
			}
			continue
		}
		normalized := strings.ReplaceAll(text, "\n", "\r")
		for _, line := range strings.Split(normalized, "\r") {
			if line == "" {
				continue
			}
			ordinary = append(ordinary, line)
			u := strings.ToUpper(strings.TrimSpace(line))
			// XML-capable SDS commands may send the command marker as its own
			// datagram (for example, "GSI,<XML>,\r") and the XML document in
			// one or more later datagrams.  That marker is not a complete command
			// response and must never be returned to the caller on its own.
			if u == expected+",<XML>," {
				continue
			}
			if strings.HasPrefix(u, expected+",") || u == expected {
				joined := strings.Join(ordinary, "\r")
				if !looksLikeXML(joined) {
					return CommandResult{wire, joined, float64(time.Since(started).Microseconds()) / 1000}, nil
				}
				if xmlComplete(joined) {
					return CommandResult{wire, joined, float64(time.Since(started).Microseconds()) / 1000}, nil
				}
			}
		}
		joined := strings.Join(ordinary, "\r")
		if looksLikeXML(joined) && xmlComplete(joined) {
			return CommandResult{wire, joined, float64(time.Since(started).Microseconds()) / 1000}, nil
		}
	}
}

func extractXML(s string) string {
	if i := strings.Index(s, "<?xml"); i >= 0 {
		return s[i:]
	}
	// Skip CMD,<XML>, prefix if present.
	if i := strings.Index(strings.ToUpper(s), ",<XML>,"); i >= 0 {
		s = s[i+7:]
	}
	if m := rootRE.FindStringIndex(s); m != nil {
		return s[m[0]:]
	}
	return s
}
func looksLikeXML(s string) bool {
	x := extractXML(s)
	return strings.HasPrefix(strings.TrimSpace(x), "<")
}
func xmlComplete(s string) bool {
	x := footerRE.ReplaceAllString(extractXML(s), "")
	var v anyXML
	return xml.Unmarshal([]byte(x), &v) == nil
}

type anyXML struct {
	XMLName xml.Name
	Inner   string `xml:",innerxml"`
}

func xmlParts(x string) (open, close, inner string) {
	x = footerRE.ReplaceAllString(extractXML(x), "")
	x = strings.TrimSpace(x)
	if strings.HasPrefix(x, "<?xml") {
		if i := strings.Index(x, "?>"); i >= 0 {
			x = strings.TrimSpace(x[i+2:])
		}
	}
	m := rootRE.FindStringSubmatchIndex(x)
	if m == nil {
		return
	}
	open = x[m[0]:m[1]]
	tag := x[m[2]:m[3]]
	close = "</" + tag + ">"
	end := strings.LastIndex(x, close)
	if end < 0 {
		return open, close, ""
	}
	inner = x[m[1]:end]
	return
}
func mergeFragments(frags map[int]string, eot, first int, open, close string) (string, bool) {
	if eot < 0 || first < 0 || open == "" || close == "" {
		return "", false
	}
	for i := first; i <= eot; i++ {
		if _, ok := frags[i]; !ok {
			return "", false
		}
	}
	var b strings.Builder
	b.WriteString(open)
	for i := first; i <= eot; i++ {
		b.WriteString(frags[i])
	}
	b.WriteString(close)
	return b.String(), true
}

func packetFields(raw, command string) []string {
	for _, line := range strings.FieldsFunc(raw, func(r rune) bool { return r == '\r' || r == '\n' }) {
		line = strings.TrimSpace(line)
		if strings.EqualFold(line, command) {
			return []string{}
		}
		p := command + ","
		if len(line) >= len(p) && strings.EqualFold(line[:len(p)], p) {
			return strings.Split(line[len(p):], ",")
		}
	}
	return nil
}

func xmlRecords(raw string) (map[string]any, error) {
	x := extractXML(raw)
	x = footerRE.ReplaceAllString(x, "")
	dec := xml.NewDecoder(strings.NewReader(x))
	records := []map[string]any{}
	root := ""
	rootAttrs := map[string]string{}
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			if errors.Is(err, errors.New("EOF")) {
			}
			if err.Error() == "EOF" {
				break
			}
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			attrs := map[string]any{"tag": t.Name.Local}
			for _, a := range t.Attr {
				attrs[a.Name.Local] = a.Value
			}
			if depth == 1 {
				root = t.Name.Local
				for _, a := range t.Attr {
					rootAttrs[a.Name.Local] = a.Value
				}
			} else if depth == 2 {
				records = append(records, attrs)
			}
		case xml.EndElement:
			depth--
		}
	}
	return map[string]any{"root": root, "root_attributes": rootAttrs, "records": records, "raw": raw}, nil
}

type ScannerInfo struct {
	Mode            string                         `json:"mode,omitempty"`
	Screen          string                         `json:"screen,omitempty"`
	MonitorList     string                         `json:"monitor_list,omitempty"`
	System          string                         `json:"system,omitempty"`
	Department      string                         `json:"department,omitempty"`
	Site            string                         `json:"site,omitempty"`
	Channel         string                         `json:"channel,omitempty"`
	ChannelKind     string                         `json:"channel_kind,omitempty"`
	Frequency       string                         `json:"frequency,omitempty"`
	TGID            string                         `json:"tgid,omitempty"`
	UnitID          string                         `json:"unit_id,omitempty"`
	Modulation      string                         `json:"modulation,omitempty"`
	ServiceType     string                         `json:"service_type,omitempty"`
	SystemHold      string                         `json:"system_hold,omitempty"`
	DepartmentHold  string                         `json:"department_hold,omitempty"`
	SiteHold        string                         `json:"site_hold,omitempty"`
	ChannelHold     string                         `json:"channel_hold,omitempty"`
	SystemIndex     *int                           `json:"system_index,omitempty"`
	DepartmentIndex *int                           `json:"department_index,omitempty"`
	SiteIndex       *int                           `json:"site_index,omitempty"`
	ChannelIndex    *int                           `json:"channel_index,omitempty"`
	Volume          *int                           `json:"volume,omitempty"`
	Squelch         *int                           `json:"squelch,omitempty"`
	RSSI            string                         `json:"rssi,omitempty"`
	Signal          string                         `json:"signal,omitempty"`
	Recording       string                         `json:"recording,omitempty"`
	Mute            string                         `json:"mute,omitempty"`
	Attenuator      string                         `json:"attenuator,omitempty"`
	P25Status       string                         `json:"p25_status,omitempty"`
	Raw             string                         `json:"raw,omitempty"`
	ParseError      string                         `json:"parse_error,omitempty"`
	Elements        map[string][]map[string]string `json:"elements,omitempty"`
}

func parseScannerInfo(raw string) ScannerInfo {
	out := ScannerInfo{Raw: raw, Elements: map[string][]map[string]string{}}
	x := extractXML(raw)
	start := strings.Index(x, "<ScannerInfo")
	if start < 0 {
		out.ParseError = "ScannerInfo XML not found"
		return out
	}
	x = x[start:]
	x = footerRE.ReplaceAllString(x, "")
	dec := xml.NewDecoder(strings.NewReader(x))
	firstRoot := true
	for {
		tok, err := dec.Token()
		if err != nil {
			if err.Error() != "EOF" {
				out.ParseError = err.Error()
			}
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			attrs := map[string]string{}
			for _, a := range t.Attr {
				attrs[a.Name.Local] = a.Value
			}
			if firstRoot && t.Name.Local == "ScannerInfo" {
				out.Mode = attrs["Mode"]
				out.Screen = attrs["V_Screen"]
				firstRoot = false
				continue
			}
			out.Elements[t.Name.Local] = append(out.Elements[t.Name.Local], attrs)
		}
	}
	first := func(tag string) map[string]string {
		if a := out.Elements[tag]; len(a) > 0 {
			return a[0]
		}
		return map[string]string{}
	}
	monitor, sys, dept, site := first("MonitorList"), first("System"), first("Department"), first("Site")
	prop := first("Property")
	out.MonitorList = monitor["Name"]
	out.System = sys["Name"]
	out.Department = dept["Name"]
	out.Site = site["Name"]
	for _, tag := range []string{"ConvFrequency", "TGID", "SrchFrequency", "CcHitsChannel", "ToneOutChannel", "WxChannel"} {
		if len(out.Elements[tag]) > 0 {
			out.ChannelKind = tag
			break
		}
	}
	ch := first(out.ChannelKind)
	out.Channel = ch["Name"]
	out.Frequency = firstNonEmpty(ch["Freq"], ch["Frequency"], first("SiteFrequency")["Freq"], site["Freq"])
	out.TGID = firstNonEmpty(ch["TGID"], ch["Id"], ch["ID"])
	out.UnitID = firstNonEmpty(ch["U_Id"], ch["UID"], prop["U_Id"])
	out.Modulation = firstNonEmpty(ch["Mod"], site["Mod"], prop["P25Status"])
	out.ServiceType = firstNonEmpty(ch["SvcType"], ch["ServiceType"])
	out.SystemHold = sys["Hold"]
	out.DepartmentHold = dept["Hold"]
	out.SiteHold = site["Hold"]
	out.ChannelHold = ch["Hold"]
	out.SystemIndex = intPtr(sys["Index"])
	out.DepartmentIndex = intPtr(dept["Index"])
	out.SiteIndex = intPtr(site["Index"])
	out.ChannelIndex = intPtr(ch["Index"])
	out.Volume = intPtr(prop["VOL"])
	out.Squelch = intPtr(prop["SQL"])
	out.RSSI = firstNonEmpty(prop["Rssi"], prop["RSSI"])
	out.Signal = firstNonEmpty(prop["Sig"], prop["Signal"], prop["S_Level"])
	out.Recording = firstNonEmpty(prop["Rec"], prop["REC"], prop["Recording"])
	out.Mute = firstNonEmpty(prop["Mute"], prop["MUTE"])
	out.Attenuator = firstNonEmpty(prop["ATT"], prop["Att"])
	out.P25Status = prop["P25Status"]
	return out
}
func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}
func intPtr(s string) *int {
	if s == "" {
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	return &n
}

func decodeSTSDisplayText(s string) string {
	// SDS display streams use a single-byte scanner font: 0x20-0x7E are ASCII,
	// everything else is an icon/glyph (signal, battery, large digits). Some
	// glyph pairs happen to form valid UTF-8 sequences, so never UTF-8 decode;
	// keep printable ASCII only and turn the protocol's tab (escaped comma)
	// back into a comma. Glyphs become spaces to preserve column alignment.
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\t':
			b.WriteByte(',')
		case c >= 0x20 && c <= 0x7e:
			b.WriteByte(c)
		default:
			b.WriteByte(' ')
		}
	}
	return strings.TrimRight(b.String(), " ")
}
func safeRawScannerBytes(raw []byte) string {
	var b strings.Builder
	for _, c := range raw {
		if c == '\r' {
			b.WriteString("\\r")
		} else if c == '\n' {
			b.WriteString("\\n")
		} else if c >= 0x20 && c <= 0x7e {
			b.WriteByte(c)
		} else {
			b.WriteString(fmt.Sprintf("\\x%02x", c))
		}
	}
	return b.String()
}

type STSLine struct {
	Font string `json:"font"`
	Mode string `json:"mode"`
	Text string `json:"text"`
}
type STSDisplay struct {
	DisplayForm string    `json:"display_form"`
	ElapsedMS   float64   `json:"elapsed_ms"`
	Lines       []STSLine `json:"lines"`
	Raw         string    `json:"raw"`
	RawHex      string    `json:"raw_hex,omitempty"`
	ParseError  string    `json:"parse_error,omitempty"`
}

func parseSTS(raw string, elapsed float64) STSDisplay {
	out := STSDisplay{ElapsedMS: elapsed, Raw: safeRawScannerBytes([]byte(raw))}
	f := strings.Split(raw, ",")
	if len(f) < 2 || !strings.EqualFold(f[0], "STS") {
		out.ParseError = "not an STS response"
		return out
	}
	form := f[1]
	out.DisplayForm = form
	n := len(form)
	need := 2 + 2*n
	if n == 0 || len(f) < need {
		out.ParseError = fmt.Sprintf("unexpected STS field count %d for display form %q", len(f), form)
		return out
	}
	out.Lines = make([]STSLine, 0, n)
	for i := 0; i < n; i++ {
		txt := decodeSTSDisplayText(f[2+i*2])
		mode := decodeSTSDisplayText(f[3+i*2])
		font := "small"
		if form[i] == '1' {
			font = "large"
		}
		out.Lines = append(out.Lines, STSLine{Font: font, Mode: mode, Text: txt})
	}
	return out
}

func parseGST(raw string) map[string]any {
	fields := packetFields(raw, "GST")
	out := map[string]any{"raw": raw}
	if len(fields) == 0 {
		return out
	}
	form := fields[0]
	if len(form) < 5 || len(form) > 40 {
		return out
	}
	n := len(form)
	tailCount := 12
	expected := 1 + 2*n + tailCount
	if len(fields) < expected {
		out["parse_error"] = fmt.Sprintf("Unexpected GST field count %d", len(fields))
		return out
	}
	lineFields := fields[1 : 1+2*n]
	lines := []map[string]string{}
	for i := 0; i < len(lineFields); i += 2 {
		lines = append(lines, map[string]string{"text": decodeSTSDisplayText(lineFields[i]), "mode": decodeSTSDisplayText(lineFields[i+1])})
	}
	tail := fields[len(fields)-tailCount:]
	out["display_form"] = form
	out["lines"] = lines
	keys := []string{"mute", "alert_led", "charge_led", "waterfall_mode", "marker_frequency", "modulation", "marker_position", "center_frequency", "lower_frequency", "upper_frequency", "color_mode", "fft_area_size"}
	for i, k := range keys {
		out[k] = tail[i]
	}
	return out
}

func parseWaterfallLine(raw string) map[string]any {
	f := strings.Split(strings.TrimSpace(raw), ",")
	if len(f) < 2 {
		return nil
	}
	kind := strings.ToUpper(f[0])
	if kind != "GWF" && kind != "PWF" {
		return nil
	}
	vals := f[1:]
	if len(vals) > 0 && vals[len(vals)-1] == "" {
		vals = vals[:len(vals)-1]
	}
	if kind == "GWF" && len(vals) != 240 {
		return nil
	}
	numeric := make([]float64, 0, len(vals))
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v == "" {
			return nil
		}
		// GWF payloads are fixed two-digit hexadecimal bytes (e.g. "1a", "2f",
		// "10"); never interpret digit-only values as decimal.
		x, err := strconv.ParseUint(v, 16, 8)
		n := float64(x)
		if err != nil {
			return nil
		}
		numeric = append(numeric, n)
	}
	return map[string]any{"kind": kind, "values": numeric, "raw_values": vals, "count": len(vals), "received_at": float64(time.Now().UnixNano()) / 1e9, "calibrated": false}
}

func rtpPayload(datagram []byte) (payload []byte, seq uint16, timestamp uint32, ssrc uint32, pt byte, err error) {
	if len(datagram) < 12 {
		return nil, 0, 0, 0, 0, errors.New("RTP packet too short")
	}
	v := datagram[0] >> 6
	if v != 2 {
		return nil, 0, 0, 0, 0, fmt.Errorf("unsupported RTP version %d", v)
	}
	cc := int(datagram[0] & 0x0f)
	ext := datagram[0]&0x10 != 0
	padding := datagram[0]&0x20 != 0
	off := 12 + 4*cc
	if len(datagram) < off {
		return nil, 0, 0, 0, 0, errors.New("RTP CSRC truncated")
	}
	if ext {
		if len(datagram) < off+4 {
			return nil, 0, 0, 0, 0, errors.New("RTP extension truncated")
		}
		n := int(datagram[off+2])<<8 | int(datagram[off+3])
		off += 4 + 4*n
		if len(datagram) < off {
			return nil, 0, 0, 0, 0, errors.New("RTP extension body truncated")
		}
	}
	end := len(datagram)
	if padding {
		pad := int(datagram[len(datagram)-1])
		if pad == 0 || pad > end-off {
			return nil, 0, 0, 0, 0, errors.New("invalid RTP padding")
		}
		end -= pad
	}
	seq = uint16(datagram[2])<<8 | uint16(datagram[3])
	timestamp = uint32(datagram[4])<<24 | uint32(datagram[5])<<16 | uint32(datagram[6])<<8 | uint32(datagram[7])
	ssrc = uint32(datagram[8])<<24 | uint32(datagram[9])<<16 | uint32(datagram[10])<<8 | uint32(datagram[11])
	pt = datagram[1] & 0x7f
	return datagram[off:end], seq, timestamp, ssrc, pt, nil
}

func muLawToPCM(b byte) int16 {
	u := ^b
	sign := u & 0x80
	exponent := (u >> 4) & 0x07
	mantissa := u & 0x0f
	sample := ((int(mantissa) << 3) + 0x84) << exponent
	sample -= 0x84
	if sign != 0 {
		sample = -sample
	}
	if sample > 32767 {
		sample = 32767
	}
	if sample < -32768 {
		sample = -32768
	}
	return int16(sample)
}
func pcm16LEFromMuLaw(p []byte) []byte {
	out := make([]byte, len(p)*2)
	for i, b := range p {
		s := uint16(muLawToPCM(b))
		out[i*2] = byte(s)
		out[i*2+1] = byte(s >> 8)
	}
	return out
}
func hexShort(b []byte, max int) string {
	if len(b) > max {
		b = b[:max]
	}
	return hex.EncodeToString(b)
}

func sortedKeys[V any](m map[int]V) []int {
	ks := make([]int, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Ints(ks)
	return ks
}
func bytesIndexFold(b []byte, sub string) int {
	return bytes.Index(bytes.ToLower(b), bytes.ToLower([]byte(sub)))
}
