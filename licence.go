package main

// Trial period and licence keys.
//
// The app runs unrestricted for 14 days from first start. After that, control
// of the scanner (anything that writes, plus audio and the assistant) needs a
// licence key; reading status stays available so the operator can still see
// what is going on and activate.
//
// A key is an Ed25519-signed token, so it can be checked with no network and
// no server round trip. The app embeds only the public key: keys are issued by
// the licence server in cmd/licence-server, which holds the private half.
//
// Honest limitation: any check that runs on the customer's own machine can be
// removed by someone willing to patch or rebuild the binary. This stops casual
// sharing and makes buying the easy path; it is not copy protection.

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	trialDays  = 14
	keyPrefix  = "SDS200-1"
	productTag = "sds200-webapp"
)

// licencePublicKey verifies keys issued by the licence server. Replace this
// with the public half printed by "licence-server -keygen" before shipping.
var licencePublicKey = "" // hex-encoded Ed25519 public key; empty disables key checks

var keyEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// Licence is the payload inside a key.
type Licence struct {
	Product  string    `json:"p"`
	Licensee string    `json:"n"`
	Email    string    `json:"e,omitempty"`
	Issued   time.Time `json:"i"`
	Expires  time.Time `json:"x,omitempty"` // zero means perpetual
	Order    string    `json:"o,omitempty"` // PayPal order id, for support
}

// licenceState is stored beside the config so a reinstall of the binary does
// not reset the trial. It is deliberately not hidden or obfuscated.
type licenceState struct {
	FirstRun time.Time `json:"first_run"`
	Key      string    `json:"key,omitempty"`
}

type LicenceManager struct {
	mu    sync.RWMutex
	path  string
	state licenceState
	lic   *Licence
	log   *AppLogger
}

func NewLicenceManager(dir string, log *AppLogger) *LicenceManager {
	m := &LicenceManager{path: filepath.Join(dir, "licence.json"), log: log}
	if b, err := os.ReadFile(m.path); err == nil {
		_ = json.Unmarshal(b, &m.state)
	}
	if m.state.FirstRun.IsZero() {
		m.state.FirstRun = time.Now()
		m.save()
	}
	if m.state.Key != "" {
		if lic, err := VerifyKey(m.state.Key); err == nil {
			m.lic = lic
		} else if log != nil {
			log.Warn("LICENCE", "Stored key is not valid", map[string]any{"error": err.Error()})
		}
	}
	return m
}

func (m *LicenceManager) save() {
	_ = saveJSON(m.path, m.state)
}

// TrialEnds is when the free period runs out.
func (m *LicenceManager) TrialEnds() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.FirstRun.AddDate(0, 0, trialDays)
}

// Allowed reports whether scanner control is permitted, and why not if it is
// not. Reading state is always allowed.
func (m *LicenceManager) Allowed() (bool, string) {
	m.mu.RLock()
	lic := m.lic
	m.mu.RUnlock()
	if lic != nil {
		if !lic.Expires.IsZero() && time.Now().After(lic.Expires) {
			return false, "this licence expired on " + lic.Expires.Format("2 January 2006")
		}
		return true, ""
	}
	if time.Now().Before(m.TrialEnds()) {
		return true, ""
	}
	return false, fmt.Sprintf("the %d-day trial ended on %s", trialDays, m.TrialEnds().Format("2 January 2006"))
}

func (m *LicenceManager) Status() map[string]any {
	m.mu.RLock()
	lic, first := m.lic, m.state.FirstRun
	m.mu.RUnlock()
	ok, reason := m.Allowed()
	out := map[string]any{
		"licensed":     lic != nil,
		"allowed":      ok,
		"first_run":    first.Format(time.RFC3339),
		"trial_days":   trialDays,
		"trial_ends":   m.TrialEnds().Format(time.RFC3339),
		"days_left":    int(time.Until(m.TrialEnds()).Hours() / 24),
		"price":        "£15",
		"keys_enabled": licencePublicKey != "",
	}
	if reason != "" {
		out["reason"] = reason
	}
	if lic != nil {
		out["licensee"] = lic.Licensee
		out["issued"] = lic.Issued.Format("2006-01-02")
		if !lic.Expires.IsZero() {
			out["expires"] = lic.Expires.Format("2006-01-02")
		}
		delete(out, "days_left")
	}
	return out
}

// Activate stores a key after checking it.
func (m *LicenceManager) Activate(key string) (*Licence, error) {
	lic, err := VerifyKey(key)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.lic = lic
	m.state.Key = strings.TrimSpace(key)
	m.mu.Unlock()
	m.save()
	if m.log != nil {
		m.log.Info("LICENCE", "Activated", map[string]any{"licensee": lic.Licensee})
	}
	return lic, nil
}

// InstallationID is a stable, non-identifying id used only to tie a purchase
// to the machine it was bought from, so a lost key can be re-sent.
func (m *LicenceManager) InstallationID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	h := sha256.Sum256([]byte(m.path + m.state.FirstRun.Format(time.RFC3339Nano)))
	return hex.EncodeToString(h[:6])
}

// ----------------------------------------------------------------- the key
//
// Wire format: SDS200-1-<base32(payload)>-<base32(signature)>, upper case and
// grouped in fives so it can be read out over the air or copied by hand.

