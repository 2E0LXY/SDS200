package main

import (
	"path/filepath"
	"testing"
	"time"

	"sds200webapp/licencekey"
)

func TestStoreRoundTrip(t *testing.T) {
	st, err := openStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p := purchase{OrderID: "5O1", CaptureID: "3C4", Installation: "abc123", Licensee: "A. Operator",
		Email: "A@Example.com", Key: "SDS200-1-AAA-BBB", Amount: "15.00", Currency: "GBP", Created: time.Now()}
	if err := st.Put(p); err != nil {
		t.Fatal(err)
	}
	// Re-inserting the same order must not create a second row.
	if err := st.Put(p); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.Count(); n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
	for _, get := range []func() (*purchase, error){
		func() (*purchase, error) { return st.ByOrder("5O1") },
		func() (*purchase, error) { return st.LatestByInstallation("abc123") },
		func() (*purchase, error) { return st.LatestByEmail("a@example.com") },
	} {
		got, err := get()
		if err != nil {
			t.Fatal(err)
		}
		if got.Key != p.Key || got.Licensee != p.Licensee {
			t.Fatalf("round trip lost data: %+v", got)
		}
	}
	if _, err := st.LatestByInstallation("nope"); err != errNotFound {
		t.Fatalf("missing installation gave %v", err)
	}
}

func TestKeygenAndSignedKeyVerifies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k.key")
	if err := generateKey(path); err != nil {
		t.Fatal(err)
	}
	if err := generateKey(path); err == nil {
		t.Fatal("keygen overwrote an existing signing key")
	}
	priv, err := loadKey(path)
	if err != nil {
		t.Fatal(err)
	}
	key, err := licencekey.Sign(priv, licencekey.Licence{Licensee: "A. Operator", Issued: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	pubHex := hexOf(priv)
	if _, err := licencekey.Verify(pubHex, key); err != nil {
		t.Fatalf("a freshly signed key did not verify: %v", err)
	}
}

func hexOf(priv []byte) string {
	// The public half is the last 32 bytes of an Ed25519 private key.
	const hexDigits = "0123456789abcdef"
	pub := priv[32:]
	out := make([]byte, 0, 64)
	for _, b := range pub {
		out = append(out, hexDigits[b>>4], hexDigits[b&0xf])
	}
	return string(out)
}

func TestMoneyComparisonIsExact(t *testing.T) {
	for _, c := range []struct {
		a, b string
		same bool
	}{
		{"15.00", "15", true}, {"15.0", "15.00", true}, {"015.00", "15.00", true},
		{"15.00", "1.50", false}, {"150.00", "15.00", false}, {"15.01", "15.00", false},
	} {
		if got := sameMoney(c.a, c.b); got != c.same {
			t.Errorf("sameMoney(%q,%q) = %v", c.a, c.b, got)
		}
	}
}

func TestCleanIDStripsAnythingUnexpected(t *testing.T) {
	if got := cleanID("  5O19/../127TN3 64715T\n"); got != "5O19127TN364715T" {
		t.Fatalf("cleanID = %q", got)
	}
	if got := cleanID("a_b-c"); got != "a_b-c" {
		t.Fatalf("cleanID dropped valid characters: %q", got)
	}
}

func TestCompletedCaptureChecksAmountAndCurrency(t *testing.T) {
	mk := func(status, cur, val string) *payPalOrder {
		var o payPalOrder
		o.PurchaseUnits = make([]struct {
			CustomID string `json:"custom_id"`
			Amount   struct {
				CurrencyCode string `json:"currency_code"`
				Value        string `json:"value"`
			} `json:"amount"`
			Payments struct {
				Captures []struct {
					ID     string `json:"id"`
					Status string `json:"status"`
					Amount struct {
						CurrencyCode string `json:"currency_code"`
						Value        string `json:"value"`
					} `json:"amount"`
				} `json:"captures"`
			} `json:"payments"`
		}, 1)
		c := struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Amount struct {
				CurrencyCode string `json:"currency_code"`
				Value        string `json:"value"`
			} `json:"amount"`
		}{ID: "CAP1", Status: status}
		c.Amount.CurrencyCode = cur
		c.Amount.Value = val
		o.PurchaseUnits[0].Payments.Captures = append(o.PurchaseUnits[0].Payments.Captures, c)
		return &o
	}
	if _, ok := mk("COMPLETED", "GBP", "15.00").completedCapture("15.00", "GBP"); !ok {
		t.Fatal("a completed £15 capture was rejected")
	}
	for _, bad := range []*payPalOrder{
		mk("PENDING", "GBP", "15.00"),
		mk("COMPLETED", "USD", "15.00"),
		mk("COMPLETED", "GBP", "1.50"),
	} {
		if _, ok := bad.completedCapture("15.00", "GBP"); ok {
			t.Fatalf("accepted a capture it should not have: %+v", bad.PurchaseUnits[0].Payments.Captures)
		}
	}
}
