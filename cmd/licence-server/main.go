// Command licence-server sells and issues SDS200 WebApp licence keys.
//
//	licence-server -keygen                  write a signing key, print the public half
//	licence-server -addr :8080 -db keys.db  run the shop
//
// It serves three things:
//
//	GET  /buy?installation=<id>   the checkout page (PayPal JS SDK)
//	POST /api/orders              open a PayPal order for the licence price
//	POST /api/orders/<id>/capture take the money, issue and store the key
//	GET  /api/key?installation=   re-send a key that was bought and lost
//
// The private signing key never leaves this machine. Keys are checked offline
// by the app against the public half embedded in its binary.
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sds200webapp/licencekey"
)

type config struct {
	addr       string
	dbPath     string
	privPath   string
	clientID   string
	secret     string
	live       bool
	price      string
	currency   string
	validYears int
}

type server struct {
	cfg  config
	priv ed25519.PrivateKey
	pp   *payPal
	st   *store
}

func main() {
	var cfg config
	var keygen bool
	flag.StringVar(&cfg.addr, "addr", envOr("LICENCE_ADDR", "127.0.0.1:8080"), "listen address (put Caddy in front for TLS)")
	flag.StringVar(&cfg.dbPath, "db", envOr("LICENCE_DB", "licences.db"), "SQLite database path")
	flag.StringVar(&cfg.privPath, "key", envOr("LICENCE_KEY_FILE", "licence-signing.key"), "Ed25519 private key file")
	flag.StringVar(&cfg.clientID, "paypal-client-id", os.Getenv("PAYPAL_CLIENT_ID"), "PayPal REST app client id")
	flag.StringVar(&cfg.secret, "paypal-secret", os.Getenv("PAYPAL_CLIENT_SECRET"), "PayPal REST app secret")
	flag.BoolVar(&cfg.live, "live", os.Getenv("PAYPAL_ENV") == "live", "use PayPal live instead of sandbox")
	flag.StringVar(&cfg.price, "price", envOr("LICENCE_PRICE", "15.00"), "licence price")
	flag.StringVar(&cfg.currency, "currency", envOr("LICENCE_CURRENCY", "GBP"), "ISO currency code")
	flag.IntVar(&cfg.validYears, "valid-years", 0, "licence lifetime in years; 0 means perpetual")
	flag.BoolVar(&keygen, "keygen", false, "generate a signing key and exit")
	flag.Parse()

	if keygen {
		if err := generateKey(cfg.privPath); err != nil {
			log.Fatal(err)
		}
		return
	}
	priv, err := loadKey(cfg.privPath)
	if err != nil {
		log.Fatalf("%v\n\nRun \"licence-server -keygen\" first.", err)
	}
	if cfg.clientID == "" || cfg.secret == "" {
		log.Fatal("set -paypal-client-id and -paypal-secret (or PAYPAL_CLIENT_ID / PAYPAL_CLIENT_SECRET)")
	}
	st, err := openStore(cfg.dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	s := &server{cfg: cfg, priv: priv, pp: newPayPal(cfg.live, cfg.clientID, cfg.secret), st: st}
	mux := http.NewServeMux()
	mux.HandleFunc("/buy", s.handleBuy)
	mux.HandleFunc("/api/orders", s.handleCreateOrder)
	mux.HandleFunc("/api/orders/", s.handleCapture)
	mux.HandleFunc("/api/key", s.handleRecoverKey)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		n, err := st.Count()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "purchases": n, "paypal": s.payPalEnv()})
	})

	mode := "sandbox"
	if cfg.live {
		mode = "LIVE"
	}
	log.Printf("licence-server on %s, PayPal %s, %s %s per licence, db %s", cfg.addr, mode, cfg.price, cfg.currency, cfg.dbPath)
	srv := &http.Server{Addr: cfg.addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

func (s *server) payPalEnv() string {
	if s.cfg.live {
		return "live"
	}
	return "sandbox"
}

func envOr(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

// ------------------------------------------------------------- signing key

func generateKey(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists; move it aside before generating a new key (keys signed with the old one would stop verifying)", path)
	}
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o700)
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(priv)+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Printf("Private key written to %s (keep it; back it up; never ship it).\n\n", path)
	fmt.Printf("Put this in licence.go before building the app:\n\n")
	fmt.Printf("var licencePublicKey = %q\n", hex.EncodeToString(pub))
	return nil
}

func loadKey(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read signing key: %w", err)
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, fmt.Errorf("signing key is not hex: %w", err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("signing key is %d bytes, want %d", len(raw), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(raw), nil
}

// --------------------------------------------------------------- endpoints

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"detail": msg})
}

