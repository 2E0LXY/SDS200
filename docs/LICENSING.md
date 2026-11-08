# Trial, licence keys and the PayPal checkout

## What the trial covers

| Area | During the 14-day trial | After it ends, unlicensed |
|---|---|---|
| Read status, display, memory browser, logs, diagnostics | yes | yes |
| Anything that writes to the scanner (keys, holds, volume, squelch, tuning, location, menus) | yes | **no** (HTTP 402) |
| Live audio, recording | yes | **no** |
| The assistant | yes | **no** |

The clock starts at first run and is stored in `licence.json` beside `config.json`,
so reinstalling the binary does not reset it. The file is plain JSON and is not
hidden or obfuscated.

## Honest limitation

The check runs on the customer's machine, and this repository is public. Anyone
willing to patch or rebuild the binary can remove it. The point is to stop
casual sharing and make buying the easy path; it is not copy protection. Don't
build a business model that assumes otherwise.

## Key format

```
SDS200-1-<base32(payload)>-<base32(Ed25519 signature)>
```

The payload is packed binary (version byte, product tag, licensee, email,
order id, issued, expires) so keys stay short enough to read out over the air.
The app embeds only the public half, so activation needs no network. The format
lives in `licencekey/` and is shared by the app and the server, so the two can
never drift apart.

## Running the shop

```sh
cd cmd/licence-server
go build -o licence-server .

# once, on the server: create the signing key
./licence-server -keygen                  # prints the public hex to embed

# paste that into licence.go:
#   var licencePublicKey = "…64 hex characters…"
# and rebuild the app. Back the private key up: lose it and no further keys
# can be issued that existing builds will accept.

export PAYPAL_CLIENT_ID=…
export PAYPAL_CLIENT_SECRET=…
./licence-server -addr 127.0.0.1:8080 -db /var/lib/sds200/licences.db
# add -live once sandbox has been tested end to end
```

Flags: `-price` (default `15.00`), `-currency` (`GBP`), `-valid-years` (0 =
perpetual), `-key`, `-db`, `-addr`, `-live`.

### Endpoints

| Method | Path | Purpose |
|---|---|---|
| GET | `/buy?installation=<id>` | checkout page (PayPal JS SDK) |
| POST | `/api/orders` | open an order at the server-set price |
| POST | `/api/orders/<id>/capture` | take payment, sign and store the key |
| GET | `/api/key?installation=…` or `?email=…` | re-send a key that was lost |
| GET | `/healthz` | purchase count and PayPal environment |

The price is never taken from the request, and the captured amount and currency
are checked against the configured price before a key is signed. Capture is
idempotent: an order that already has a key returns the same key rather than
charging again.

### Caddy in front

```caddy
licence.2e0lxy.uk {
	reverse_proxy 127.0.0.1:8080
}
```

Then set `"licence_server": "https://licence.2e0lxy.uk"` in the app's
`config.json`; the app's Buy button opens
`<licence_server>/buy?installation=<id>` so a lost key can be matched back to
the machine that bought it.

### Storage

SQLite, one row per purchase: order id, capture id, installation id, licensee,
email, the issued key, amount and currency. Driver is `modernc.org/sqlite`
(pure Go, no cgo), so the server cross-compiles like the app.

## Still outstanding

- PayPal client id and secret (sandbox first) and the host name for the server.
- Emailing the key on purchase — the page shows it and `/api/key` re-sends it,
  but nothing is posted out yet.
- A webhook listener for `PAYMENT.CAPTURE.COMPLETED`, for the case where the
  buyer closes the tab between capture and the page rendering the key.
