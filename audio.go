package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type RTSPResponse struct {
	Status  int
	Reason  string
	Headers map[string]string
	Body    []byte
}
type RTPTransport struct {
	Source     string
	ServerPort int
	ClientPort int
	SSRC       *uint32
}

type RTSPClient struct {
	host        string
	port        int
	path        string
	timeout     time.Duration
	log         *AppLogger
	conn        net.Conn
	br          *bufio.Reader
	cseq        int
	session     string
	contentBase string
	track       string
	transport   RTPTransport
}

func newRTSPClient(host string, port int, path string, log *AppLogger) *RTSPClient {
	if path == "" {
		path = "/au:scanner.au"
	}
	return &RTSPClient{host: host, port: port, path: path, timeout: 5 * time.Second, log: log}
}
func (c *RTSPClient) aggregate() string {
	authority := c.host
	if c.port != 554 {
		authority = fmt.Sprintf("%s:%d", c.host, c.port)
	}
	return "rtsp://" + authority + c.path
}
func (c *RTSPClient) connect() error {
	if c.conn != nil {
		return nil
	}
	start := time.Now()
	c.log.Info("RTSP", "TCP connect", map[string]any{"host": c.host, "port": c.port})
	conn, err := net.DialTimeout("tcp4", fmt.Sprintf("%s:%d", c.host, c.port), c.timeout)
	if err != nil {
		class := "tcp-connect"
		if strings.Contains(strings.ToLower(err.Error()), "refused") {
			class = "scanner-refused-port"
		}
		c.log.Error("RTSP", "TCP connect failed", map[string]any{"host": c.host, "port": c.port, "classification": class, "error": err.Error(), "elapsed_ms": float64(time.Since(start).Microseconds()) / 1000})
		return fmt.Errorf("rtsp tcp-connect: %w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(c.timeout))
	c.conn = conn
	c.br = bufio.NewReaderSize(conn, 64*1024)
	c.log.Info("RTSP", "TCP connected", map[string]any{"local": conn.LocalAddr().String(), "remote": conn.RemoteAddr().String(), "elapsed_ms": float64(time.Since(start).Microseconds()) / 1000})
	return nil
}
func (c *RTSPClient) Close() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
	c.conn = nil
	c.br = nil
	c.session = ""
	c.track = ""
	c.contentBase = ""
}
func (c *RTSPClient) request(method, uri string, headers map[string]string) (RTSPResponse, error) {
	if c.conn == nil {
		return RTSPResponse{}, errors.New("RTSP client not connected")
	}
	c.cseq++
	seq := c.cseq
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s RTSP/1.0\r\nCSeq: %d\r\nUser-Agent: SDS200-WebApp/%s\r\n", method, uri, seq, appVersion)
	for k, v := range headers {
		fmt.Fprintf(&b, "%s: %s\r\n", k, v)
	}
	b.WriteString("\r\n")
	payload := b.String()
	c.log.Debug("RTSP", "request", map[string]any{"method": method, "uri": uri, "cseq": seq, "headers": headers})
	c.log.Trace("RTSP", "wire tx", map[string]any{"request": strings.ReplaceAll(payload, "\r\n", " | ")})
	_ = c.conn.SetDeadline(time.Now().Add(c.timeout))
	start := time.Now()
	if _, err := io.WriteString(c.conn, payload); err != nil {
		return RTSPResponse{}, fmt.Errorf("RTSP %s write: %w", method, err)
	}
	res, err := c.readResponse()
	if err != nil {
		return RTSPResponse{}, fmt.Errorf("RTSP %s response: %w", method, err)
	}
	if v := res.Headers["cseq"]; v != "" && strings.TrimSpace(v) != strconv.Itoa(seq) {
		return RTSPResponse{}, fmt.Errorf("RTSP %s CSeq mismatch %q", method, v)
	}
	c.log.Info("RTSP", "response", map[string]any{"method": method, "status": res.Status, "reason": res.Reason, "elapsed_ms": float64(time.Since(start).Microseconds()) / 1000})
	if res.Status != 200 {
		return res, fmt.Errorf("RTSP %s failed with %d %s", method, res.Status, res.Reason)
	}
	return res, nil
}
func (c *RTSPClient) readResponse() (RTSPResponse, error) {
	if c.br == nil {
		return RTSPResponse{}, errors.New("no RTSP reader")
	}
	line, err := c.br.ReadString('\n')
	if err != nil {
		return RTSPResponse{}, err
	}
	line = strings.TrimRight(line, "\r\n")
	p := strings.SplitN(line, " ", 3)
	if len(p) < 2 || p[0] != "RTSP/1.0" {
		return RTSPResponse{}, fmt.Errorf("invalid RTSP status %q", line)
	}
	status, err := strconv.Atoi(p[1])
	if err != nil {
		return RTSPResponse{}, err
	}
	reason := ""
	if len(p) > 2 {
		reason = p[2]
	}
	h := map[string]string{}
	for {
		ln, e := c.br.ReadString('\n')
		if e != nil {
			return RTSPResponse{}, e
		}
		ln = strings.TrimRight(ln, "\r\n")
		if ln == "" {
			break
		}
		k, v, ok := strings.Cut(ln, ":")
		if !ok {
			return RTSPResponse{}, fmt.Errorf("invalid RTSP header %q", ln)
		}
		h[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	n := 0
	if s := h["content-length"]; s != "" {
		n, _ = strconv.Atoi(s)
		if n < 0 || n > 4*1024*1024 {
			return RTSPResponse{}, errors.New("RTSP content length out of range")
		}
	}
	body := make([]byte, n)
	if n > 0 {
		if _, err = io.ReadFull(c.br, body); err != nil {
			return RTSPResponse{}, err
		}
	}
	return RTSPResponse{status, reason, h, body}, nil
}
func parseSDPControl(body []byte) (string, error) {
	txt := string(body)
	inAudio := false
	hasPCMU := false
	control := ""
	for _, ln := range strings.Split(strings.ReplaceAll(txt, "\r\n", "\n"), "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "m=") {
			f := strings.Fields(strings.TrimPrefix(ln, "m="))
			inAudio = len(f) >= 4 && f[0] == "audio" && f[2] == "RTP/AVP"
			if inAudio {
				for _, x := range f[3:] {
					if x == "0" {
						hasPCMU = true
					}
				}
			}
			continue
		}
		if inAudio && strings.HasPrefix(ln, "a=control:") {
			control = strings.TrimSpace(strings.TrimPrefix(ln, "a=control:"))
		}
	}
	if !hasPCMU {
		return "", errors.New("SDP does not advertise PCMU payload type 0")
	}
	if control == "" {
		return "", errors.New("SDP has no audio control track")
	}
	return control, nil
}
func (c *RTSPClient) Start(clientPort int) (RTPTransport, error) {
	if err := c.connect(); err != nil {
		return RTPTransport{}, err
	}
	cleanup := func(err error) (RTPTransport, error) {
		if c.session != "" {
			_, _ = c.request("TEARDOWN", c.aggregate()+"/", map[string]string{"Session": c.session})
		}
		c.Close()
		return RTPTransport{}, err
	}
	if _, err := c.request("OPTIONS", c.aggregate(), nil); err != nil {
		return cleanup(fmt.Errorf("rtsp OPTIONS: %w", err))
	}
	d, err := c.request("DESCRIBE", c.aggregate(), map[string]string{"Accept": "application/sdp"})
	if err != nil {
		return cleanup(fmt.Errorf("rtsp DESCRIBE: %w", err))
	}
	if ct := strings.ToLower(d.Headers["content-type"]); ct != "" && !strings.HasPrefix(ct, "application/sdp") {
		return cleanup(fmt.Errorf("rtsp DESCRIBE returned content-type %q", ct))
	}
	c.contentBase = d.Headers["content-base"]
	if c.contentBase == "" {
		c.contentBase = c.aggregate() + "/"
	}
	track, err := parseSDPControl(d.Body)
	if err != nil {
		return cleanup(err)
	}
	if strings.HasPrefix(track, "rtsp://") {
		c.track = track
	} else {
		c.track = strings.TrimRight(c.contentBase, "/") + "/" + strings.TrimLeft(track, "/")
	}
	s, err := c.request("SETUP", c.track, map[string]string{"Transport": fmt.Sprintf("RTP/AVP;unicast;client_port=%d", clientPort)})
	if err != nil {
		return cleanup(fmt.Errorf("rtsp SETUP: %w", err))
	}
	session := strings.TrimSpace(strings.SplitN(s.Headers["session"], ";", 2)[0])
	if session == "" {
		return cleanup(errors.New("rtsp SETUP response missing Session"))
	}
	c.session = session
	tr, err := parseTransportHeader(s.Headers["transport"], clientPort)
	if err != nil {
		return cleanup(err)
	}
	c.transport = tr
	if _, err = c.request("PLAY", c.aggregate()+"/", map[string]string{"Session": c.session, "Range": "npt=0.000-"}); err != nil {
		return cleanup(fmt.Errorf("rtsp PLAY: %w", err))
	}
	return tr, nil
}
func parseTransportHeader(v string, clientPort int) (RTPTransport, error) {
	out := RTPTransport{ClientPort: clientPort}
	if v == "" {
		return out, errors.New("SETUP missing Transport header")
	}
	for _, f := range strings.Split(v, ";") {
		f = strings.TrimSpace(f)
		k, val, ok := strings.Cut(f, "=")
		if !ok {
			continue
		}
		switch strings.ToLower(k) {
		case "source":
			out.Source = val
		case "server_port":
			p := strings.SplitN(val, "-", 2)[0]
			out.ServerPort, _ = strconv.Atoi(p)
		case "client_port":
			p := strings.SplitN(val, "-", 2)[0]
			got, _ := strconv.Atoi(p)
			if got != 0 && got != clientPort {
				return out, fmt.Errorf("SETUP client_port %d does not match %d", got, clientPort)
			}
		case "ssrc":
			base := 10
			if strings.HasPrefix(strings.ToLower(val), "0x") || strings.IndexAny(val, "abcdefABCDEF") >= 0 {
				base = 16
				val = strings.TrimPrefix(strings.TrimPrefix(val, "0x"), "0X")
			}
			n, e := strconv.ParseUint(val, base, 32)
			if e == nil {
				x := uint32(n)
				out.SSRC = &x
			}
		}
	}
	if out.Source == "" {
		return out, errors.New("SETUP Transport missing source")
	}
	if out.ServerPort == 0 {
		return out, errors.New("SETUP Transport missing server_port")
	}
	return out, nil
}
func (c *RTSPClient) GetParameter() error {
	if c.session == "" {
		return errors.New("no RTSP session")
	}
	_, err := c.request("GET_PARAMETER", c.aggregate()+"/", map[string]string{"Session": c.session})
	return err
}
func (c *RTSPClient) Teardown() error {
	if c.session == "" {
		return nil
	}
	_, err := c.request("TEARDOWN", c.aggregate()+"/", map[string]string{"Session": c.session})
	return err
}

type AudioHub struct {
	app            *App
	startMu        sync.Mutex
	mu             sync.Mutex
	running        bool
	starting       bool
	stage          string
	lastError      string
	classification string
	url            string
	localIP        string
	packets        uint64
	bytes          uint64
	lastPacket     time.Time
	clientPort     int
	serverPort     int
	session        string
	owners         map[string]bool
	subs           map[chan []byte]struct{}
	stop           chan struct{}
	done           chan struct{}
	firstPacket    chan struct{}
	firstOnce      sync.Once
	rtp            *net.UDPConn
	rtsp           *RTSPClient
	expectedSource *net.UDPAddr
}

func NewAudioHub(a *App) *AudioHub {
	return &AudioHub{app: a, owners: map[string]bool{}, subs: map[chan []byte]struct{}{}, stage: "not-started"}
}
func (h *AudioHub) endpoint() string {
	host, port, path := h.app.rtspSettings()
	authority := host
	if port != 554 {
		authority = fmt.Sprintf("%s:%d", host, port)
	}
	return "rtsp://" + authority + path
}
func (h *AudioHub) Status() map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := "NOT STARTED"
	if h.starting {
		state = "STARTING"
	} else if h.running {
		state = "STREAMING"
	} else if h.lastError != "" {
		state = "ERROR"
	} else if h.stage == "stopped" {
		state = "STOPPED"
	}
	return map[string]any{"state": state, "reachable": h.running, "stage": h.stage, "error": h.lastError, "classification": h.classification, "url": h.url, "local_ip": h.localIP, "tcp_port": func() int { _, p, _ := h.app.rtspSettings(); return p }(), "session": map[string]any{"running": h.running, "packets": h.packets, "bytes": h.bytes, "last_packet": func() any {
		if h.lastPacket.IsZero() {
			return nil
		}
		return h.lastPacket.Format(time.RFC3339Nano)
	}(), "client_port": h.clientPort, "server_port": h.serverPort, "owners": len(h.owners)}}
}
func (h *AudioHub) setFailure(stage string, err error) {
	h.mu.Lock()
	h.running = false
	h.starting = false
	h.stage = stage
	h.lastError = err.Error()
	if strings.Contains(strings.ToLower(err.Error()), "refused") {
		h.classification = "scanner-refused-port"
	} else {
		h.classification = "rtsp-error"
	}
	h.mu.Unlock()
	h.app.log.Error("AUDIO", "RTSP audio start failed", map[string]any{"stage": stage, "classification": h.classification, "error": err.Error()})
}
func (h *AudioHub) Acquire(owner string) (map[string]any, error) {
	h.startMu.Lock()
	defer h.startMu.Unlock()
	h.mu.Lock()
	if h.running {
		h.owners[owner] = true
		d := h.statusLocked()
		h.mu.Unlock()
		h.app.log.Info("AUDIO", "Audio owner acquired existing session", map[string]any{"owner": owner})
		return d, nil
	}
	if h.starting {
		h.mu.Unlock()
		return nil, &httpError{409, "audio start already in progress"}
	}
	h.starting = true
	h.stage = "tcp-connect"
	h.lastError = ""
	h.classification = ""
	h.url = h.endpoint()
	h.owners[owner] = true
	h.packets = 0
	h.bytes = 0
	h.lastPacket = time.Time{}
	h.mu.Unlock()
	host, port, path := h.app.rtspSettings()
	if host == "" {
		err := errors.New("no scanner selected")
		h.setFailure("tcp-connect", err)
		return nil, err
	}
	local := routeLocalIPv4(host, port)
	if local == "" {
		err := errors.New("could not determine route-selected local IPv4 address")
		h.setFailure("rtp-bind", err)
		return nil, err
	}
	ip := net.ParseIP(local)
	rtp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip, Port: 0})
	if err != nil {
		h.setFailure("rtp-bind", err)
		return nil, err
	}
	_ = rtp.SetReadBuffer(256 * 1024)
	clientPort := rtp.LocalAddr().(*net.UDPAddr).Port
	client := newRTSPClient(host, port, path, h.app.log)
	h.mu.Lock()
	h.stage = "OPTIONS/DESCRIBE/SETUP/PLAY"
	h.localIP = local
	h.clientPort = clientPort
	h.rtp = rtp
	h.rtsp = client
	h.stop = make(chan struct{})
	h.done = make(chan struct{})
	h.firstPacket = make(chan struct{})
	h.firstOnce = sync.Once{}
	h.mu.Unlock()
	tr, err := client.Start(clientPort)
	if err != nil {
		_ = rtp.Close()
		h.mu.Lock()
		delete(h.owners, owner)
		h.rtp = nil
		h.rtsp = nil
		h.mu.Unlock()
		h.setFailure(rtspErrorStage(err), err)
		return nil, err
	}
	expectedIP := net.ParseIP(tr.Source)
	exp := &net.UDPAddr{IP: expectedIP, Port: tr.ServerPort}
	// Open the host firewall's stateful UDP path (Windows Defender Firewall,
	// nftables conntrack) by sending from the RTP port to the scanner's RTP
	// source port. Without this the unsolicited RTP stream is silently dropped
	// unless an inbound allow rule exists for the executable.
	punchRTP(rtp, exp, h.app.log)
	h.mu.Lock()
	h.expectedSource = exp
	h.serverPort = tr.ServerPort
	h.session = client.session
	h.running = true
	h.starting = false
	h.stage = "play-ok-waiting-rtp"
	h.mu.Unlock()
	h.app.log.Info("AUDIO", "RTSP PLAY succeeded", map[string]any{"client_port": clientPort, "server_port": tr.ServerPort, "source": tr.Source, "session": shorten(client.session, 16)})
	go h.run()
	select {
	case <-h.firstPacket:
		h.mu.Lock()
		h.stage = "streaming"
		d := h.statusLocked()
		h.mu.Unlock()
		h.app.log.Info("RTP", "First audio packet received", map[string]any{"packets": 1})
		return d, nil
	case <-time.After(5 * time.Second):
		h.app.log.Warn("RTP", "PLAY succeeded but no RTP packet arrived within 5s", map[string]any{"client_port": clientPort, "server_port": tr.ServerPort})
		h.mu.Lock()
		d := h.statusLocked()
		h.mu.Unlock()
		return d, nil
	}
}

