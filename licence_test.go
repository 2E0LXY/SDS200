package main

import (
	"crypto/ed25519"
	"encoding/hex"

	"sds200webapp/licencekey"
	"strings"
	"testing"
	"time"
)

// withTestKeys installs a throwaway key pair for the duration of a test.
func withTestKeys(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	old := licencePublicKey
	licencePublicKey = hex.EncodeToString(pub)
	t.Cleanup(func() { licencePublicKey = old })
	return priv
}

func TestSignAndVerifyKey(t *testing.T) {
	priv := withTestKeys(t)
	want := Licence{Licensee: "A. Operator", Email: "a@example.com", Issued: time.Now().Truncate(time.Second), Order: "5O190127TN364715T"}
	key, err := licencekey.Sign(priv, want)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, licencekey.Prefix+"-") {
		t.Fatalf("key has the wrong prefix: %q", key)
	}
	got, err := VerifyKey(strings.ToLower(key))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.Licensee != want.Licensee || got.Email != want.Email || got.Order != want.Order {
		t.Fatalf("round trip lost fields: %+v", got)
	}
	if got.Product != licencekey.ProductTag {
		t.Fatalf("product tag = %q", got.Product)
	}
	if !got.Expires.IsZero() {
		t.Fatalf("a perpetual licence must decode with a zero expiry, got %v", got.Expires)
	}
}

func TestVerifyKeyRejectsTampering(t *testing.T) {
	priv := withTestKeys(t)
	key, err := licencekey.Sign(priv, Licence{Licensee: "A. Operator", Issued: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	// Flip a character in the payload half.
	parts := strings.Split(key, "-")
	body := []byte(parts[2])
	if body[0] == 'A' {
		body[0] = 'B'
	} else {
		body[0] = 'A'
	}
	parts[2] = string(body)
	if _, err := VerifyKey(strings.Join(parts, "-")); err == nil {
		t.Fatal("a tampered key was accepted")
	}
}

func TestVerifyKeyNeedsAPublicKey(t *testing.T) {
	old := licencePublicKey
	licencePublicKey = ""
	t.Cleanup(func() { licencePublicKey = old })
	if _, err := VerifyKey(licencekey.Prefix + "-AAAA-AAAA"); err == nil {
		t.Fatal("keys must not verify in a build with no public key")
	}
}

func TestTrialAllowsThenExpires(t *testing.T) {
	dir := t.TempDir()
	m := NewLicenceManager(dir, nil)
	if ok, why := m.Allowed(); !ok {
		t.Fatalf("a fresh install must be inside the trial: %s", why)
	}
	// Reopening must not restart the trial.
	first := m.TrialEnds()
	if again := NewLicenceManager(dir, nil).TrialEnds(); !again.Equal(first) {
		t.Fatalf("trial end moved on reopen: %v -> %v", first, again)
	}
	m.state.FirstRun = time.Now().AddDate(0, 0, -(trialDays + 1))
	ok, why := m.Allowed()
	if ok {
		t.Fatal("an expired trial must not allow control")
	}
	if !strings.Contains(why, "trial ended") {
		t.Fatalf("unhelpful reason: %q", why)
	}
}

func TestActivatedLicenceOutlivesTheTrial(t *testing.T) {
	priv := withTestKeys(t)
	dir := t.TempDir()
	m := NewLicenceManager(dir, nil)
	m.state.FirstRun = time.Now().AddDate(0, 0, -99)
	key, err := licencekey.Sign(priv, Licence{Licensee: "A. Operator", Issued: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Activate(key); err != nil {
		t.Fatal(err)
	}
	if ok, why := m.Allowed(); !ok {
		t.Fatalf("activated licence refused: %s", why)
	}
	// And it survives a restart, because the key is stored.
	if ok, why := NewLicenceManager(dir, nil).Allowed(); !ok {
		t.Fatalf("stored key not reloaded: %s", why)
	}
	if st := m.Status(); st["licensed"] != true || st["licensee"] != "A. Operator" {
		t.Fatalf("status does not report the licence: %+v", st)
	}
}

func TestExpiredLicenceIsRefused(t *testing.T) {
	priv := withTestKeys(t)
	m := NewLicenceManager(t.TempDir(), nil)
	m.state.FirstRun = time.Now().AddDate(0, 0, -99)
	key, err := licencekey.Sign(priv, Licence{Licensee: "A. Operator", Issued: time.Now().AddDate(-1, 0, 0), Expires: time.Now().AddDate(0, 0, -1)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Activate(key); err != nil {
		t.Fatal(err)
	}
	ok, why := m.Allowed()
	if ok || !strings.Contains(why, "expired") {
		t.Fatalf("expired licence accepted (%v, %q)", ok, why)
	}
}
