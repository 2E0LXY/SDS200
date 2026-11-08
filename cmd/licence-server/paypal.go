package main

// PayPal Orders v2, the server half of the "advanced" checkout integration:
// the browser loads PayPal's JS SDK and calls our two endpoints, we talk to
// PayPal with the app's client id and secret.
//
//	https://developer.paypal.com/studio/checkout/advanced/integrate
//
// Nothing here trusts the browser: the amount is set server-side and the
// captured amount is checked again before a key is issued.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	paypalSandbox = "https://api-m.sandbox.paypal.com"
	paypalLive    = "https://api-m.paypal.com"
)

type payPal struct {
	base         string
	clientID     string
	clientSecret string
	client       *http.Client

	mu        sync.Mutex
	token     string
	tokenTill time.Time
}

func newPayPal(live bool, clientID, clientSecret string) *payPal {
	base := paypalSandbox
	if live {
		base = paypalLive
	}
	return &payPal{base: base, clientID: clientID, clientSecret: clientSecret, client: &http.Client{Timeout: 25 * time.Second}}
}

// accessToken fetches and caches an OAuth2 token.
func (p *payPal) accessToken(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.token != "" && time.Now().Before(p.tokenTill) {
		return p.token, nil
	}
	form := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, "POST", p.base+"/v1/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(p.clientID, p.clientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("paypal token: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != 200 {
		return "", fmt.Errorf("paypal token: HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(raw)))
	}
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", fmt.Errorf("paypal token: bad JSON: %w", err)
	}
	if body.AccessToken == "" {
		return "", fmt.Errorf("paypal token: empty response")
	}
	p.token = body.AccessToken
	ttl := time.Duration(body.ExpiresIn) * time.Second
	if ttl < time.Minute {
		ttl = 5 * time.Minute
	}
	p.tokenTill = time.Now().Add(ttl - 30*time.Second)
	return p.token, nil
}

// do makes an authenticated Orders v2 call. requestID makes POSTs idempotent,
// so a retried create or capture cannot take a second payment.
func (p *payPal) do(ctx context.Context, method, path, requestID string, in, out any) error {
	tok, err := p.accessToken(ctx)
	if err != nil {
		return err
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	if requestID != "" {
		req.Header.Set("PayPal-Request-Id", requestID)
	}
	res, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("paypal %s %s: %w", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode >= 300 {
		return fmt.Errorf("paypal %s %s: HTTP %d: %s", method, path, res.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("paypal %s %s: bad JSON: %w", method, path, err)
	}
	return nil
}

// payPalOrder is the part of an order we care about.
type payPalOrder struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	PurchaseUnits []struct {
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
	} `json:"purchase_units"`
	Payer struct {
		EmailAddress string `json:"email_address"`
		Name         struct {
			GivenName string `json:"given_name"`
			Surname   string `json:"surname"`
		} `json:"name"`
	} `json:"payer"`
}

// CreateOrder opens an order for one licence. installation is carried as
// custom_id so a capture can be tied back to the machine that bought it.
func (p *payPal) CreateOrder(ctx context.Context, price, currency, installation, requestID string) (*payPalOrder, error) {
	in := map[string]any{
		"intent": "CAPTURE",
		"purchase_units": []map[string]any{{
			"description": "SDS200 WebApp licence",
			"custom_id":   installation,
			"amount":      map[string]string{"currency_code": currency, "value": price},
		}},
		"payment_source": map[string]any{
			"paypal": map[string]any{
				"experience_context": map[string]any{
					"shipping_preference": "NO_SHIPPING",
					"user_action":         "PAY_NOW",
				},
			},
		},
	}
	var out payPalOrder
	if err := p.do(ctx, "POST", "/v2/checkout/orders", requestID, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CaptureOrder takes the money. A 422 ORDER_ALREADY_CAPTURED is reported as
// an error; the caller then re-reads the order with GetOrder.
func (p *payPal) CaptureOrder(ctx context.Context, orderID string) (*payPalOrder, error) {
	var out payPalOrder
	if err := p.do(ctx, "POST", "/v2/checkout/orders/"+url.PathEscape(orderID)+"/capture", "capture-"+orderID, map[string]any{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (p *payPal) GetOrder(ctx context.Context, orderID string) (*payPalOrder, error) {
	var out payPalOrder
	if err := p.do(ctx, "GET", "/v2/checkout/orders/"+url.PathEscape(orderID), "", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// completedCapture returns the capture that paid for the licence, if the order
// holds one that is COMPLETED for the expected amount.
func (o *payPalOrder) completedCapture(price, currency string) (id string, ok bool) {
	for _, pu := range o.PurchaseUnits {
		for _, c := range pu.Payments.Captures {
			if !strings.EqualFold(c.Status, "COMPLETED") {
				continue
			}
			if !strings.EqualFold(c.Amount.CurrencyCode, currency) {
				continue
			}
			if !sameMoney(c.Amount.Value, price) {
				continue
			}
			return c.ID, true
		}
	}
	return "", false
}

func (o *payPalOrder) installation() string {
	for _, pu := range o.PurchaseUnits {
		if pu.CustomID != "" {
			return pu.CustomID
		}
	}
	return ""
}

func (o *payPalOrder) payerName() string {
	n := strings.TrimSpace(o.Payer.Name.GivenName + " " + o.Payer.Name.Surname)
	return n
}

// sameMoney compares two decimal money strings without floating point.
func sameMoney(a, b string) bool {
	return normaliseMoney(a) == normaliseMoney(b)
}

func normaliseMoney(s string) string {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, ".") {
		s += ".00"
	}
	parts := strings.SplitN(s, ".", 2)
	whole := strings.TrimLeft(parts[0], "0")
	if whole == "" {
		whole = "0"
	}
	frac := (parts[1] + "00")[:2]
	return whole + "." + frac
}