// punchRTP sends a 4-byte non-RTP datagram (the same NAT/firewall keepalive
// pattern used by live555/VLC) to the scanner's RTP source port.
func punchRTP(conn *net.UDPConn, to *net.UDPAddr, log *AppLogger) {
	if conn == nil || to == nil || to.Port == 0 {
		return
	}
	if _, err := conn.WriteToUDP([]byte{0xce, 0xfa, 0xed, 0xfe}, to); err != nil {
		log.Warn("RTP", "Firewall punch send failed", map[string]any{"to": to.String(), "error": err.Error()})
		return
	}
	log.Debug("RTP", "Firewall punch sent", map[string]any{"to": to.String()})
}
func rtspErrorStage(err error) string {
	s := err.Error()
	for _, k := range []string{"tcp-connect", "OPTIONS", "DESCRIBE", "SETUP", "PLAY"} {
		if strings.Contains(strings.ToUpper(s), strings.ToUpper(k)) {
			return strings.ToLower(k)
		}
	}
	return "rtsp-start"
}
func (h *AudioHub) statusLocked() map[string]any {
	state := "NOT STARTED"
	if h.starting {
		state = "STARTING"
	} else if h.running {
		state = "STREAMING"
	} else if h.lastError != "" {
		state = "ERROR"
	} else if h.stage == "stopped" {
		state = "STOPPED"
	}
	return map[string]any{"state": state, "reachable": h.running, "stage": h.stage, "error": h.lastError, "classification": h.classification, "url": h.url, "local_ip": h.localIP, "tcp_port": func() int { _, p, _ := h.app.rtspSettings(); return p }(), "session": map[string]any{"running": h.running, "packets": h.packets, "bytes": h.bytes, "client_port": h.clientPort, "server_port": h.serverPort, "owners": len(h.owners)}}
}
func (h *AudioHub) run() {
	h.mu.Lock()
	rtp := h.rtp
	stop := h.stop
	done := h.done
	client := h.rtsp
	expected := h.expectedSource
	h.mu.Unlock()
	defer close(done)
	keep := time.NewTicker(15 * time.Second)
	defer keep.Stop()
	buf := make([]byte, 65535)
	for {
		_ = rtp.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, from, err := rtp.ReadFromUDP(buf)
		if err == nil && n > 0 {
			if expected != nil && (from.IP.String() != expected.IP.String() || from.Port != expected.Port) {
				h.app.log.Debug("RTP", "Discarding packet from unexpected source", map[string]any{"source": from.String(), "expected": expected.String()})
				continue
			}
			payload, seq, ts, ssrc, pt, e := rtpPayload(buf[:n])
			if e != nil {
				h.app.log.Warn("RTP", "Malformed RTP packet", map[string]any{"error": e.Error(), "hex": hexShort(buf[:n], 32)})
				continue
			}
			if pt != 0 {
				h.app.log.Warn("RTP", "Unexpected payload type", map[string]any{"payload_type": pt, "sequence": seq})
				continue
			}
			pcm := pcm16LEFromMuLaw(payload)
			h.mu.Lock()
			h.packets++
			h.bytes += uint64(len(payload))
			h.lastPacket = time.Now()
			subs := make([]chan []byte, 0, len(h.subs))
			for ch := range h.subs {
				subs = append(subs, ch)
			}
			h.mu.Unlock()
			h.firstOnce.Do(func() { close(h.firstPacket) })
			if h.app.log.Level() == "TRACE" {
				h.app.log.Trace("RTP", "packet", map[string]any{"sequence": seq, "timestamp": ts, "ssrc": ssrc, "payload_bytes": len(payload)})
			}
			for _, ch := range subs {
				cp := append([]byte(nil), pcm...)
				select {
				case ch <- cp:
				default:
				}
			}
		} else if err != nil {
			if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
				select {
				case <-stop:
					return
				default:
					h.app.log.Error("RTP", "RTP receive failed", map[string]any{"error": err.Error()})
					return
				}
			}
		}
		select {
		case <-stop:
			return
		case <-keep.C:
			punchRTP(rtp, expected, h.app.log)
			if client != nil {
				if err := client.GetParameter(); err != nil {
					h.app.log.Error("RTSP", "GET_PARAMETER keepalive failed", map[string]any{"error": err.Error()})
					return
				}
				h.app.log.Debug("RTSP", "GET_PARAMETER keepalive OK", nil)
			}
		default:
		}
	}
}
func (h *AudioHub) AcquireExisting(owner string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.running || h.packets == 0 {
		return &httpError{409, "start Listen Live first; remote recording will not open a separate RTSP session"}
	}
	h.owners[owner] = true
	h.app.log.Info("AUDIO", "Audio owner acquired existing session", map[string]any{"owner": owner})
	return nil
}