// handleCreateOrder opens a PayPal order. The price is fixed here, never read
// from the request.
func (s *server) handleCreateOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		fail(w, 405, "method not allowed")
		return
	}
	var b struct {
		Installation string `json:"installation"`
		Licensee     string `json:"licensee"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&b)
	inst := cleanID(b.Installation)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	order, err := s.pp.CreateOrder(ctx, s.cfg.price, s.cfg.currency, inst, "order-"+inst+"-"+time.Now().UTC().Format("20060102T150405"))
	if err != nil {
		log.Printf("create order failed: %v", err)
		fail(w, 502, "PayPal would not open the order. Try again in a moment.")
		return
	}
	writeJSON(w, 200, map[string]any{"id": order.ID, "status": order.Status})
}

// handleCapture takes the money and issues the key. It is safe to call twice:
// an already-captured order is re-read and the stored key returned.
func (s *server) handleCapture(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		fail(w, 405, "method not allowed")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/orders/")
	orderID := cleanID(strings.TrimSuffix(rest, "/capture"))
	if orderID == "" || !strings.HasSuffix(rest, "/capture") {
		fail(w, 404, "not found")
		return
	}
	// Already issued: hand the same key back rather than charging again.
	if p, err := s.st.ByOrder(orderID); err == nil {
		writeJSON(w, 200, keyResponse(p))
		return
	}
	var b struct {
		Licensee string `json:"licensee"`
		Email    string `json:"email"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&b)

	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	order, err := s.pp.CaptureOrder(ctx, orderID)
	if err != nil {
		// A retried capture, or a capture that raced a webhook, shows up as an
		// error; the order itself is the truth, so re-read it.
		log.Printf("capture %s: %v", orderID, err)
		order, err = s.pp.GetOrder(ctx, orderID)
		if err != nil {
			fail(w, 502, "The payment could not be confirmed with PayPal. Nothing has been charged twice; contact support with order "+orderID+".")
			return
		}
	}
	captureID, ok := order.completedCapture(s.cfg.price, s.cfg.currency)
	if !ok {
		fail(w, 402, "PayPal has not completed a "+s.cfg.price+" "+s.cfg.currency+" payment for this order (status "+order.Status+").")
		return
	}
	licensee := strings.TrimSpace(b.Licensee)
	if licensee == "" {
		licensee = order.payerName()
	}
	if licensee == "" {
		licensee = "SDS200 operator"
	}
	email := strings.TrimSpace(b.Email)
	if email == "" {
		email = order.Payer.EmailAddress
	}
	lic := licencekey.Licence{
		Licensee: licensee,
		Email:    email,
		Issued:   time.Now().UTC().Truncate(time.Second),
		Order:    order.ID,
	}
	if s.cfg.validYears > 0 {
		lic.Expires = lic.Issued.AddDate(s.cfg.validYears, 0, 0)
	}
	key, err := licencekey.Sign(s.priv, lic)
	if err != nil {
		log.Printf("sign key for %s: %v", order.ID, err)
		fail(w, 500, "The payment went through but the key could not be signed. Contact support with order "+order.ID+".")
		return
	}
	p := purchase{
		OrderID: order.ID, CaptureID: captureID, Installation: order.installation(),
		Licensee: licensee, Email: email, Key: key,
		Amount: s.cfg.price, Currency: s.cfg.currency, Created: time.Now().UTC(),
	}
	if err := s.st.Put(p); err != nil {
		// The key is valid whether or not it was stored, so still return it.
		log.Printf("store purchase %s: %v", order.ID, err)
	}
	log.Printf("issued licence for order %s (installation %q, %s)", order.ID, p.Installation, licensee)
	writeJSON(w, 200, keyResponse(&p))
}

func keyResponse(p *purchase) map[string]any {
	return map[string]any{
		"key":      p.Key,
		"licensee": p.Licensee,
		"order":    p.OrderID,
		"issued":   p.Created.Format("2006-01-02"),
	}
}

// handleRecoverKey re-sends a key to someone who lost it, matched on the
// installation id the app shows, or on the PayPal email.
func (s *server) handleRecoverKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.Header().Set("Allow", "GET")
		fail(w, 405, "method not allowed")
		return
	}
	q := r.URL.Query()
	var p *purchase
	var err error
	if inst := cleanID(q.Get("installation")); inst != "" {
		p, err = s.st.LatestByInstallation(inst)
	} else if email := strings.TrimSpace(q.Get("email")); email != "" {
		p, err = s.st.LatestByEmail(email)
	} else {
		fail(w, 422, "give an installation id or the PayPal email the licence was bought with")
		return
	}
	if errors.Is(err, errNotFound) {
		fail(w, 404, "no licence has been bought with that installation id or email")
		return
	}
	if err != nil {
		fail(w, 500, "lookup failed")
		return
	}
	writeJSON(w, 200, keyResponse(p))
}

// cleanID keeps only characters PayPal ids and installation ids use, so
// nothing from the query string reaches a URL path or the database raw.
func cleanID(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 64 {
		s = s[:64]
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9', c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c == '-', c == '_':
			out = append(out, c)
		}
	}
	return string(out)
}
