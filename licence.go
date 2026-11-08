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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"sds200webapp/licencekey"
)

const trialDays = 14

// licencePublicKey verifies keys issued by the licence server. Replace this
// with the public half printed by "licence-server -keygen" before shipping.
var licencePublicKey = "" // hex-encoded Ed25519 public key; empty disables key checks

// Licence is the payload inside a key; the format lives in licencekey so the
// licence server can sign what this verifies.
type Licence = licencekey.Licence

// VerifyKey checks a key's signature against the embedded public key.
func VerifyKey(key string) (*Licence, error) { return licencekey.Verify(licencePublicKey, key) }

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
		"licensed":   lic != nil,
		"allowed":    ok,
		"first_run":  first.Format(time.RFC3339),
		"trial_days": trialDays,
		"trial_ends": m.TrialEnds().Format(time.RFC3339),
		// Round up: on the first day the operator should be told 14 days, not 13.
		"days_left":    int(math.Ceil(time.Until(m.TrialEnds()).Hours() / 24)),
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