func formatKey(payload, sig []byte) string {
	body := keyEncoding.EncodeToString(payload) + "-" + keyEncoding.EncodeToString(sig)
	return keyPrefix + "-" + body
}

// SignKey builds a key for a licence. Used by the licence server.
func SignKey(priv ed25519.PrivateKey, lic Licence) (string, error) {
	lic.Product = productTag
	payload, err := encodeLicence(lic)
	if err != nil {
		return "", err
	}
	return formatKey(payload, ed25519.Sign(priv, payload)), nil
}

// VerifyKey checks a key's signature and returns what it grants.
func VerifyKey(key string) (*Licence, error) {
	key = strings.ToUpper(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, " ", "")
	if !strings.HasPrefix(key, strings.ToUpper(keyPrefix)+"-") {
		return nil, errors.New("that does not look like an SDS200 licence key")
	}
	rest := key[len(keyPrefix)+1:]
	parts := strings.Split(rest, "-")
	if len(parts) != 2 {
		return nil, errors.New("the key is incomplete")
	}
	payload, err := keyEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, errors.New("the key is damaged")
	}
	sig, err := keyEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("the key is damaged")
	}
	if licencePublicKey == "" {
		return nil, errors.New("this build has no licence public key, so keys cannot be checked")
	}
	pub, err := hex.DecodeString(licencePublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("this build has a malformed licence public key")
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), payload, sig) {
		return nil, errors.New("the key is not valid for this product")
	}
	lic, err := decodeLicence(payload)
	if err != nil {
		return nil, err
	}
	if lic.Product != productTag {
		return nil, errors.New("that key is for a different product")
	}
	return lic, nil
}

// encodeLicence packs the payload compactly so keys stay short enough to type.
func encodeLicence(l Licence) ([]byte, error) {
	if l.Licensee == "" {
		return nil, errors.New("a licence needs a licensee name")
	}
	b := []byte{1}
	b = appendString(b, l.Product)
	b = appendString(b, l.Licensee)
	b = appendString(b, l.Email)
	b = appendString(b, l.Order)
	b = binary.BigEndian.AppendUint32(b, uint32(l.Issued.Unix()))
	var exp uint32
	if !l.Expires.IsZero() {
		exp = uint32(l.Expires.Unix())
	}
	b = binary.BigEndian.AppendUint32(b, exp)
	return b, nil
}

func decodeLicence(b []byte) (*Licence, error) {
	if len(b) < 1 || b[0] != 1 {
		return nil, errors.New("unsupported key version")
	}
	p := b[1:]
	var l Licence
	var err error
	if l.Product, p, err = takeString(p); err != nil {
		return nil, err
	}
	if l.Licensee, p, err = takeString(p); err != nil {
		return nil, err
	}
	if l.Email, p, err = takeString(p); err != nil {
		return nil, err
	}
	if l.Order, p, err = takeString(p); err != nil {
		return nil, err
	}
	if len(p) < 8 {
		return nil, errors.New("the key is truncated")
	}
	l.Issued = time.Unix(int64(binary.BigEndian.Uint32(p[:4])), 0)
	if exp := binary.BigEndian.Uint32(p[4:8]); exp != 0 {
		l.Expires = time.Unix(int64(exp), 0)
	}
	return &l, nil
}

func appendString(b []byte, s string) []byte {
	if len(s) > 255 {
		s = s[:255]
	}
	return append(append(b, byte(len(s))), s...)
}

func takeString(b []byte) (string, []byte, error) {
	if len(b) < 1 {
		return "", nil, errors.New("the key is truncated")
	}
	n := int(b[0])
	if len(b) < 1+n {
		return "", nil, errors.New("the key is truncated")
	}
	return string(b[1 : 1+n]), b[1+n:], nil
}

// ------------------------------------------------------------- HTTP surface

func (a *App) apiLicence(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		st := a.licence.Status()
		st["installation"] = a.licence.InstallationID()
		st["buy_url"] = a.buyURL()
		writeJSON(w, 200, st)
	case "POST":
		var b struct {
			Key string `json:"key"`
		}
		if err := readJSON(r, &b); err != nil {
			errJSON(w, err)
			return
		}
		lic, err := a.licence.Activate(b.Key)
		if err != nil {
			errJSON(w, &httpError{422, err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"licensed": true, "licensee": lic.Licensee, "status": a.licence.Status()})
	default:
		w.Header().Set("Allow", "GET, POST")
		writeJSON(w, 405, map[string]any{"detail": "method not allowed"})
	}
}

// buyURL points at the licence server's checkout, carrying the installation id
// so a key can be re-sent if it is lost.
func (a *App) buyURL() string {
	base := strings.TrimRight(a.configSnapshot().LicenceServer, "/")
	if base == "" {
		return ""
	}
	return fmt.Sprintf("%s/buy?installation=%s", base, a.licence.InstallationID())
}

// requireLicence gates anything that controls the scanner.
func (a *App) requireLicence() error {
	ok, reason := a.licence.Allowed()
	if ok {
		return nil
	}
	msg := reason + ". Enter a licence key on the System page"
	if u := a.buyURL(); u != "" {
		msg += " or buy one at " + u
	}
	return &httpError{402, msg + "."}
}
