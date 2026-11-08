package main

// The checkout page. One self-contained file served from the binary: the only
// outside script is PayPal's own SDK, which has to come from PayPal.

import (
	"html/template"
	"net/http"
	"strings"
)

var buyPage = template.Must(template.New("buy").Parse(`<!doctype html>
<html lang="en-GB"><head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>SDS200 WebApp licence</title>
<style>
 :root{color-scheme:light dark;--bg:#0f1115;--fg:#e8eaed;--mut:#9aa0a6;--card:#171a20;--line:#2a2f38;--acc:#7cc4ff}
 @media (prefers-color-scheme:light){:root{--bg:#f6f7f9;--fg:#15181d;--mut:#5a6069;--card:#fff;--line:#dfe2e7;--acc:#005a9c}}
 *{box-sizing:border-box}
 body{margin:0;padding:24px 16px;background:var(--bg);color:var(--fg);font:16px/1.5 system-ui,-apple-system,Segoe UI,Roboto,sans-serif}
 main{max-width:620px;margin:0 auto}
 h1{font-size:1.5rem;margin:0 0 4px}
 p.sub{color:var(--mut);margin:0 0 20px}
 .card{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:18px;margin-bottom:16px}
 label{display:block;font-size:.85rem;color:var(--mut);margin:12px 0 4px}
 input{width:100%;padding:10px;border:1px solid var(--line);border-radius:6px;background:var(--bg);color:var(--fg);font:inherit}
 .price{font-size:1.6rem;font-weight:600}
 .key{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:1rem;word-break:break-all;background:var(--bg);border:1px dashed var(--acc);border-radius:6px;padding:12px;margin:12px 0}
 .err{color:#ff8a80}
 .ok{color:#81c995}
 .mut{color:var(--mut);font-size:.9rem}
 button.link{background:none;border:0;color:var(--acc);cursor:pointer;padding:0;font:inherit;text-decoration:underline}
 #paypal{margin-top:16px;min-height:48px}
</style></head><body><main>
<h1>SDS200 WebApp licence</h1>
<p class="sub">One payment, one machine, no expiry. The trial keeps working until it runs out.</p>

<div class="card" id="buyCard">
  <div class="price">{{.Symbol}}{{.Price}} <span class="mut">{{.Currency}}</span></div>
  <label for="licensee">Name on the licence</label>
  <input id="licensee" autocomplete="name" placeholder="Your name or callsign">
  <label for="installation">Installation id <span class="mut">(from the System page in the app)</span></label>
  <input id="installation" value="{{.Installation}}" spellcheck="false">
  <div id="paypal"></div>
  <p class="mut" id="status"></p>
</div>

<div class="card" id="keyCard" hidden>
  <p class="ok"><strong>Paid. Here is your key.</strong></p>
  <div class="key" id="key"></div>
  <p class="mut">Copy it into <em>System → Licence</em> in the app and press Activate. Keep it somewhere safe; it is also tied to the installation id above, so it can be re-sent.</p>
  <button id="copy">Copy key</button>
</div>

<div class="card">
  <p class="mut">Already bought one and lost it? <button class="link" id="recover">Re-send my key</button></p>
  <p class="mut" id="recoverStatus"></p>
</div>

<p class="mut">Payments are taken by PayPal ({{.Env}}). This page never sees your card details.</p>
</main>
<script src="https://www.paypal.com/sdk/js?client-id={{.ClientID}}&currency={{.Currency}}&intent=capture&disable-funding=credit"></script>
<script>
const $ = s => document.querySelector(s);
const status = m => { $("#status").textContent = m; };
function showKey(d){
  $("#key").textContent = d.key;
  $("#keyCard").hidden = false;
  $("#buyCard").hidden = true;
}
$("#copy").onclick = () => navigator.clipboard.writeText($("#key").textContent).then(() => $("#copy").textContent = "Copied");
$("#recover").onclick = async () => {
  const inst = $("#installation").value.trim();
  const q = inst ? "installation=" + encodeURIComponent(inst) : "";
  if (!q) { $("#recoverStatus").textContent = "Enter your installation id above first."; return; }
  $("#recoverStatus").textContent = "Looking…";
  const r = await fetch("/api/key?" + q);
  const d = await r.json().catch(() => ({}));
  if (!r.ok) { $("#recoverStatus").textContent = d.detail || "Not found."; return; }
  $("#recoverStatus").textContent = "";
  showKey(d);
};
if (window.paypal) {
  paypal.Buttons({
    style: { layout: "vertical", label: "paypal" },
    createOrder: async () => {
      status("Opening the order…");
      const r = await fetch("/api/orders", {
        method: "POST", headers: {"Content-Type": "application/json"},
        body: JSON.stringify({ installation: $("#installation").value.trim(), licensee: $("#licensee").value.trim() })
      });
      const d = await r.json();
      if (!r.ok) { status(d.detail || "Could not start the payment."); throw new Error(d.detail); }
      status("");
      return d.id;
    },
    onApprove: async (data) => {
      status("Taking the payment and signing your key…");
      const r = await fetch("/api/orders/" + encodeURIComponent(data.orderID) + "/capture", {
        method: "POST", headers: {"Content-Type": "application/json"},
        body: JSON.stringify({ licensee: $("#licensee").value.trim() })
      });
      const d = await r.json();
      if (!r.ok) { status(d.detail || "The payment did not complete."); return; }
      status("");
      showKey(d);
    },
    onError: (e) => status("PayPal reported a problem: " + e)
  }).render("#paypal");
} else {
  status("PayPal's checkout script did not load, so nothing can be bought right now.");
}
</script></body></html>`))

func (s *server) handleBuy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_ = buyPage.Execute(w, map[string]any{
		"Price":        s.cfg.price,
		"Currency":     strings.ToUpper(s.cfg.currency),
		"Symbol":       currencySymbol(s.cfg.currency),
		"ClientID":     s.cfg.clientID,
		"Installation": cleanID(r.URL.Query().Get("installation")),
		"Env":          s.payPalEnv(),
	})
}

func currencySymbol(code string) string {
	switch strings.ToUpper(code) {
	case "GBP":
		return "£"
	case "USD":
		return "$"
	case "EUR":
		return "€"
	}
	return ""
}