func (h *AudioHub) Release(owner string) {
	h.startMu.Lock()
	defer h.startMu.Unlock()
	h.mu.Lock()
	delete(h.owners, owner)
	remain := len(h.owners)
	h.mu.Unlock()
	h.app.log.Info("AUDIO", "Audio owner released", map[string]any{"owner": owner, "remaining": remain})
	if remain == 0 {
		h.stopSession("no audio owners")
	}
}
func (h *AudioHub) ForceStop(reason string) {
	h.startMu.Lock()
	defer h.startMu.Unlock()
	h.mu.Lock()
	h.owners = map[string]bool{}
	h.mu.Unlock()
	h.stopSession(reason)
}
func (h *AudioHub) stopSession(reason string) {
	h.mu.Lock()
	if !h.running && !h.starting {
		h.stage = "stopped"
		h.mu.Unlock()
		return
	}
	stop := h.stop
	done := h.done
	rtp := h.rtp
	client := h.rtsp
	h.running = false
	h.starting = false
	h.stage = "teardown"
	h.mu.Unlock()
	if client != nil {
		h.app.log.Info("RTSP", "Sending TEARDOWN", map[string]any{"reason": reason})
		if err := client.Teardown(); err != nil {
			h.app.log.Warn("RTSP", "TEARDOWN failed", map[string]any{"error": err.Error()})
		} else {
			h.app.log.Info("RTSP", "TEARDOWN complete", nil)
		}
	}
	if stop != nil {
		select {
		case <-stop:
		default:
			close(stop)
		}
	}
	if rtp != nil {
		_ = rtp.Close()
	}
	if client != nil {
		client.Close()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(1500 * time.Millisecond):
		}
	}
	h.mu.Lock()
	h.rtp = nil
	h.rtsp = nil
	h.expectedSource = nil
	h.stage = "stopped"
	h.session = ""
	h.clientPort = 0
	h.serverPort = 0
	h.mu.Unlock()
	h.app.log.Info("AUDIO", "Audio session stopped", map[string]any{"reason": reason})
}
func (h *AudioHub) Subscribe() (chan []byte, func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.running || h.packets == 0 {
		return nil, nil, &httpError{409, "audio is not ready; press Listen Live and wait for RTP"}
	}
	ch := make(chan []byte, 64)
	h.subs[ch] = struct{}{}
	return ch, func() {
		h.mu.Lock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
		h.mu.Unlock()
	}, nil
}
func streamingWAVHeader() []byte {
	b := make([]byte, 44)
	copy(b[0:4], "RIFF")
	binary.LittleEndian.PutUint32(b[4:8], 0xffffffff)
	copy(b[8:12], "WAVE")
	copy(b[12:16], "fmt ")
	binary.LittleEndian.PutUint32(b[16:20], 16)
	binary.LittleEndian.PutUint16(b[20:22], 1)
	binary.LittleEndian.PutUint16(b[22:24], 1)
	binary.LittleEndian.PutUint32(b[24:28], 8000)
	binary.LittleEndian.PutUint32(b[28:32], 16000)
	binary.LittleEndian.PutUint16(b[32:34], 2)
	binary.LittleEndian.PutUint16(b[34:36], 16)
	copy(b[36:40], "data")
	binary.LittleEndian.PutUint32(b[40:44], 0xffffffff)
	return b
}
func (h *AudioHub) StreamWAV(w http.ResponseWriter, r *http.Request) error {
	ch, release, err := h.Subscribe()
	if err != nil {
		return err
	}
	defer release()
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-store, no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	if _, err = w.Write(streamingWAVHeader()); err != nil {
		return err
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	h.app.log.Info("AUDIO", "Browser WAV subscriber connected", map[string]any{"remote": r.RemoteAddr})
	for {
		select {
		case <-r.Context().Done():
			h.app.log.Info("AUDIO", "Browser WAV subscriber disconnected", nil)
			return nil
		case b, ok := <-ch:
			if !ok {
				return nil
			}
			if _, err := w.Write(b); err != nil {
				return err
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}
}

// Recorder records decoded PCM from the shared single RTSP session; it never opens
// a second scanner audio connection.
type Recorder struct {
	app     *App
	dir     string
	mu      sync.Mutex
	file    *os.File
	path    string
	started time.Time
	bytes   uint64
	stop    chan struct{}
	done    chan struct{}
}

func NewRecorder(a *App, dir string) *Recorder {
	_ = os.MkdirAll(dir, 0o755)
	return &Recorder{app: a, dir: dir}
}
func wavHeader(dataBytes uint32) []byte {
	b := streamingWAVHeader()
	binary.LittleEndian.PutUint32(b[4:8], 36+dataBytes)
	binary.LittleEndian.PutUint32(b[40:44], dataBytes)
	return b
}
func (r *Recorder) Start() (map[string]any, error) {
	r.mu.Lock()
	if r.file != nil {
		d := r.stateLocked()
		r.mu.Unlock()
		return d, nil
	}
	r.mu.Unlock()
	if err := r.app.audio.AcquireExisting("record"); err != nil {
		return nil, err
	}
	ch, release, err := r.app.audio.Subscribe()
	if err != nil {
		r.app.audio.Release("record")
		return nil, err
	}
	stamp := time.Now().Format("20060102_150405")
	path := filepath.Join(r.dir, "SDS200_"+stamp+".wav")
	f, err := os.Create(path)
	if err != nil {
		release()
		r.app.audio.Release("record")
		return nil, err
	}
	if _, err = f.Write(wavHeader(0)); err != nil {
		f.Close()
		release()
		r.app.audio.Release("record")
		return nil, err
	}
	r.mu.Lock()
	r.file = f
	r.path = path
	r.started = time.Now()
	r.bytes = 0
	r.stop = make(chan struct{})
	r.done = make(chan struct{})
	stop := r.stop
	done := r.done
	r.mu.Unlock()
	r.app.log.Info("RECORD", "Remote WAV recording started", map[string]any{"path": path})
	go func() {
		defer close(done)
		defer release()
		for {
			select {
			case <-stop:
				return
			case b, ok := <-ch:
				if !ok {
					return
				}
				r.mu.Lock()
				if r.file != nil {
					n, e := r.file.Write(b)
					r.bytes += uint64(n)
					if e != nil {
						r.app.log.Error("RECORD", "Recording write failed", map[string]any{"error": e.Error()})
					}
				}
				r.mu.Unlock()
			}
		}
	}()
	r.mu.Lock()
	d := r.stateLocked()
	r.mu.Unlock()
	return d, nil
}
func (r *Recorder) Stop() error {
	r.mu.Lock()
	if r.file == nil {
		r.mu.Unlock()
		r.app.audio.Release("record")
		return nil
	}
	stop := r.stop
	done := r.done
	r.mu.Unlock()
	select {
	case <-stop:
	default:
		close(stop)
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
	r.mu.Lock()
	f := r.file
	bytesN := r.bytes
	path := r.path
	r.file = nil
	r.stop = nil
	r.done = nil
	r.mu.Unlock()
	if f != nil {
		_, _ = f.Seek(0, 0)
		size := uint32(bytesN)
		if bytesN > 0xffffffff {
			size = 0xffffffff
		}
		_, _ = f.Write(wavHeader(size))
		_ = f.Close()
	}
	r.app.audio.Release("record")
	r.app.log.Info("RECORD", "Remote WAV recording stopped", map[string]any{"path": path, "pcm_bytes": bytesN})
	return nil
}
func (r *Recorder) stateLocked() map[string]any {
	running := r.file != nil
	name := ""
	urlPath := ""
	if r.path != "" {
		name = filepath.Base(r.path)
		urlPath = "/recordings/" + url.PathEscape(name)
	}
	elapsed := 0.0
	if running {
		elapsed = time.Since(r.started).Seconds()
	}
	return map[string]any{"recording": running, "filename": name, "url": urlPath, "elapsed_s": elapsed, "bytes": r.bytes}
}
func (r *Recorder) State() map[string]any { r.mu.Lock(); defer r.mu.Unlock(); return r.stateLocked() }
