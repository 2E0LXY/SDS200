# Uniden SDS100 / SDS200 / SDS150 — Remote Command Protocol Reference

Implementation reference for a complete control client. Assembled from:

| Tag | Source |
|---|---|
| **V0.01** | `SDS200_RemoteCommand_Specification_V0_01.pdf`, "SDSx00 Remote Command Specification", Version 0.01, 2018‑04‑13, 34 pp. (`/home/claude/sds_docs/rcs001.pdf`) |
| **V2.00** | `SDS_Series_RemoteCommand_Specification_V2_00.pdf`, "SDS100 (UB383Z) / SDS200 (UB384Z) / SDS150 (UB391Z) Remote Command Specification", Version 2.00, 2025‑07‑07, 37 pp. (`/home/claude/sds_docs/rcs200.pdf`) |
| **HW** | Live testing against an SDS200E running firmware 1.23.15 — see §14. **HW observations override both specs where they conflict.** |
| **OP** | Operational knowledge not present in either PDF (chiefly the network transport). Marked explicitly wherever used. |

Both PDFs are marked "For Internal Use Only" and are derived from the BCD536HP/BCD436HP Remote Command Specification V1.05; several artefacts of that ancestry survive and are flagged below.

Revision history recorded in V2.00:

| Date | Version | Contents |
|---|---|---|
| 2018‑04‑13 | 0.01 | Created new, based on BCD536HP/BCD436HP Remote Command Specification V1.05 |
| 2023‑12‑11 | 1.01 | Append Waterfall |
| 2023‑12‑22 | 1.02 | Append GST command |
| 2024‑12‑04 | 1.03 | Append POF/GCS command |
| 2025‑07‑07 | 2.00 | Appended GW2 command; appended KAL command |

Throughout: `[\r]` is a single carriage return, 0x0D. `\r` in example payloads means the same byte. Neither spec ever uses LF as a terminator (the sole exception is the `GCS` response, which the spec terminates with `\n`).

---

## 1. Transport

Neither PDF documents the link layer; everything in this section is **OP** plus **HW** unless stated.

### 1.1 Datagram transport

| Property | Value |
|---|---|
| Protocol | UDP |
| Port | **50536** (scanner listens; replies are sent back to the source address/port of the request) |
| Address | SDS200: the unit's wired-Ethernet or Wi‑Fi address. SDS100/SDS150: the Wi‑Fi address when "Wi‑Fi Setup → Scanner Mode" is active |
| Encoding | 7‑bit ASCII; bytes 0x20–0x7E in text fields |
| Framing | One command per datagram, terminated by a single CR (0x0D). No LF, no NUL |
| Model | Strict request/response. The scanner answers every command except `KAL` |

Serial (USB CDC) carries the identical command set; the only documented difference is `AST,RAW_DATA_OUTPUT`, which the spec says is **USB‑only**.

### 1.2 One outstanding command

The scanner services a single command at a time. A client must not pipeline: send a request, wait for the complete response (including all continuation datagrams, §1.4), then send the next. Issuing a second command while a response is still being emitted produces interleaved or dropped datagrams.

Practical consequences:

- A poll loop (`GSI`, `GST`, `GWF`) must be serialised against user-initiated commands from the same client.
- `PSI` and the `AST` streaming modes break the request/response model: the scanner emits unsolicited datagrams. A client that enables them must be able to demultiplex unsolicited `PSI,`/`AST,` datagrams from the response it is waiting for.
- Suggested timeout: 500 ms for scalar commands, 2000 ms for `GLT`/`GSI`/`MSI` (XML) commands, with one retry.

### 1.3 The `CMD,<XML>,` marker datagram

Commands whose payload is XML (`GSI`, `PSI`, `GLT`, `MSI`, and the XML `AST` modes) do **not** put the XML in the same datagram as the command echo. The scanner first sends a short marker datagram:

```
GLT,<XML>,\r
```

— the command token, a comma, the literal five-character ASCII text `<XML>`, a comma, CR. The XML document itself follows in one or more subsequent datagrams. The spec's own examples show this explicitly:

```
Controller → Radio   GLT,FL\r
Radio → Controller   GLT,<XML>,\r
                     <?xml version="1.0" encoding="utf-8"?>\r
                     <GLT>\r
                       <FL Index="0" Name="Favorites List 1" Monitor="On" Q_Key="1" N_Tag="None" />\r
                       ...
                     </GLT>\r
```

and for `MSI`:

```
MSI,<XML>,\r
<?xml version="1.0" encoding="utf-8"?>\r
<MSI Name=" Title " Index="xxxxxx " >\r
  :
</MSI>\r
```

and for `AST`:

```
AST,<XML>,\r
<?xml version="1.0" encoding="utf-8"?>\r
<AST>\r
  ...
</AST>\r
```

Parser rule: on receiving `<CMD>,<XML>,` treat the command as accepted and switch to XML-accumulation mode for that command. Accumulate until the closing root tag (`</GLT>`, `</MSI>`, `</AST>`, `</ScannerInfo>`) is seen, or until a `<Footer …EOT="1"…/>` is seen (§1.4).

### 1.4 Fragmentation of long lists — `<Footer No= EOT=>`

A single UDP datagram cannot carry an arbitrarily long list. `GLT` responses for large favourites lists, department lists, TGID lists and record-file lists are split across datagrams. Each datagram of a split response carries a footer element as its last line:

```
<Footer No="0" EOT="0" />
<Footer No="1" EOT="0" />
...
<Footer No="n" EOT="1" />
```

| Attribute | Meaning |
|---|---|
| `No` | Zero-based sequence number of this fragment within the current response |
| `EOT` | End Of Transmission. `0` = more fragments follow; `1` = this is the last fragment |

Reassembly algorithm:

1. On `<CMD>,<XML>,` start a buffer and set `expected = 0`.
2. For each subsequent datagram, strip and parse the trailing `<Footer .../>`.
3. If `No != expected`, the response is corrupt — discard the buffer and re-issue the command.
4. Append the datagram body (everything before the footer) to the buffer; `expected++`.
5. When `EOT="1"`, the buffer holds the complete document. Note that the XML declaration appears only in fragment 0, and the root element's closing tag only in the final fragment, so the fragments must be concatenated *before* parsing, not parsed individually.

Short responses may arrive with no footer at all (single-datagram case). A client must tolerate both.

### 1.5 Field escaping

Two escaping rules apply to comma-delimited (non‑XML) responses:

- **Comma → TAB.** If a text field would contain a comma, the scanner substitutes a TAB (0x09). Stated for `GST` Note 2 and for `MSV` (the client must apply the same substitution in the *outbound* direction: "Replace comma(,) to tab(\t), if value contain ,(comma)").
- **All-space → empty.** In `GST`, if `Lx_CHAR` or `Lx_MODE` is entirely spaces (0x20) it is emitted as an empty field. (`STS` does **not** do this; `STS` pads to fixed length.)

In XML payloads the usual XML entities are used: `&amp;`, `&quot;`, `&#xD;`. The spec's examples also show a literal two-character `\n` inside `PopupScreen Text` attributes as a line separator, so a client must handle both `&#xD;` and literal `\n`.

### 1.6 Error responses

The specs define remarkably few error forms:

| Form | Used by | Meaning |
|---|---|---|
| `<CMD>,OK\r` | most setters | Accepted |
| `<CMD>,NG\r` | `JPM`, `AST` | Rejected. Explicitly stated for: `JPM` to discovery mode with a temporary clock set; `JPM` to WX alert mode with a temporary clock set; `AST,ACTIVITY_LOG` with a temporary clock set |
| `<CMD>,ERR,[ERROR CODE]\r` | `URC` only | Four-digit code, see §4.23 |
| `<CMD>,ERR\r` | — | Not in either spec, but **HW**: `KAL` returns `ERR` on SDS200E 1.23.15 (§14) |

There is no documented generic syntax-error, out-of-range or wrong-mode response. In practice a malformed or inapplicable command is most often silently ignored (no datagram at all), so a client **must** treat "no response within timeout" as a normal failure mode and not as a dead link.

### 1.7 Program Mode Only

The command-summary table in both specs carries a "Program Mode Only" column inherited from the BCD536HP specification. **In the SDS specification that column is blank for every one of the 34 commands** — no SDS remote command is program-mode only. (Verified by rendering page 3 of V2.00; the column exists but contains no marks.)

---

## 2. Command summary

Numbering follows V2.00's command list. "V" = first spec version in which the command appears.

| # | Cmd | Function | Request | Response | V | Prog-mode only |
|---|---|---|---|---|---|---|
| 1 | `MDL` | Get model info | `MDL` | `MDL,[MODEL_NAME]` | 0.01 | no |
| 2 | `VER` | Get firmware version | `VER` | `VER,[VERSION]` | 0.01 | no |
| 3 | `KEY` | Push key | `KEY,[KEY_CODE],[KEY_MODE]` | `KEY,OK` | 0.01 | no |
| 4 | `QSH` | Go to quick-search hold | `QSH,[FRQ]` | `QSH,OK` | 0.01 | no |
| 5 | `STS` | Get current status (legacy) | `STS` | `STS,[DSP_FORM],[L1_CHAR],[L1_MODE],…,[RSV]×9` | 0.01 | no |
| 6 | `JNT` | Jump to number tag | `JNT,[FL_TAG],[SYS_TAG],[CHAN_TAG]` | `JNT,OK` | 0.01 | no |
| 7 | `NXT` | Next | `NXT,[tkw],[xxx1],[xxx2],[COUNT]` | `NXT,OK` | 0.01 | no |
| 8 | `PRV` | Previous | `PRV,[tkw],[xxx1],[xxx2],[COUNT]` | `PRV,OK` | 0.01 | no |
| 9 | `FQK` | Get/set FL quick-key status | `FQK` / `FQK,[S0]…[S99]` | `FQK,[S0]…[S99]` / `FQK,OK` | 0.01 | no |
| 10 | `SQK` | Get/set system quick-key status | `SQK,[FAV_QK]` / `SQK,[FAV_QK],[S0]…[S99]` | `SQK,[FAV_QK],[SYS_QK],[S0]…[S99]` / `SQK,OK` | 0.01 | no |
| 11 | `DQK` | Get/set dept quick-key status | `DQK,[FAV_QK],[SYS_QK]` / `DQK,[FAV_QK],[SYS_QK],[S0]…[S99]` | `DQK,[FAV_QK],[SYS_QK],[S0]…[S99]` / `DQK,OK` | 0.01 | no |
| 12 | `PSI` | Push scanner information | `PSI[,…]` (see §4.12) | `PSI,<XML>,` + `<ScannerInfo>` periodically | 0.01 | no |
| 13 | `GSI` | Get scanner information | `GSI` | `GSI,<XML>,` + `<ScannerInfo>` | 0.01 | no |
| 14 | `GLT` | Get list | `GLT,[kind][,index]` | `GLT,<XML>,` + `<GLT>` | 0.01 | no |
| 15 | `HLD` | Hold | `HLD,[tkw],[xxx1],[xxx2]` | `HLD,OK` | 0.01 | no |
| 16 | `AVD` | Set avoid option | `AVD,[tkw],[xxx1],[xxx2],[STATUS]` | `AVD,OK` | 0.01 | no |
| 17 | `SVC` | Get/set service-type settings | `SVC` / `SVC,[PST1]…[PST37],[CST1]…[CST10]` | `SVC,[PST1]…[CST10]` / `SVC,OK` | 0.01 | no |
| 18 | `JPM` | Jump mode | `JPM,[JUMP_MODE],[INDEX]` | `JPM,OK` / `JPM,NG` | 0.01 | no |
| 19 | `DTM` | Get/set date and time | `DTM` / `DTM,[DST],[YYYY],[MM],[DD],[hh],[mm],[ss]` | `DTM,[DST],[YYYY],…,[ss],[RTC]` / `DTM,OK` | 0.01 | no |
| 20 | `LCR` | Get/set location and range | `LCR` / `LCR,[LAT],[LON],[RANGE]` | `LCR,[LAT],[LON],[RANGE]` / `LCR,OK` | 0.01 | no |
| 21 | `AST` | Analyse start | `AST,[MODE][,…]` | mode-dependent, see §9 | 0.01 | no |
| 22 | `APR` | Analyse pause/resume | `APR,[MODE]` | `APR,OK` | 0.01 | no |
| 23 | `URC` | User record control | `URC` / `URC,[STATUS]` | `URC,[STATUS]` / `URC,OK` / `URC,ERR,[CODE]` | 0.01 | no |
| 24 | `MNU` | Menu mode | `MNU,[MENU_ID],[INDEX]` | `MNU,OK` | 0.01 | no |
| 25 | `MSI` | Menu status info | `MSI` | `MSI,<XML>,` + `<MSI>` | 0.01 | no |
| 26 | `MSV` | Menu set value | `MSV,[RSV],[VALUE]` | `MSV,OK` | 0.01 | no |
| 27 | `MSB` | Menu structure back | `MSB,[RSV],[RET_LEVEL]` | `MSB,OK` | 0.01 | no |
| 28 | `GST` | Get scanner status (waterfall-aware) | `GST` | `GST,[DSP_FORM],…,[FFT_SIZE]` | 1.02 | no |
| 29 | `PWF` | Push waterfall FFT info | `PWF,[FFT_TYPE],[ON/OFF]` | `PWF,[DATA1],…,[DATA_n],` periodically | 1.01 | no |
| 30 | `GWF` | Get waterfall FFT info | `GWF,[TYPE],[ON/OFF]` | `GWF,[DATA1],…,[DATA240],` | 1.01 | no |
| — | `GW2` | Get waterfall FFT, no separator | `GW2,[TYPE],[ON/OFF]` | `GW2,[FFT_DATA]` (binary) | 2.00 | no |
| — | `KAL` | Keep alive | `KAL` | *none* (spec) / `ERR` (**HW**) | 2.00 | no |
| 31 | `POF` | Power off | `POF` | `POF,OK` | 1.03 | no |
| 32 | `GCS` | Get charge status | `GCS` | `GCS,CST=…,VOLT=…,CURR=…,TEMP=…\n` | 1.03 | no |
| 33 | `VOL` | Get/set volume level | `VOL` / `VOL,[LEVEL]` | `VOL,[LEVEL]` / `VOL` | 2.00¹ | no |
| 34 | `SQL` | Get/set squelch level | `SQL` / `SQL,[LEVEL]` | `SQL,[LEVEL]` / `SQL` | 2.00¹ | no |

¹ `VOL` and `SQL` are absent from V0.01 and first appear in the V2.00 document; the revision history does not say in which intermediate version they were added.

---

## 3. Common parameter encodings

| Encoding | Rule | Examples |
|---|---|---|
| Frequency (command parameters) | Integer, units of **100 Hz**, no decimal point, no leading zeros required | `4060000` = 406.0000 MHz; `250000` = 25 MHz; `13000000` = 1300 MHz |
| Frequency (XML attributes) | `xxxx.xxxxMHz`, four decimal places, may carry leading spaces | `" 154.4150MHz"` |
| Frequency (`AST` XML) | Bare decimal MHz, no unit suffix | `851.0125` |
| Index | Decimal integer, 0‑based, allocated when the database is loaded to RAM. **Invalidated whenever `DB_Counter` changes** | `283` |
| `0xFFFFFFFF` | Decimal `4294967295`. Means "none"/"top" depending on context | `MonitorList Index="4294967295"` for Full Database |
| Quick key | `0`–`99`, or the literal `None` in XML |  |
| Number tag | FL/system `0`–`99`; channel `0`–`999`; or `None` in XML |  |
| Boolean in XML | `On` / `Off` |  |
| Avoid in XML | `Off` / `T-Avoid` / `Avoid` |  |
| Modulation | `Auto`, `AM`, `NFM`, `FM`, `WFM`, `FMB` (sites: `Auto`/`NFM`/`FM`) |  |
| Name | ASCII 0x20–0x7E, max 64 characters |  |

### 3.1 MyId (database provenance)

Systems, departments, sites and channels that came from the full database — or were copied from it — carry a `MyId`-family attribute relating them to the RadioReference database. User-created objects have none. Search-with-Scan objects never have one.

| HPDB attribute | Describes | RRDB id |
|---|---|---|
| `CountyId` | Conventional system (county) | `ctid` |
| `AgencyId` | Conventional system (agency) | `aid` |
| `TrunkId` | Trunked system | `sid` |
| `CGroupId` | Conventional department | `scid` |
| `CFreqId` | Conventional frequency | `fid` |
| `SiteId` | Trunked site | `siteId` |
| `TGroupId` | Trunked department | `tgCid` |
| `Tid` | Trunked channel | `tgId` |

Emitted as `xxId="value"`, e.g. `CountyId="5"`, `AgencyId="15"`.

---

## 4. Command reference (detail)

### 4.1 `MDL` — Get model info

```
→ MDL\r
← MDL,[MODEL_NAME]\r
```

| Param | Values |
|---|---|
| `MODEL_NAME` | `SDS100` · `SDS200` · `SDS150GBT` |

V0.01 lists only `SDS100` and `SDS200`; V2.00 adds `SDS150GBT`. No separate identifier exists for the European SDS100E/SDS200E — an SDS200E reports `SDS200`.

### 4.2 `VER` — Get firmware version

```
→ VER\r
← VER,[VERSION]\r
```

`VERSION` has the literal form `Version x.xx.xx`, e.g. `Version 1.23.15`. Note the response therefore contains a space and no comma.

### 4.3 `KEY` — Push key

```
→ KEY,[KEY_CODE],[KEY_MODE]\r
← KEY,OK\r
```

`KEY_CODE` — single character, see §13.
`KEY_MODE` — **not enumerated in either spec**. The only in-spec occurrence is the popup-button example, which instructs the app to send `KEY,E,P`. The conventional Uniden set is:

| `KEY_MODE` | Intent |
|---|---|
| `P` | Press (short press) |
| `L` | Long press (≈1 s) |
| `H` | Hold (key down, no release) |
| `R` | Release (key up) |

**HW:** on SDS200E 1.23.15 all four of `P`, `L`, `H`, `R` behave identically — as a single short press. Long-press functions cannot be driven remotely (§14).

The scanner answers `KEY,OK` whether or not the key exists on that model, so `KEY,OK` is **not** evidence that the key did anything (§14).

### 4.4 `QSH` — Go to quick-search hold mode

```
→ QSH,[FRQ]\r
← QSH,OK\r
```

`FRQ` — frequency in units of 100 Hz (e.g. `4060000` for 406.0000 MHz).

Invalid (and ignored) when the scanner is in Menu Mode, during Direct Entry, or during a Quick Save operation.

### 4.5 `STS` — Get current status

Legacy, BCD-compatible screen dump.

```
→ STS\r
← STS,[DSP_FORM],[L1_CHAR],[L1_MODE],[L2_CHAR],[L2_MODE],…,[L20_CHAR],[L20_MODE],
      [RSV],[RSV],[RSV],[RSV],[RSV],[RSV],[RSV],[RSV],[RSV]\r
```

| Field | Meaning |
|---|---|
| `DSP_FORM` | 5–20 digits, each `0` (small font) or `1` (large font). **Its length is the number of display lines present**, and therefore the number of `Lx_CHAR`/`Lx_MODE` pairs that follow |
| `Lx_CHAR` | Line *x* characters, 24 or 30 chars, fixed length (space-padded) |
| `Lx_MODE` | Line *x* display mode, same length as `Lx_CHAR`, character-for-character: `' '` = normal, `'*'` = reverse, `'_'` = underline |
| `RSV` × 9 | Reserved. The worked example shows `0,1,0,0,,,0,OFF,3` — i.e. a mix of numerics, empties and the token `OFF`. Treat as opaque |

V0.01 adds three notes that V2.00 drops:

- "STS Command is compatible with old scanner."
- "**PSI is better than STS.**"
- "See *Font Data Specification* for not ascii character code." (That companion document is not supplied; non-ASCII glyph codes in `Lx_CHAR` are therefore undocumented.)

V2.00 replaces those notes with the full field list and a worked menu-screen example.

Use `GST` (§4.28) in preference to `STS` on any firmware that supports it: `GST` returns the same screen plus mute, LED, and waterfall state.

### 4.6 `JNT` — Jump to number tag

```
→ JNT,[FL_TAG],[SYS_TAG],[CHAN_TAG]\r
← JNT,OK\r
```

| Param | Range |
|---|---|
| `FL_TAG` | 0–99 — Favorites List number tag |
| `SYS_TAG` | 0–99 — System number tag |
| `CHAN_TAG` | 0–999 — Channel number tag |

Fields may be left empty to mean "unchanged/any" (the spec does not say so explicitly, but the three-field form with empties is how the BCD ancestor behaves).

### 4.7 `NXT` — Next

```
→ NXT,[tkw],[xxx1],[xxx2],[COUNT]\r
← NXT,OK\r
```

### 4.8 `PRV` — Previous

```
→ PRV,[tkw],[xxx1],[xxx2],[COUNT]\r
← PRV,OK\r
```

| Param | Meaning |
|---|---|
| `tkw` | Target keyword, §5 |
| `xxx1` | 1st option, §5 |
| `xxx2` | 2nd option, §5 |
| `COUNT` | Slide count, **1–8**. How many items to step in one command |

Both specs label the response line "(2)" — a numbering typo; there is only one response form.

**HW:** `xxx1` must be the *current, live* index. Passing a stale index makes the scanner step relative to that stale position and land somewhere unexpected. Re-read the index with `GSI` immediately before issuing `NXT`/`PRV` (§14).

**Note 2 of the target-keyword sheet:** if `HLD`, `NXT` or `PRV` is sent while in Repeater Find mode, the scanner cancels Repeater Find and returns to the previous mode (Custom Search / Quick Search / Close Call).

### 4.9 `FQK` — Get/set Favorites List quick-key status

```
→ FQK\r                       (get)
← FQK,[S0],[S1],…,[S99]\r

→ FQK,[S0],[S1],…,[S99]\r     (set)
← FQK,OK\r
```

| `Sn` | Meaning |
|---|---|
| `0` | FLQK does not exist |
| `1` | FLQK exists and is **disabled** |
| `2` | FLQK exists and is **enabled** |

100 fields, `S0`…`S99`. On set, a `0` sent by the controller is ignored by the radio (you cannot create or destroy a quick key this way, only enable/disable one that exists).

### 4.10 `SQK` — Get/set System quick-key status

```
→ SQK,[FAV_QK]\r                            (get)
← SQK,[FAV_QK],[SYS_QK],[S0],…,[S99]\r

→ SQK,[FAV_QK],[S0],…,[S99]\r               (set)
← SQK,OK\r
```

`FAV_QK` selects which Favorites List's systems are addressed. Same `0/1/2` semantics as `FQK`.

Note the asymmetry: the get-response carries an extra `SYS_QK` field that the set-request does not. The spec does not define `SYS_QK`'s meaning; by position it is the currently selected system quick key.

### 4.11 `DQK` — Get/set Department quick-key status

```
→ DQK,[FAV_QK],[SYS_QK]\r                        (get)
← DQK,[FAV_QK],[SYS_QK],[S0],…,[S99]\r

→ DQK,[FAV_QK],[SYS_QK],[S0],…,[S99]\r           (set)
← DQK,OK\r
```

Same `0/1/2` semantics. Get-response and set-request are symmetric here.

### 4.12 `PSI` — Push scanner information

Both specs say only: *"format will be XML. See PSI,GSI tab"*, and on the summary page: *"If the scanner receive PSI command, it outputs information periodically. **User can change interval by parameter.**"*

**The request syntax and the interval parameter are not given anywhere in either document.** This is a genuine specification gap (§15). What is certain:

- The payload is the same `<ScannerInfo>` document as `GSI` (§7), preceded by a `PSI,<XML>,` marker datagram.
- Output is unsolicited and periodic until turned off.
- At least one parameter controls the interval.

Until the syntax is confirmed on hardware, implement periodic state by polling `GSI` (or `GST`, which is much cheaper) on a timer. Polling `GSI` at 2–5 Hz is the approach used by every working third-party client.

### 4.13 `GSI` — Get scanner information

```
→ GSI\r
← GSI,<XML>,\r
  <?xml version="1.0" encoding="utf-8"?>\r
  <ScannerInfo Mode="…" V_Screen="…"> … </ScannerInfo>\r
```

Full document description in §7.

**HW:** `GSI` lags the radio's actual state after a `HLD`: roughly 0.2–0.4 s for a channel change, and up to 2.4 s for a system or department change (§14). A client that issues `HLD` then immediately reads `GSI` will see the old object.

### 4.14 `GLT` — Get list

See §6.

### 4.15 `HLD` — Hold

```
→ HLD,[tkw],[xxx1],[xxx2]\r
← HLD,OK\r
```

Holds a system, department or channel. **Cannot** hold a Favorites List or a site frequency. Parameters per §5.

Repeater Find caveat: see Note 2 under §4.8.

"Unknown" department in ID Search is a *virtual* department. You may hold, next and previous it, but not avoid it. It **requires** the parent system index in `xxx2`; for any other department `xxx2` may be blank or the system index, both are accepted (target-keyword sheet Note 3).

**HW:** `HLD` **toggles**. Sending `HLD` for the object that is already held releases the hold rather than re-asserting it. Read `Hold="On"/"Off"` from `GSI` first if you need a definite state (§14).

### 4.16 `AVD` — Set avoid option

```
→ AVD,[tkw],[xxx1],[xxx2],[STATUS]\r
← AVD,OK\r
```

| `STATUS` | Meaning |
|---|---|
| `1` | Permanent Avoid |
| `2` | Temporary Avoid |
| `3` | Stop Avoiding |

Cannot avoid a Favorites List or a site frequency. There is no "get" form — read avoid state from `GSI` or `GLT`.

Spec typo: V0.01 prints the request as `AVD,[tkw],[xxx1],[xxx2][STATUS1:Permanent Avoid`; V2.00 as `AVD,[tkw],[xxx1],[xxx2][STATUS]`. Both are missing the separating comma. The worked example settles it:

```
AVD,AFREQ,4060000,,1\r        ← correct: avoid 406.0 MHz in Quick Search
AVD,QS_FREQ,4060000,,1\r      ← wrong, will not work
```

So: five comma-separated fields, with `xxx2` empty when the target keyword takes no 2nd option.

**Search-mode avoid rule:** to avoid a frequency found in Quick Search, Custom Search, Search-with-Scan, Close Call or Repeater Find, always use the `AFREQ` keyword — not `QS_FREQ`/`CS_FREQ`/`SWS_FREQ`/`CC`. Repeater Find frequencies cannot be avoided at all.

### 4.17 `SVC` — Get/set service-type settings

```
→ SVC\r                                            (get)
← SVC,[PST1],[PST2],…,[PST37],[CST1],…,[CST10]\r

→ SVC,[PST1],…,[PST37],[CST1],…,[CST10]\r          (set)
← SVC,OK\r
```

Exactly **47 fields** in fixed order: PST1…PST37 then CST1…CST10. Each field:

| Value | Meaning |
|---|---|
| `0` | Off (not scanned) |
| `1` | On (scanned) |

Positions marked "non" in the name table (§12) are unused slots but **still occupy their position** — always send all 47 fields.

### 4.18 `JPM` — Jump mode

```
→ JPM,[JUMP_MODE],[INDEX]\r
← JPM,OK\r      or      JPM,NG\r
```

| `JUMP_MODE` | `INDEX` | Notes |
|---|---|---|
| `SCN_MODE` | Channel Index | `4294967295` (0xFFFFFFFF) = start scanning from the top channel |
| `CTM_MODE` | Reserved (send empty) | Custom search |
| `QSH_MODE` | Reserved | Quick search hold |
| `CC_MODE` | Reserved | Close Call |
| `WX_MODE` | `NORMAL`, `A_ONLY`, `SAME_1`, `SAME_2`, `SAME_3`, `SAME_4`, `SAME_5`, `ALL_FIPS` | `NG` if a temporary clock is set |
| `FTO_MODE` | Reserved | Fire Tone-Out |
| `WF_MODE` | Reserved | **Waterfall. Added in V1.01/V2.00; absent from V0.01** |
| `IREC_MODE` | Reserved | Internal (instant) record replay |
| `UREC_MODE` | Folder Name | User record replay |
| `TDIS_MODE` | Session Name | Trunk discovery. `NG` if a temporary clock is set |
| `CDIS_MODE` | Session Name | Conventional discovery. `NG` if a temporary clock is set |

### 4.19 `DTM` — Get/set date and time

```
→ DTM\r                                                      (get)
← DTM,[DayLightSaving],[YYYY],[MM],[DD],[hh],[mm],[ss],[RTC Status]\r

→ DTM,[DayLightSaving],[YYYY],[MM],[DD],[hh],[mm],[ss]\r     (set)
← DTM,OK\r
```

| Field | Values |
|---|---|
| `DayLightSaving` | Boolean flag (spec does not state the encoding; `0`/`1`) |
| `RTC Status` | `0` = RTC NG (clock invalid/lost), `1` = RTC OK. **Get-response only** |

A "temporary clock" (an RTC that has been set only for this power cycle) blocks `JPM` to discovery and WX alert modes, and blocks `AST,ACTIVITY_LOG`.

### 4.20 `LCR` — Get/set location and range

```
→ LCR\r                                  (get)
← LCR,[LATITUDE],[LONGITUDE],[RANGE]\r

→ LCR,[LATITUDE],[LONGITUDE],[RANGE]\r   (set)
← LCR,OK\r
```

Latitude and longitude are in **decimal degrees** ("degree format"), signed. The spec does not state the unit of `RANGE`; on the radio's own menu it is miles or km according to the unit setting.

### 4.21 `AST` — Analyse start

See §9.

### 4.22 `APR` — Analyse pause/resume

```
→ APR,[Analize Mode]\r
← APR,OK\r
```

| `Analize Mode` |
|---|
| `SYSTEM_STATUS` |
| `RF_POWER_PLOT` |
| `CURRENT_ACTIVITY` |
| `LCN_MONITOR` |
| `ACTIVITY_LOG` |
| `RAW_DATA_OUTPUT` |

`APR` is a **toggle** — the same command pauses a running analyse mode and resumes a paused one. Note `BAND_SCOPE` and `LCN_FINDER` are *not* in the `APR` list even though they are `AST` modes.

`RAW_DATA_OUTPUT` saturates the link; the spec instructs you to send the pause command before attempting any other remote command.

### 4.23 `URC` — User record control

```
→ URC\r                 (get)
← URC,[STATUS]\r

→ URC,[STATUS]\r        (set)
← URC,OK\r
  or URC,ERR,[ERROR CODE]\r
```

| `STATUS` | Meaning |
|---|---|
| `0` | Stop |
| `1` | Start |

| `ERROR CODE` | Meaning |
|---|---|
| `0001` | File access error |
| `0002` | Low battery |
| `0003` | Session over limit |
| `0004` | RTC lost |

### 4.24 `MNU` — Menu mode

See §8.

### 4.25 `MSI` — Menu status info

See §8.

### 4.26 `MSV` — Menu set value

See §8.

### 4.27 `MSB` — Menu structure back

See §8.

### 4.28 `GST` — Get scanner status (waterfall-aware)

Added in V1.02. Supersedes `STS`.

```
→ GST\r
← GST,[DSP_FORM],[L1_CHAR],[L1_MODE],[L2_CHAR],[L2_MODE],…,[L20_CHAR],[L20_MODE],
      [MUTE],[LED1],[LED2],
      [WF_MODE],[FREQ],[MOD],[MF_POS],
      [CF],[LOWER],[UPPER],[COLOR_MODE],[FFT_SIZE]\r
```

Screen fields (`DSP_FORM`, `Lx_CHAR`, `Lx_MODE`) are exactly as `STS` (§4.5), including the rule that `DSP_FORM`'s digit count determines how many line pairs follow.

Tail fields, in order:

| # | Field | Meaning / values |
|---|---|---|
| 1 | `MUTE` | Mute state. **Not described in the spec's own field list** — only named in the response template. Expect `0`/`1` or `Mute`/`Unmute`; cross-check against `Property Mute=` from `GSI` |
| 2 | `LED1` | Alert LED colour: `0`=OFF, `1`=BLUE, `2`=RED, `3`=MAGENTA, `4`=GREEN, `5`=CYAN, `6`=YELLOW, `7`=WHITE |
| 3 | `LED2` | Battery-charge LED, same colour codes. **SDS150 only** (Note 3). Present but meaningless on SDS100/SDS200 |
| 4 | `WF_MODE` | Scanner mode: `0` = Normal Mode, `1` = Waterfall, `2` = Menu / Direct Entry. "Normal Mode" means SDS Link is not in an effective mode (i.e. ordinary scan, search, …) |
| 5 | `FREQ` | Mark frequency (the waterfall marker's frequency) |
| 6 | `MOD` | Modulation at the marker |
| 7 | `MF_POS` | Marker position — the marker's x-position within the 240-bin FFT span (0–239) |
| 8 | `CF` | Centre frequency of the waterfall span |
| 9 | `LOWER` | Lower frequency of the span |
| 10 | `UPPER` | Upper frequency of the span |
| 11 | `COLOR_MODE` | `0` = Colour, `1` = Black back, `2` = White back |
| 12 | `FFT_SIZE` | FFT area size on screen: `0` = 25 %, `1` = 50 %, `2` = 75 %, `3` = 100 % |

Escaping (Note 2, `GST` only):

- If `Lx_CHAR` or `Lx_MODE` is all spaces (0x20) it is emitted as `""` (empty field).
- If `Lx_CHAR` contains a comma, the comma is replaced by TAB (`\t`).

`GST` is the right command for a polling display loop: it is a single datagram, needs no XML parse, and carries everything the waterfall UI needs except the FFT bins themselves.

### 4.29 `PWF` — Push waterfall FFT information

```
→ PWF,[FFT_TYPE],[ON/OFF]\r
← PWF,[DATA1],[DATA2],…,[DATA_n],\r        (repeatedly, until turned off)
```

| Param | Values |
|---|---|
| `FFT_TYPE` | `1` = Displayed FFT (240 bins) |
| `ON/OFF` | Enable/disable the push stream. The spec gives no literal; use `1`/`0` |

Note the response has a **trailing comma before the CR**.

### 4.30 `GWF` — Get waterfall FFT information

```
→ GWF,[TYPE],[ON/OFF]\r
← GWF,[DATA1],[DATA2],…,[DATA240],\r
```

240 comma-separated bin values, then a trailing comma, then CR. See §10 for the value encoding.

### 4.31 `GW2` — Get waterfall FFT information without separator

Added in V2.00. Identical data, binary-packed instead of comma-separated — one byte per bin, 240 bytes.

```
→ GW2,[TYPE],[ON/OFF]\r
← GW2,[FFT_DATA]\r
```

**Spec defect:** the V2.00 page for `GW2` shows the request and response both spelled `GWF`. The command token is `GW2` (as listed in the command list and the heading); the `GWF` in the body is a copy-paste error from the preceding page.

Because `[FFT_DATA]` is raw binary, a byte of value 0x0D inside it is indistinguishable from the terminator. A client must therefore read exactly 240 bytes after the `GW2,` prefix and treat the following byte as the terminator — not scan for CR.

### 4.32 `KAL` — Keep alive

```
→ KAL\r
← (no response)
```

Added in V2.00. The spec states explicitly: *"Radio does not return a response for Keep Alive command."*

**HW:** on SDS200E firmware 1.23.15 `KAL` returns `ERR` (§14). Treat `KAL` as unsupported on that firmware and keep the session alive with a cheap `GST` or `VER` poll instead.

### 4.33 `POF` — Power off

```
→ POF\r
← POF,OK\r
```

Added in V1.03. Powers the radio down. The UDP session dies immediately afterwards; expect no further traffic.

### 4.34 `GCS` — Get charge status

Added in V1.03. Primarily meaningful on the battery-powered SDS100/SDS150.

```
→ GCS\r
← GCS,CST=[DATA1],VOLT=[DATA2]mV:[DATA3]%,CURR=[DATA4]mA,TEMP=[DATA5]C\n
```

**This is the only response in the specification terminated by `\n` rather than `\r`.** It is also the only one with a `key=value` payload rather than positional fields.

| `DATA1` (`CST`) | Symbol | Meaning |
|---|---|---|
| `0` | `CHG_SUBSTS_NO_CHG` | No external power or no battery |
| `1` | `CHG_SUBSTS_BQ274_INIT` | Initialising gauge IC (BQ27426) |
| `2` | `CHG_SUBSTS_TEMP_NG` | Abnormal temperature |
| `3` | `CHG_SUBSTS_PWR_NG` | Abnormal power |
| `4` | `CHG_SUBSTS_FULL_CHG` | Full charge |
| `5` | `CHG_SUBSTS_RECHG` | Recharge |
| `6` | `CHG_SUBSTS_CHARGE` | Charging |

| Field | Meaning |
|---|---|
| `DATA2` | Battery voltage, mV |
| `DATA3` | Remaining capacity, % |
| `DATA4` | Current, mA; positive = charging, negative = discharging. Zero-padded to 4 digits |
| `DATA5` | Battery temperature, °C, with decimals, possibly space-padded |

Example: `CST=4,VOLT=4184mV:100%,CURR=0000mA,TEMP= 27.65C`

### 4.35 `VOL` — Get/set volume level

```
→ VOL\r                 (get)
← VOL,[LEVEL]\r

→ VOL,[LEVEL]\r         (set)
← VOL\r
```

| Model | `LEVEL` range |
|---|---|
| SDS100 / SDS150 | 0–15 |
| SDS200 | **0–29** |

**Spec defect:** the set-acknowledgement is printed as bare `VOL[\r]` with no `,OK`. Every other setter in the specification answers `<CMD>,OK`. Treat both `VOL` and `VOL,OK` as success.

### 4.36 `SQL` — Get/set squelch level

```
→ SQL\r                 (get)
← SQL,[LEVEL]\r

→ SQL,[LEVEL]\r         (set)
← SQL\r
```

| Model | `LEVEL` range |
|---|---|
| SDS100 / SDS150 | 0–15 |
| SDS200 | **0–19** |

The V2.00 parameter table mislabels this field "Volume Level"; it is the squelch level. Same `,OK` ambiguity as `VOL`.

---

## 5. Target keywords — `tkw`, 1st and 2nd options

The "tkd and 1st,2nd opt" sheet. `[none]` means the parameter exists but must be empty; `---` means the command is invalid for that keyword. **`GLT` never takes a 2nd option** (that column is hatched out for every row).

| Target | `tkw` | GLT 1st | NXT/PRV 1st | NXT/PRV 2nd | HLD 1st | HLD 2nd | AVD 1st | AVD 2nd |
|---|---|---|---|---|---|---|---|---|
| Favorites List | `FL` | `[none]` | --- | --- | --- | --- | --- | --- |
| System | `SYS` | Parent FL Index | Sys Index | `[none]` | Sys Index | `[none]` | Sys Index | `[none]` |
| Department | `DEPT` | Parent Sys Index | Dept Index | Parent Sys Index | Dept Index | Parent Sys Index | Dept Index | `[none]` |
| Site | `SITE` | Parent Sys Index | Site Index | `[none]` | Site Index | `[none]` | Site Index | `[none]` |
| Conventional frequency | `CFREQ` | Parent Dept Index | Chan Index | `[none]` | Chan Index | `[none]` | Chan Index | `[none]` |
| TGID in ID Scan | `TGID` | Parent Dept Index | Chan Index | `[none]` | Chan Index | `[none]` | Chan Index | `[none]` |
| TGID in ID Search | `STGID` | --- | TGID | Site Index | TGID | Site Index | --- (use `ATGID`) | --- |
| Site frequency | `SFREQ` | Parent Site Index | --- | --- | --- | --- | --- | --- |
| Avoiding TGID in ID Search | `ATGID` | Parent Sys Index | --- | --- | --- | --- | TGID | Parent Sys Index |
| Search avoiding frequency | `AFREQ` | `[none]` | --- | --- | --- | --- | Frequency | `[none]` |
| Close Call | `CC` | --- | `[none]` | `[none]` | `[none]` | `[none]` | --- (use `AFREQ`) | --- |
| WX | `WX` | `[none]` | WX Chan Index | `[none]` | WX Chan Index | `[none]` | --- | --- |
| Tone-Out mode | `FTO` | `[none]` | FTO Chan Index | `[none]` | FTO Chan Index | `[none]` | --- | --- |
| Search with scan frequency | `SWS_FREQ` | --- | Frequency | Parent Dept Index | Frequency | Parent Dept Index | --- (use `AFREQ`) | --- |
| CC Hits channel | `CCHIT` | Parent Dept Index | CC Chan Index | `[none]` | CC Chan Index | `[none]` | CC Chan Index | `[none]` |
| Custom Search bank | `CS_BANK` | `[none]` | --- | --- | --- | --- | --- | --- |
| Custom Search frequency | `CS_FREQ` | --- | Frequency | Parent Bank Index | Frequency | Parent Bank Index | --- (use `AFREQ`) | --- |
| Quick Search frequency | `QS_FREQ` | --- | Frequency | `[none]` | Frequency | `[none]` | --- (use `AFREQ`) | --- |
| Repeater Find frequency | `RPTR_FREQ` | --- | Frequency | `[none]` | Frequency | `[none]` | --- (cannot avoid) | --- |
| Instant-record file | `IREC_FILE` | `[none]` | *(blank in source — use File Index)* | `[none]` | File Index | `[none]` | --- (cannot avoid) | --- |
| User-record folder | `UREC_FOLDER` | `[none]` | --- (cannot select folder) | --- | --- (cannot select folder) | --- | --- (cannot avoid) | --- |
| User-record file | `UREC_FILE` | Folder Index | File Index | `[none]` | File Index | `[none]` | --- (cannot avoid) | --- |
| Trunk discovery | `TRN_DISCOV` | `[none]` | --- | --- | --- | --- | TGID | `[none]` |
| Conventional discovery | `CNV_DISCOV` | `[none]` | --- | --- | --- | --- | Frequency | `[none]` |
| ~~Band scope~~ | ~~`BAND_SCOPE`~~ | ~~---~~ | ~~Frequency~~ | ~~`[none]`~~ | ~~Frequency~~ | ~~`[none]`~~ | ~~---~~ | ~~---~~ |

**`BAND_SCOPE` is struck through in V2.00** (highlighted and crossed out on the sheet), consistent with the Band Scope analyse mode being marked "Removed in SDS100". It existed in V0.01. Do not use it.

Which commands accept which keyword, at a glance:

| `tkw` | GLT | NXT | PRV | HLD | AVD |
|---|---|---|---|---|---|
| `FL` | ✓ | — | — | — | — |
| `SYS` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `DEPT` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `SITE` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `CFREQ` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `TGID` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `STGID` | — | ✓ | ✓ | ✓ | — |
| `SFREQ` | ✓ | — | — | — | — |
| `ATGID` | ✓ | — | — | — | ✓ |
| `AFREQ` | ✓ | — | — | — | ✓ |
| `CC` | — | ✓ | ✓ | ✓ | — |
| `WX` | ✓ | ✓ | ✓ | ✓ | — |
| `FTO` | ✓ | ✓ | ✓ | ✓ | — |
| `SWS_FREQ` | — | ✓ | ✓ | ✓ | — |
| `CCHIT` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `CS_BANK` | ✓ | — | — | — | — |
| `CS_FREQ` | — | ✓ | ✓ | ✓ | — |
| `QS_FREQ` | — | ✓ | ✓ | ✓ | — |
| `RPTR_FREQ` | — | ✓ | ✓ | ✓ | — |
| `IREC_FILE` | ✓ | ✓ | ✓ | ✓ | — |
| `UREC_FOLDER` | ✓ | — | — | — | — |
| `UREC_FILE` | ✓ | ✓ | ✓ | ✓ | — |
| `TRN_DISCOV` | ✓ | — | — | — | ✓ |
| `CNV_DISCOV` | ✓ | — | — | — | ✓ |
| ~~`BAND_SCOPE`~~ | — | ~~✓~~ | ~~✓~~ | ~~✓~~ | — |

Sheet notes, in full:

- **Note 1** — To avoid 406.0 MHz in Quick Search mode: `AVD,AFREQ,4060000,,1\r` is correct; `AVD,QS_FREQ,4060000,,1\r` is a bad command.
- **Note 2** — If the app sends `HLD`, `NXT` or `PRV` in Repeater Find mode, the scanner cancels Repeater Find and returns to the previous mode (Custom Search / Quick Search / Close Call).
- **Note 3** — "Unknown" department in ID Search is a virtual department. You can hold, next and previous it but cannot avoid it. "Unknown" department *requires* a parent system index; other departments do not — blank or the system index are both acceptable as the 2nd parameter.

---

## 6. `GLT` — list retrieval

`GLT` is how a controller pulls the scanner's internal lists. Response is XML, preceded by the `GLT,<XML>,` marker and possibly fragmented (§1.3–1.4).

### 6.1 Request forms

| # | Request | Returns |
|---|---|---|
| 1 | `GLT,FL` | Favorites Lists |
| 2 | `GLT,SYS,[fl_index]` | Systems in that Favorites List |
| 3 | `GLT,DEPT,[system_index]` | Departments in that system |
| 4 | `GLT,SITE,[system_index]` | Sites in that system |
| 5 | `GLT,CFREQ,[dept_index]` | Conventional frequencies in that department |
| 6 | `GLT,TGID,[dept_index]` | TGIDs in that department |
| 7 | `GLT,SFREQ,[site_index]` | Site frequencies |
| 8 | `GLT,AFREQ` | Search-avoiding frequencies |
| 9 | `GLT,ATGID,[system_index]` | Search-avoiding TGIDs |
| 10 | `GLT,FTO` | Fire Tone-Out channels |
| 11 | `GLT,CS_BANK` | Custom Search banks |
| 12 | `GLT,UREC` | User-record folders |
| 13 | `GLT,IREC_FILE` | Instant-record files |
| 14 | `GLT,UREC_FILE,[folder_index]` | User-record files in that folder |
| 15 | `GLT,TRN_DISCOV` | Trunk discovery sessions |
| 16 | `GLT,CNV_DISCOV` | Conventional discovery sessions |

### 6.2 Record attribute order

The XML element name equals the keyword; attributes appear in the order listed. Abbreviations: `Q_Key` = quick key, `N_Tag` = number tag, `Freq` = frequency, `Mod` = modulation, `SAS` = sub-audio setting (CTCSS / DCS / P25 NAC / colour code / RAN / area).

| # | Element | Attribute order |
|---|---|---|
| 1 | `FL` | `Index`, `Name`, `Monitor`, `Q_Key`, `N_Tag` |
| 2 | `SYS` | `Index`, `MyId`, `Name`, `Avoid`, `Type`, `Q_Key`, `N_Tag` |
| 3 | `DEPT` | `Index`, `MyId`, `Name`, `Avoid`, `Q_Key` |
| 4 | `SITE` | `Index`, `MyId`, `Name`, `Avoid`, `Q_Key` |
| 5 | `CFREQ` | `Index`, `MyId`, `Name`, `Avoid`, `Freq`, `Mod`, `SAS`, `SAL`, `SvcType`, `N_Tag` |
| 6 | `TGID` | `Index`, `MyId`, `Name`, `Avoid`, `TGID`, `Audio Type`, `SvcType`, `N_Tag` |
| 7 | `SFREQ` | `Index`, `Freq` |
| 8 | `AFREQ` | `Freq`, `Avoid` |
| 9 | `ATGID` | `TGID`, `Avoid`, `Index`, `Name`, `DeptName`, `DeptIndex` |
| 10 | `FTO` | `Index`, `Freq`, `Mod`, `Name`, `ToneA`, `ToneB` |
| 11 | `CS_BANK` | `Index`, `Name`, `Lower`, `Upper`, `Mod`, `Step` |
| 12 | `UREC` | `Index`, `Name` — `Name` = folder name |
| 13 | `IREC_FILE` | `Index`, `Name`, `Time` — `Name` = file name |
| 14 | `UREC_FILE` | `Index`, `Name`, `Time` — `Name` = file name |
| 15 | `TRN_DISCOV` | `Name`, `Delay`, `Logging`, `Duration`, `CompareDB`, `SystemName`, `SystemType`, `SiteName`, `TimeOutTimer`, `AutoStore` — `Name` = session name |
| 16 | `CNV_DISCOV` | `Name`, `Lower`, `Upper`, `Mod`, `Step`, `Delay`, `Logging`, `CompareDB`, `Duration`, `TimeOutTimer`, `AutoStore` — `Name` = session name |

`Avoid` values in `GLT` records: `Off`, `Avoid`, `T-Avoid` (the sheet's legend lists these three).

Two notes on the sheet:

- *"The Index is kind of handle. PC uses index to Hold and Avoid."*
- *"MyId is like RRDB ID."*

### 6.3 Worked example

```
→ GLT,FL\r
← GLT,<XML>,\r
  <?xml version="1.0" encoding="utf-8"?>\r
  <GLT>\r
    <FL Index="0" Name="Favorites List 1" Monitor="On"  Q_Key="1" N_Tag="None" />\r
    <FL Index="1" Name="Favorites List 2" Monitor="On"  Q_Key="2" N_Tag="2" />\r
    <FL Index="2" Name="Favorites List 3" Monitor="Off" Q_Key="3" N_Tag="999" />\r
  </GLT>\r
```

### 6.4 Index invalidation

Indices are allocated when the database is loaded into RAM. The `MonitorList DB_Counter` attribute in `GSI` increments whenever that happens. **If `DB_Counter` differs from the value in force when a list was fetched, every index from that list is invalid** — re-fetch before using any index in `HLD`, `AVD`, `NXT`, `PRV` or `MNU`.

---

## 7. `PSI` / `GSI` — the `ScannerInfo` document

### 7.1 Structure

Root element is `<ScannerInfo>`. Elements present in **every** mode:

| Element | Note |
|---|---|
| `ScannerInfo` | Root |
| `Property` | |
| `AGC` | |
| `DispFormat` | Named in the "All mode Elements" list but **no attribute table is given anywhere in either spec** — an undocumented element |
| `ViewDescription` | Only when the radio is showing an override area |
| `ReplayDescription` | Only when the radio is in REPLAY mode |

### 7.2 `ScannerInfo` attributes

| Attribute | Values |
|---|---|
| `Mode` | Human-readable mode name, 14 values (below) |
| `V_Screen` | Machine-readable view-screen id, 19 values (below) |

**`Mode` value list** (exactly as printed):

`Scan Mode` · `Scan Hold` · `Tone-Out` · `Custom Search` · `Custom Search Hold` · `Quick Search` · `Quick Search Hold` · `Service Scan` · `Service Scan Hold` · `Trunk Scan` · `Trunk Scan Hold` · `Close Call Only` · `Close Call` · `Menu tree`

**`V_Screen` value list** (exactly as printed in V2.00):

`plain_text` · `conventional_scan` · `trunk_scan` · `custom_with_scan` · `cchits_with_scan` · `custom_search` · `quick_search` · `close_call` · `cc_searching` · `tone_out` · `wx_alert` · `discovery_conventional` · `discovery_trunking` · `reverse_frequency` · `repeater_find` · `direct_entry` · `menu_selection` · `menu_input` · `analyze_system_status` · `analyze`

V0.01 additionally listed `band_scope`; V2.00 removed it.

> **Specification inconsistency.** V2.00's mode/element matrix has a `waterfall` column (and `WaterfallBand`/`WaterfallSettings` elements keyed to it), but `waterfall` was **not** added to the `V_Screen` enumeration on the attribute page. A client must accept `V_Screen="waterfall"` even though the enumeration does not list it. Likewise `rf_power_plot` is a matrix column but no element row references it, even though an `RfPowerPlot` attribute table exists (§7.6).

### 7.3 Mode / element matrix

Rows = elements that appear only in some modes. Columns = `V_Screen`. ✓ = present, – = absent. (Transcribed cell-by-cell from the rendered table on page 18 of V2.00, not from text extraction.)

Column key:
1 `conventional_scan` · 2 `trunk_scan` · 3 `custom_with_scan` · 4 `cchits_with_scan` · 5 `custom_search` · 6 `quick_search` · 7 `close_call` · 8 `cc_searching` · 9 `tone_out` · 10 `wx_alert` · 11 `reverse_frequency` · 12 `repeater_find` · 13 `discovery_conventional` · 14 `discovery_trunking` · 15 `analyze_system_status` · 16 `rf_power_plot` · 17 `analyze` · 18 `waterfall`

| Element | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9 | 10 | 11 | 12 | 13 | 14 | 15 | 16 | 17 | 18 |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| `MonitorList` | ✓ | ✓ | ✓ | ✓ | – | – | – | – | – | – | – | – | – | – | – | – | – | – |
| `System` | ✓ | ✓ | ✓ | ✓ | – | – | – | – | – | – | – | – | – | – | – | – | – | – |
| `Department` | ✓ | ✓ | ✓ | ✓ | – | – | – | – | – | – | – | – | – | – | – | – | – | – |
| `Site` | – | ✓ | – | – | – | – | – | – | – | – | – | – | – | – | – | – | – | – |
| `ConvFrequency` | ✓ | – | – | – | – | – | – | – | – | – | – | – | – | – | – | – | – | – |
| `TGID` | – | ✓ | – | – | – | – | – | – | – | – | – | – | – | – | – | – | – | – |
| `SiteFrequency` | – | ✓ | – | – | – | – | – | – | – | – | – | – | – | – | – | – | – | – |
| `SrchFrequency` | – | – | ✓ | – | ✓ | ✓ | ✓ | – | – | ✓ | ✓ | ✓ | – | – | – | – | – | – |
| `CcHitsChannel` | – | – | – | ✓ | – | – | – | – | – | – | – | – | – | – | – | – | – | – |
| `DualWatch` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | – | – | ✓ | ✓ | – | – | – | – | – | – |
| `SearchRange` | – | – | ✓ | – | ✓ | ✓ | – | – | – | – | – | – | – | – | – | – | – | – |
| `SearchBanks` | – | – | – | – | ✓ | – | – | – | – | – | – | – | – | – | – | – | – | – |
| `CC_Bands` | – | – | – | – | – | – | – | ✓ | – | – | – | – | – | – | – | – | – | – |
| `CC_Counters` | – | – | – | – | – | – | – | ✓ | – | – | – | – | – | – | – | – | – | – |
| `ToneOutChannel` | – | – | – | – | – | – | – | – | ✓ | – | – | – | – | – | – | – | – | – |
| `WxChannel` | – | – | – | – | – | – | – | – | – | ✓ | – | – | – | – | – | – | – | – |
| `WxMode` | – | – | – | – | – | – | – | – | – | ✓ | – | – | – | – | – | – | – | – |
| `ConventionalDiscovery` | – | – | – | – | – | – | – | – | – | – | – | – | ✓ | – | – | – | – | – |
| `TrunkingDiscovery` | – | – | – | – | – | – | – | – | – | – | – | – | – | ✓ | – | – | – | – |
| `SystemStatus` | – | – | – | – | – | – | – | – | – | – | – | – | – | – | ✓ | – | – | – |
| `Analyze` | – | – | – | – | – | – | – | – | – | – | – | – | – | – | – | – | ✓ | – |
| `WaterfallBand` | – | – | – | – | – | – | – | – | – | – | – | – | – | – | – | – | – | ✓ |
| `WaterfallSettings` | – | – | – | – | – | – | – | – | – | – | – | – | – | – | – | – | – | ✓ |

Observations a client must handle:

- No element row is marked for column 16 (`rf_power_plot`), though `RfPowerPlot` has a full attribute table. In V0.01 the matrix *did* carry an `RfPowerPlot` row (✓ in the rf_power_plot column) — V2.00 dropped the row and kept the attribute table.
- `SrchFrequency` is marked present in `wx_alert`, alongside `WxChannel`/`WxMode`.
- V0.01 had `BandScope` and `BandScopeRange` rows ✓ in the `band_scope` column; V2.00 replaced both rows with `WaterfallBand`/`WaterfallSettings` in a new `waterfall` column.

### 7.4 Elements inside `ViewDescription` and `ReplayDescription`

| Container | Child elements |
|---|---|
| `ViewDescription` | `InfoArea1`, `InfoArea2`, `OverWrite`, `PopupScreen`, `PlainText` |
| `ReplayDescription` | `File`, `ReplayMode` |

### 7.5 Attribute tables

#### `Property`

| Attribute | Values |
|---|---|
| `F` | `Off` / `On` — Function (F) key latched state |
| `VOL` | 0–29 (SDS200) or 0–15 (SDS100/SDS150) |
| `SQL` | 0–19 (SDS200) or 0–15 (SDS100/SDS150) |
| `Sig` | 0–4 — signal-strength bars |
| `WiFi` | `Off` / `0`–`3` / `AP` |
| `Battery` | 0.0–3.3 |
| `Att` | `Off` / `On` / `G-Att` |
| `Rec` | `Off` / `On` |
| `KeyLock` | `Off` / `On` |
| `P25Status` | `None` / `Data` / `P25` / `DMR` / `CAP` / `CON` / `DT3` / `XPT` / `NX9` / `NX4` / `ND9` / `ND4` / `IDS` / `NXD` |
| `Mute` | `Unmute` / `Mute` |
| `A_Led` | `Off` / `Blue` / `Red` / `Magenta` / `Green` / `Cyan` / `Yellow` / `White` |
| `Dir` | `Up` / `Down` — search/scan direction |
| `Rssi` | `0-` — floating point, e.g. `0.377` |
| `Backlight` | **Not in the attribute table** but present in the spec's own worked example (`Backlight="100"`). Percentage |

#### `AGC`

| Attribute | Values |
|---|---|
| `A_AGC` | `Off` / `On` — analogue AGC |
| `D_AGC` | `Off` / `On` — digital AGC |

#### `DualWatch`

| Attribute | Values |
|---|---|
| `PRI` | `Off` / `DND` / `Priority` |
| `CC` | `Off` / `DND` / `Priority` |
| `WX` | `Off` / `Priority` |

#### `MonitorList`

| Attribute | Values |
|---|---|
| `Name` | ASCII, max 64 |
| `Index` | 0– (`4294967295` for Full Database) |
| `ListType` | `FullDb` / `FL` / `SWS` |
| `Q_Key` | 0–99 / `None` |
| `N_Tag` | 0–99 / `None` |
| `DB_Counter` | 0–65535, wraps to 0 |

#### `System`

| Attribute | Values |
|---|---|
| `Name` | ASCII, max 64 |
| `Index` | 0– |
| `Avoid` | `Off` / `T-Avoid` / `Avoid` |
| `SystemType` | `Conventional` · `Motorola` · `EDACS` · `LTR` · `P25 Trunk` · `P25 One Frequency` · `MotoTRBO Trunk` · `DMR One Frequency` · `NXDN Trunk` · `NXDN One Frequency` |
| `Q_Key` | 0–99 / `None` |
| `N_Tag` | 0–99 / `None` |
| `Hold` | `Off` / `On` |

#### `Department`

| Attribute | Values |
|---|---|
| `Name` | ASCII, max 64 |
| `Index` | 0– |
| `Avoid` | `Off` / `T-Avoid` / `Avoid` |
| `Q_Key` | 0–99 / `None` |
| `Hold` | `Off` / `On` |

#### `Site`

| Attribute | Values |
|---|---|
| `Name` | ASCII, max 64 |
| `Index` | 0– |
| `Avoid` | `Off` / `T-Avoid` / `Avoid` |
| `Q_Key` | 0–99 / `None` |
| `Hold` | `Off` / `On` |
| `Mod` | `Auto` / `NFM` / `FM` |

#### `ConvFrequency`

| Attribute | Values |
|---|---|
| `Name` | ASCII, max 64 |
| `Index` | 0– |
| `Avoid` | `Off` / `T-Avoid` / `Avoid` |
| `Freq` | `xxxx.xxxxMHz` |
| `Mod` | `Auto` / `AM` / `NFM` / `FM` / `WFM` / `FMB` |
| `N_Tag` | 0–999 / `None` |
| `Hold` | `Off` / `On` |
| `SvcType` | See §12 |
| `P_Ch` | `Off` / `On` — priority channel |
| `SAS` | Sub-audio setting (see note below) |
| `SAL` | `Off` / `On` |
| `SAD` | Sub-audio decode (see note below) |
| `RecSlot` | `Slot 1` / `Slot 2` / `None` |
| `LVL` | `-3` … `0` … `3` |
| `IFX` | `Off` / `On` — intermediate-frequency exchange |
| `TGID` | `TGID xxxx` / `None` |
| `U_Id` | `UID xxxx` / `None` |

#### `TGID`

| Attribute | Values |
|---|---|
| `Name` | ASCII, max 64 |
| `Index` | 0– |
| `Avoid` | `Off` / `T-Avoid` / `Avoid` |
| `TGID` | `TGID:xxxx` |
| `SetSlot` | `Slot 1` / `Slot 2` / `Any` |
| `RecSlot` | `Slot 1` / `Slot 2` / `None` |
| `N_Tag` | 0–999 / `None` |
| `Hold` | `Off` / `On` |
| `SvcType` | See §12 |
| `P_Ch` | `Off` / `On` |
| `LVL` | `-3` … `3` |

#### `SiteFrequency`

| Attribute | Values |
|---|---|
| `Freq` | `xxxx.xxxxMHz` |
| `SAS` | Sub-audio |
| `SAD` | Sub-audio |
| `IFX` | `Off` / `On` |

#### `SrchFrequency`

| Attribute | Values |
|---|---|
| `Avoid` | `Off` / `T-Avoid` / `Avoid` |
| `Freq` | `xxxx.xxxxMHz` |
| `Mod` | `Auto` / `AM` / `NFM` / `FM` / `WFM` / `FMB` |
| `Hold` | `Off` / `On` |
| `SAD` | Sub-audio |
| `RecSlot` | `Slot 1` / `Slot 2` / `None` |
| `TGID` | `TGID xxxx` / `None` |
| `U_Id` | `UID xxxx` / `None` |
| `IFX` | `Off` / `On` |

#### `CcHitsChannel`

| Attribute | Values |
|---|---|
| `Name` | ASCII, max 64 |
| `Index` | 0– |
| `Avoid` | `Off` / `T-Avoid` / `Avoid` |
| `CH_No` | 0–9 |
| `Freq` | `xxxx.xxxxMHz` |
| `Mod` | `Auto` / `AM` / `NFM` / `FM` / `WFM` / `FMB` |
| `Hold` | `Off` / `On` |
| `SAD` | Sub-audio |
| `LVL` | `-3` … `3` |
| `IFX` | `Off` / `On` |

#### `SearchRange`

| Attribute | Values |
|---|---|
| `Lower` | `xxxx.xxxxMHz` |
| `Upper` | `xxxx.xxxxMHz` |
| `Mod` | `Auto` / `AM` / `NFM` / `FM` / `WFM` / `FMB` |
| `Step` | (enumeration not given here; see `WaterfallBand Step`) |

#### `SearchBanks`

| Attribute | Values |
|---|---|
| `Index` | 0–9 |
| `BankStatus` | 10 characters, `0` = Off / `1` = On, in bank order `0123456789` |
| `Name` | ASCII, max 64 |
| `BankNo` | 0–9 |

#### `CC_Bands`

| Attribute | Values |
|---|---|
| `BandStatus` | 7 characters, `0` = Off / `1` = On, in band order `0123456` |

#### `CC_Counters`

Named in the matrix and the element list; **no attribute table is given**. Undocumented.

#### `ToneOutChannel`

| Attribute | Values |
|---|---|
| `Name` | ASCII, max 64 |
| `Index` | 1– (note: 1-based, unlike most) |
| `CH_No` | 0–31 |
| `Freq` | `xxxx.xxxxMHz` |
| `Mod` | `Auto` / `AM` / `NFM` / `FM` / `WFM` / `FMB` |
| `Hold` | `Off` / `On` |
| `LVL` | `-3` … `3` |
| `IFX` | `Off` / `On` |
| `ToneA` | `xxxxHz` |
| `ToneB` | `xxxxHz` |

#### `WxMode`

| Attribute | Values |
|---|---|
| `Mode` | `"Monitor Weather"` or `"Weather Alert"` |
| `SAME` | `"Alert Only"` or the SAME group name |

#### `WxChannel`

| Attribute | Values |
|---|---|
| `Name` | ASCII, max 64 |
| `Index` | 0– |
| `CH_No` | 1–7 |
| `Freq` | `xxxx.xxxxMHz` |
| `Mod` | `FM` |
| `Hold` | `Off` / `On` |
| `LVL` | `-3` … `3` |
| `IFX` | `Off` / `On` |

#### `ConventionalDiscovery`

| Attribute | Values |
|---|---|
| `Lower`, `Upper`, `Freq` | `xxxx.xxxxMHz` |
| `Mod` | `Auto` / `AM` / `NFM` / `FM` / `WFM` / `FMB` |
| `Step` | step size |
| `SAD` | Sub-audio |
| `RecSlot` | `Slot 1` / `Slot 2` / `None` |
| `PastTime` | elapsed session time |
| `HitCount` | hits so far |
| `TGID` | `TGID xxxx` / `None` |
| `U_Id` | `UID xxxx` / `None` |
| `IFX` | `Off` / `On` |

#### `TrunkingDiscovery`

| Attribute | Values |
|---|---|
| `SystemName` | ASCII, max 64 |
| `SiteName` | ASCII, max 64 |
| `TGID` | talkgroup |
| `TgidName` | talkgroup name |
| `SAD` | Sub-audio |
| `RecSlot` | `Slot 1` / `Slot 2` / `None` |
| `PastTime` | elapsed session time |
| `HitCount` | hits so far |
| `U_Id` | `UID xxxx` / `None` |

#### `SystemStatus`

| Attribute | Values |
|---|---|
| `SystemName` | ASCII, max 64 |
| `SiteName` | ASCII, max 64 |
| `Signal` | 0–100 |
| `Quality` | 0–100 |
| `Activity` | 0–100 |
| `SystemID` | 0–0x1FFFF |
| `SystemSubID` | 0–99 |
| `SiteID` | 0–4095 |
| `WacnID` | 0–0xFFFFF |
| `NAC` | 0–0xFFF |
| `Color` | 0–15 |
| `RAN` | 0–63 |
| `Area` | 0–1 |
| `Att` | `Off` / `G-Att` |
| `Freqs` | 0–16 |
| `P25Status` | `None` / `Data` / `P25` / `DMR` / `CAP` / `CON` / `DT3` / `XPT` / `NX9` / `NX4` / `ND9` / `ND4` / `IDS` / `NXD` |

#### `Analyze`

| Attribute | Values |
|---|---|
| `Msg1` | ASCII, max 64 |
| `Msg2` | ASCII, max 64 |
| `SystemName` | ASCII, max 64 |
| `SiteName` | ASCII, max 64 |
| `Att` | `Off` / `G-Att` |

Used by: LCN Finder, Current Activity, LCN Monitor, Activity Log.

#### `WaterfallBand`

| Attribute | Values |
|---|---|
| `Lower` | `xxxx.xxxxMHz` |
| `Center` | `xxxx.xxxxMHz` |
| `Upper` | `xxxx.xxxxMHz` |
| `Mod` | `Auto` / `AM` / `NFM` / `FM` / `WFM` / `FMB` |
| `Step` | `5kHz` / `6.25kHz` / `7.5kHz` / `833kHz`* / `10kHz` / `12.5kHz` / `15kHz` / `20kHz` / `25kHz` / `50kHz` / `100kHz` |
| `Span` | `360kHz` / `720kHz` / `1.44MHz` / `2.88MHz` / `5.76MHz` / `8.64MHz` / `17.28MHz` |
| `Limit` | `0` / `1` |

\* `833kHz` is a typo in the spec for `8.33kHz`.

#### `WaterfallSettings`

| Attribute | Values |
|---|---|
| `MF` | `xxxx.xxxxMHz` — marker frequency |
| `Gain` | `Auto` / `0` / `1` / … / `15` |

#### `RfPowerPlot`

| Attribute | Values |
|---|---|
| `Frequency` | `xxxx.xxxxMHz` |
| `Modulation` | `Auto` / `AM` / `NFM` / `FM` / `WFM` / `FMB` |
| `SampleRate` | `100ms` / `200ms` / `400ms` / `800ms` |
| `Att` | `Off` / `G-Att` |
| `B01`–`B34` | 0–100 each — 34 plot buckets |

### 7.6 `ViewDescription` children

The scanner has two "info areas" on the main screen that show quick-key status (scan mode) or bank status (custom search), plus an "over-write area" on the channel-name row used for error or scanning messages.

```xml
<ViewDescription>
   <InfoArea1 Text="F0:01234-6*789" />
   <InfoArea2 Text="S3:01234-6*---" />
   <OverWrite  Text="No thing to scan">
</ViewDescription>
```

Info-area text convention: a prefix (`F` = Favorites quick keys, `S` = System quick keys) then the current bank digit, a colon, and ten status characters for quick keys 0–9, where the digit itself means enabled, `-` means absent/disabled, and `*` marks the active one.

**Popup screens** — transient, 1–2 s, like an Android toast:

```xml
<ViewDescription>
   <PopupScreen Text="Global ATT\nOn"/>
</ViewDescription>
```

**Popup dialogs** — a popup that carries `<Button>` children is *not* dismissed automatically; the scanner waits for a key:

```xml
<ViewDescription>
   <PopupScreen Text="USB Cable Detected&#xD;
      Select USB mode&#xD;&#xD;Mass Storage=&quot;E&quot; / Serial Port=&quot;.&quot;">
      <Button Text="&quot;E&quot; (Yes)" KeyCode="E" />
      <Button Text="&quot;.&quot; (No)"  KeyCode="." />
   </PopupScreen>
</ViewDescription>
```

To press a button the app sends `KEY,<KeyCode>,P` — the spec's own example is `KEY,E,P`. This is the only place in either document where a `KEY_MODE` literal appears.

**HW:** while any `PopupScreen` is displayed, a key press only dismisses the popup — it does not reach the underlying screen. A client must detect `PopupScreen` in `GSI`, clear it, and re-send the intended key (§14).

**Plain text view:**

```xml
<ViewDescription>
   <PlainText Text="Copyright 2014"/>
   <PlainText Text="Uniden America Corp."/>
   <PlainText Text="All Rights Reserved."/>
   <PlainText Text=""/>
</ViewDescription>
```

### 7.7 `ReplayDescription`

```xml
<ReplayDescription>
 <File Index="2" />
 <ReplayMode Mode="USER_REC" />
</ReplayDescription>
```

`ReplayMode Mode` is shown as `USER_REC`; the complementary value for instant replay is not printed in the spec (expect `INNER_REC`/`IREC` — unverified).

### 7.8 Full worked example (scan mode)

```xml
<?xml version="1.0" encoding="utf-8"?>
<ScannerInfo Mode="Trunk Scan Hold" V_Screen="trunk_scan">
   <MonitorList Name="Full Database" Index="4294967295" ListType="FullDb" Q_Key="None" N_Tag="None" DB_Counter="3" />
   <System Name="Calcasieu" Index="283" Avoid="Off" SystemType="Conventional" Q_Key="None" N_Tag="None" Hold="On" />
   <Department Name="Calcasieu Parish - Parish Fire &amp; Medical" Index="286" Avoid="Off" Q_Key="None" Hold="Off" />
   <ConvFrequency Name="DeQuincy Fire Department" Index="290" Avoid="Off" Freq=" 154.4150MHz"
        Mod="NFM" N_Tag="None" Hold="On" SvcType="Fire Dispatch" P_Ch="Off" SAS="All" SAD="None" LVL="0" IFX="Off" />
   <AGC A_AGC="Off" D_AGC="Off" />
   <DualWatch PRI="Off" CC="Off" WX="Off" />
   <Property
              VOL="0" SQL="9" Sig="0"
              WiFi="3" Att="Off" Rec="Off"
              KeyLock="Off" P25Status="None"
              Mute="Mute" Backlight="100"
              Rssi="0.377"
   />
   <ViewDescription>
        <InfoArea1 Text="F0:01234-6*789" />
        <InfoArea2 Text="S3:01234-6*---" />
        <PopupScreen Text="Quick Save?\n"/>
   </ViewDescription>
</ScannerInfo>
```

Note that this example omits `F`, `Battery`, `A_Led` and `Dir` from `Property` — attributes are emitted only when meaningful, so a parser must treat every attribute as optional.

### 7.9 "Sub Audio" sheet — missing

`SAS` and `SAD` both say *"See Sheet: Sub Audio"*. **That sheet is not present in either PDF.** The attribute carries CTCSS tone, DCS code, P25 NAC, DMR colour code, NXDN RAN or EDACS area depending on system type (per the `GLT` legend), plus the literals `All` and `None` seen in the worked example. The complete value set is undocumented (§15).

---

## 8. The menu system — `MNU`, `MSI`, `MSV`, `MSB`

### 8.1 Model

The scanner's menu is a tree of screens. A controller:

1. **Enters** the menu at a chosen node with `MNU`.
2. **Reads** the current screen with `MSI`, which returns its type, its items, and which item is selected.
3. **Writes** a value with `MSV`.
4. **Navigates back** one level, or exits menu mode entirely, with `MSB`.

While in the menu, `GSI` reports `V_Screen="menu_selection"` or `"menu_input"` and `Mode="Menu tree"`.

### 8.2 `MNU` — enter a menu node

```
→ MNU,[MENU_ID],[INDEX]\r
← MNU,OK\r
```

| `MENU_ID` | `INDEX` | Menu position |
|---|---|---|
| `TOP` | — | Top (Main) Menu |
| `MONITOR_LIST` | — | Select Lists to Monitor menu |
| `SCAN_SYSTEM` | System Index | System Menu |
| `SCAN_DEPARTMENT` | Department Index | Department Menu |
| `SCAN_SITE` | Site Index | Site Menu |
| `SCAN_CHANNEL` | Channel Index | Channel Menu |
| `SRCH_RANGE` | Custom Bank Index | Custom Search Bank Menu |
| `SRCH_OPT` | — | Search/Close Call Opt menu |
| `CC` | — | Close Call Menu |
| `CC_BAND` | — | Close Call Band Menu |
| `WX` | — | WX Operation Menu |
| `FTO_CHANNEL` | FTO Channel Index | Tone Out Channel Menu |
| `SETTINGS` | — | Settings Menu |
| `BRDCST_SCREEN` | — | Broadcast screen Menu |

Where `INDEX` is "—", send an empty field: `MNU,SETTINGS,\r`.

### 8.3 `MSI` — read the current menu screen

```
→ MSI\r
← MSI,<XML>,\r
  <?xml version="1.0" encoding="utf-8"?>\r
  <MSI Name=" Title " Index="xxxxxx " >\r
    …
  </MSI>\r
```

#### `MSI` attributes

| Attribute | Values |
|---|---|
| `Name` | Menu title (the heading shown on screen) |
| `Index` | Menu index |
| `MenuType` | `TypeSelect` / `TypeInput` / `TypeLocation` / `TypeError` |
| `Value` | Current set value |
| `Selected` | (value set not printed — the index of the currently highlighted `MenuItem`) |

#### `MenuItem` attributes — children of a `TypeSelect` menu

| Attribute | Values |
|---|---|
| `Name` | Item name |
| `Index` | Item index |
| `Value` | Menu item current value |

#### `MenuInput` attributes — child of a `TypeInput` menu

| Attribute | Values |
|---|---|
| `MaxLength` | 1–64 |
| `EnableKeys` | The characters the user may input |
| `AddedInformation` | String (hint/suffix shown with the field) |

#### `MenuLocation` attributes — child of a `TypeLocation` menu

| Attribute | Values |
|---|---|
| `MaxLength` | 1– |
| `EnableKeys` | The characters the user may input |
| `IsLatitude` | `"1"` = latitude, `"0"` = longitude |

#### `MenuErrorMsg` attributes — child of a `TypeError` menu

| Attribute | Values |
|---|---|
| `Text` | Error message |
| `ScanButton` | `"1"` = enabled, `"0"` = disabled |

### 8.4 `MSV` — set a value

```
→ MSV,[RSV],[VALUE]\r
← MSV,OK\r
```

| Field | Meaning |
|---|---|
| `RSV` | Reserved — send empty |
| `VALUE` | For a **select**-type menu: the index of the item to select. For an **input**-type menu: the string to enter |

**Escaping:** if `VALUE` contains a comma, replace it with a TAB (`\t`) before sending.

`MSV` on a `TypeSelect` screen both selects the item and activates it — i.e. it is equivalent to scrolling to the item and pressing E/Enter. If the item is a sub-menu, the scanner descends into it, and the next `MSI` returns the sub-menu.

### 8.5 `MSB` — go back

```
→ MSB,[RSV],[RET_LEVEL]\r
← MSB,OK\r
```

| `RET_LEVEL` | Effect |
|---|---|
| `RETURN_PREVOUS_MODE` | Exit menu mode entirely and return to the previous operating mode. **Note the spelling: "PREVOUS", not "PREVIOUS"** — send it exactly as printed |
| *(empty)* | Go back one level |

`RSV` is reserved — send empty: `MSB,,RETURN_PREVOUS_MODE\r` or `MSB,,\r`.

### 8.6 Worked example — change a Settings item

Goal: set *Settings → Set Backlight → Set Mode* to *Keypress*.

```
# 1. Enter the Settings menu.
→ MNU,SETTINGS,\r
← MNU,OK\r

# 2. Read the screen.
→ MSI\r
← MSI,<XML>,\r
  <?xml version="1.0" encoding="utf-8"?>
  <MSI Name="Settings" Index="0" MenuType="TypeSelect" Selected="0">
    <MenuItem Name="Set Backlight"   Index="0" />
    <MenuItem Name="Adjust Key Beep" Index="1" />
    <MenuItem Name="Set Audio"       Index="2" />
    <MenuItem Name="Set Language"    Index="3" />
    ...
  </MSI>

# 3. Select "Set Backlight" — item index 0.
→ MSV,,0\r
← MSV,OK\r

# 4. Read the sub-menu that we descended into.
→ MSI\r
← MSI,<XML>,\r
  <MSI Name="Set Backlight" Index="..." MenuType="TypeSelect" Selected="0">
    <MenuItem Name="Set Mode"     Index="0" Value="Keypress" />
    <MenuItem Name="Set Dimmer"   Index="1" Value="High" />
    <MenuItem Name="Set Color"    Index="2" Value="Blue" />
  </MSI>

# 5. Descend into "Set Mode".
→ MSV,,0\r
← MSV,OK\r

# 6. Read the value list. Note MenuType is still TypeSelect, and Value
#    on the <MSI> element carries the value currently in force.
→ MSI\r
← MSI,<XML>,\r
  <MSI Name="Set Mode" Index="..." MenuType="TypeSelect" Value="Keypress" Selected="1">
    <MenuItem Name="Always On"   Index="0" />
    <MenuItem Name="10 sec"      Index="1" />
    <MenuItem Name="30 sec"      Index="2" />
    <MenuItem Name="Keypress"    Index="3" />
    <MenuItem Name="Squelch"     Index="4" />
    <MenuItem Name="Keypress+Sq" Index="5" />
  </MSI>

# 7. Commit the new value — index 3 ("Keypress").
→ MSV,,3\r
← MSV,OK\r

# 8. Confirm.
→ MSI\r
← ...  <MSI ... Value="Keypress" ...>

# 9. Leave the menu and go back to scanning.
→ MSB,,RETURN_PREVOUS_MODE\r
← MSB,OK\r
```

For a text field the sequence is the same until step 7, where `MSI` returns `MenuType="TypeInput"` with a `<MenuInput MaxLength="16" EnableKeys="0123456789ABC…" />` child; `MSV,,NewName\r` then writes the string. For a latitude/longitude field `MenuType="TypeLocation"` with `<MenuLocation IsLatitude="1" .../>`; the two coordinates are separate screens.

If a write is rejected the next `MSI` returns `MenuType="TypeError"` with a `<MenuErrorMsg Text="…" ScanButton="1"/>` child; clear it with `MSB,,\r`.

**Indices are not stable across firmware versions or models** — always read the live `MSI` and match on `Name`, never hard-code an `Index`.

---

## 9. `AST` / `APR` — analyse modes

Start a mode with `AST`; pause or resume it with `APR` (§4.22). Only one analyse mode runs at a time. All six site-indexed modes carry the same warning:

> ※ Before sending `AST` command, please go to Scan Mode to load the hpdb data.

Three modes are struck out in V2.00 as **"Removed in SDS100"** — the whole block is greyed and crossed through on the page: **Band Scope**, **Raw Data Output** and **RF Power Plot**. They are documented here for completeness and because they remain in V0.01, but a client should not rely on them on any SDS-series radio.

### 9.1 Summary

| Mode | Request | Output form | Rate | `APR` | Status |
|---|---|---|---|---|---|
| Current Activity | `AST,CURRENT_ACTIVITY,[Site Index]` | XML `<AST><CurrentActivity …/></AST>` | 200 ms | ✓ | current |
| LCN Monitor | `AST,LCN_MONITOR,[Site Index]` | XML `<AST><LcnMonitor …/></AST>` | 1 s | ✓ | current |
| LCN Finder | `AST,LCN_FINDER,[Site Index]` | XML `<AST><LcnFinder …/></AST>` | 500 ms | ✗ | current |
| Activity Log | `AST,ACTIVITY_LOG,[Site Index]` | line, `AST,ACTIVITY_LOG,…` | per message | ✓ | current |
| System Status | `AST,SYSTEM_STATUS,[site_index]` | `AST,OK`, then `SystemStatus` element in `GSI`/`PSI` | via `GSI` | ✓ | current |
| Band Scope | `AST,BAND_SCOPE,[Center],[Span],[Step],[Modulation]` | line, `AST,BAND_SCOPE,[Freq],[RSSI]` | 10 ms | ✗ | **removed** |
| Raw Data Output | `AST,RAW_DATA_OUTPUT,[Freq],[Mod],[Filter],[GlobalAtt]` | raw binary | continuous | ✓ | **removed**, USB only |
| RF Power Plot | `AST,RF_POWER_PLOT,[Freq],[Mod],[SamplingRate]` | `AST,OK`, then `RfPowerPlot` element in `GSI`/`PSI` | via `GSI` | ✓ | **removed** |

Note the asymmetry: `SYSTEM_STATUS` and `RF_POWER_PLOT` answer `AST,OK` and then publish their data as `GSI`/`PSI` elements; the others stream their own datagrams.

Also note `LCN_FINDER` and `BAND_SCOPE` are absent from `APR`'s mode list — they cannot be paused, only replaced by starting another mode or leaving analyse.

### 9.2 Current Activity

```
→ AST,CURRENT_ACTIVITY,[Site Index]\r
← AST,<XML>,\r                                (every 200 ms)
  <?xml version="1.0" encoding="utf-8"?>
  <AST>
      <CurrentActivity LCN="1" Freq="851.0125" SystemID="0001h" SiteID="0" TgidType="Control Channel" />
      <CurrentActivity LCN="2" Freq="851.0375" TGID="16"  UnitID="32"  MOD="Analog" TgidType="TGID" />
      <CurrentActivity LCN="3" Freq="851.0625" TGID="64"  UnitID="128" MOD="Analog" TgidType="TGID" />
      :
      <CurrentActivity LCN="32" Freq="851.6125" TGID="256" UnitID="512" MOD="Analog" Tgidype="TGID" />
  </AST>
```

Two record shapes: a **control channel** row and a **voice channel** row.

| Row | Fields, in order |
|---|---|
| C‑Ch | `No`, `LCN`, `Frequency`, `SystemID`, `SiteID`, `TGID Type` |
| V‑Ch | `No`, `LCN`, `Frequency`, `TGID`, `Unit ID`, `MOD`, `TGID Type` |

| Attribute | Meaning |
|---|---|
| `LCN` | LCN, decimal |
| `Freq` | Frequency, bare decimal MHz |
| `TGID` | Talkgroup ID, decimal |
| `UnitID` | Unit ID, decimal |
| `MOD` | `Analog` / `Digital` / `Encrypted` |
| `TgidType` | `Control Channel` / `Encrypted` / `Patch` / `Unknown` / `TGID` / `I-CALL` |
| `SystemID` | System ID, hex (suffixed `h`, e.g. `0001h`) |
| `SiteID` | Site ID, decimal |

**Spec defect:** the last example row spells the attribute `Tgidype` instead of `TgidType`. Parse defensively.

### 9.3 LCN Monitor

```
→ AST,LCN_MONITOR,[Site Index]\r
← AST,<XML>,\r                                (every 1 s)
  <AST>
      <LcnMonitor LCN="1" Freq = "851.0125" ReceiveStaus="1" />
      <LcnMonitor LCN="2" Freq = "851.0250" ReceiveStaus="0" />
      :
      <LcnMonitor LCN="32" Freq = "851.4000" ReceiveStaus="0" />
  </AST>
```

| Field | Meaning |
|---|---|
| `LCN` | LCN, decimal |
| `Freq` | Frequency |
| `ReceiveStaus` | `1` (receiving) or `0`. **Note the spec's misspelling — the attribute really is `ReceiveStaus`, not `ReceiveStatus`.** The column heading in the layout table calls it `Status` |

Note also the spaces around `=` in `Freq = "…"` in the spec's example — a conforming XML parser handles this, a regex-based one may not.

### 9.4 LCN Finder

```
→ AST,LCN_FINDER,[Site Index]\r
← AST,<XML>,\r                                (every 500 ms)
  <AST>
      <LcnFinder Freq="851.0125" AccuracyStatus="566666666666666666666666666666"
      <LcnFinder Freq="851.0375" AccuracyStatus="610000000000000000000000000000"
      <LcnFinder Freq="851.0625" AccuracyStatus="603000000000000000000000000000"
      :
      <LcnFinder Freq="851.6125" AccuracyStatus="600000000000000000000000000000"
      <LcnFinder Condition="Searching"/>
  </AST>
```

| Field | Meaning |
|---|---|
| `Freq` | Frequency |
| `AccuracyStatus` | **30 characters**, one digit per candidate LCN slot ("Accuracy Level (Total 30 status)") |
| `Condition` | `Searching` or `All Lcn Found` — carried on a final, frequency-less record |

`AccuracyStatus` digit values:

| Digit | Meaning |
|---|---|
| `0` | Unknown |
| `1` | Level 1 |
| `2` | Level 2 |
| `3` | Level 3 |
| `4` | Level 4 |
| `5` | Found |
| `6` | Disable |

**Spec defect:** in the printed example the `<LcnFinder>` elements have no closing `/>`. Real output is well-formed.

### 9.5 Activity Log

Not XML — one plain line per decoded message.

```
→ AST,ACTIVITY_LOG,[Site Index]\r
← AST,ACTIVITY_LOG,[Time],[Data],[Message],[Description]
```

※ If a temporary clock was set, going to activity-log mode returns `NG`.

| Field | Meaning |
|---|---|
| `Time` | `MM/DD/YYYY hh:mm:ss` |
| `Data` | Received raw data; format depends on system type |
| `Message` | Message type; depends on system type |
| `Description1`–`Description5` | Message description fields; **how many are present depends on the message type** |

Each description field is emitted as a `Label:` prefix followed by the value (e.g. `Tid:1234`).

#### 9.5.1 Motorola

`Data` = `"<cmd>/<prv>/<id>"` — `cmd` command field 0–1023 decimal; `prv` private bit 0 or 1; `id` id field 0–65535 decimal.

| Message | D1 | D2 | D3 | D4 | D5 |
|---|---|---|---|---|---|
| System ID | `Sid:` | | | | |
| Site ID | `Site:` | | | | |
| Talkgroup Voice Channel Grant | `Tid:` | `Uid:` | `Lcn:` | `Sts:` | `Mod:` |
| Talkgroup Voice Channel Grant Update | `Tid:` | | `Lcn:` | `Sts:` | |
| I-Call Voice Channel Grant Update | `Uid:` | | `Lcn:` | | |
| Individual Call | `Uid:` | `Uid:` | `Lcn:` | | |
| Patch/MultiSelect Voice Channel Grant | `Pid:` | `Uid:` | `Lcn:` | `Sts:` | `Mod:` |
| Patch/Multiselect Voice Channel Grant Update | `Pid:` | | `Lcn:` | `Sts:` | |
| Patch List | `Pid:` | `Mid` | | | |
| Patch Cancel | `Pid:` | | | | |
| Control | | | | | |
| First OSW | | | | | |
| Receive Error | | | | | |

Labels: `Sid` system ID (hex); `Site` site ID (dec); `Tid` talkgroup (dec); `Uid` unit ID (dec); `Pid` patch ID (dec); `Mid` patch member ID (dec); `Lcn` LCN (dec); `Mod` `Analog`/`Digital`.

`Sts` (status bit) values: Normal Talkgroup · All Talkgroup · Emergency · Talkgroup Patch · Emergency Patch · Emergency Multi-Group · Multi-Select · DES Encryption Talkgroup · DES All Talkgroup · DES Emergency · DES Talkgroup Patch · DES Emergency Patch · DES Emergency Multi-Group · Multi-Select DES TG.

#### 9.5.2 P25 Standard

`Data` = `"<opecode>/<data>"` — `opecode` 1 byte `00`–`FF` hex; `data` TSBK 12 bytes hex (`000000000000000000000000`–`FFFFFFFFFFFFFFFFFFFFFFFF`).

Messages **with** description fields:

| Message | D1 | D2 | D3 | D4 | D5 |
|---|---|---|---|---|---|
| Group Voice Channel Grant | `Lcn:` | `Gad:` | `Sad:` | | |
| Group Voice Channel Grant Explicit | `LcnT:` | `Gad:` | `Sad:` | `LcnR:` | |
| Group Voice Channel Grant Update | `Lcn:` | `Gad:` | `Lcn:` | `Gad:` | |
| Group Voice Channel Grant Update Explicit | `LcnT:` | `LcnR:` | `Gad:` | | |
| Unit To Unit Voice Channel Grant | `Lcn:` | `Tad:` | `Sad:` | | |
| Unit To Unit Voice Channel Grant Extended | `LcnT:` | `Tad:` | `Sad:` | `LcnR:` | |
| Unit To Unit Answer Request | `Tad:` | `Src:` | | | |
| Unit To Unit Answer Request Extended | `Tad:` | `Src:` | | | |
| Unit To Unit Voice Channel Grant Update | `Lcn:` | `Tad:` | `Sad:` | | |
| Unit To Unit Voice Channel Grant Update Extended | `LcnT:` | `Tad:` | `Sad:` | `LcnR:` | |
| Identifier Update for TDMA | `Iden:` | `Type:` | `Tofs:` | `Csp:` | `Bfrq:` |
| Time and Date Announcement | `Iden:` | `Bw:` | `Tofs:` | `Csp:` | `Bfrq:` |
| RFSS Status Broadcast | `Sid:` | `Sub:` | `Site:` | `Lcn:` | |
| RFSS Status Broadcast Extended | `Sid:` | `Sub:` | `Site:` | `LcnT:` | `LcnR:` |
| Network Status Broadcast | `Wacn:` | `Sid:` | `Lcn:` | | |
| Network Status Broadcast Extended | `Wacn:` | `Sid:` | `LcnT:` | `LcnR:` | |
| Identifier Update for non-VHF/UHF Bands | `Iden:` | `Bw:` | `Tofs:` | `Csp:` | `Bfrq:` |

Messages recognised but carrying **no** description fields: Telephone Voice Channel Grant · Telephone Interconnect Answer Request · Identifier Update for X2TDMA · Individual Data Channel Grant · Group Data Channel Grant · Group Data Channel Announcement · Group Data Channel Announcement Explicit · SNDCP Data Channel Grant · SNDCP Data Page Request · SNDCP Data Channel Announcement Explicit · Status Update · Status Query · Message Update · Radio Unit Monitor Command · Call Alert · Acknowledge Response FNE · Queued Response · Extended Function Command · Deny Response · Group Affiliation Response · Secondary Control Channel Broadcast Explicit · Group Affiliation Query · Location Registration Response · Unit Registration Response · Unit Registration Command · Authentication Command · De-Registration Acknowledge · Identifier Update for VHF/UHF Bands · Roaming Address Command · Roaming Address Update · System Service Broadcast · Secondary Control Channel Broadcast · Adjacent Status Broadcast · Protection Parameter Broadcast · Protection Parameter Update · Receive Error.

Labels: `Lcn` LCN (dec); `LcnT` transmit-channel LCN (dec); `LcnR` receive-channel LCN (dec); `Gad` group address (dec); `Sad` source address (dec); `Tad` target address (dec); `Src` source ID (dec); `Iden` identifier (dec); `Bw` bandwidth (dec); `Tofs` transmit offset (dec); `Csp` channel spacing (dec); `Bfrq` base frequency (dec); `Sid` system ID (hex); `Sub` RF sub-system ID (dec); `Site` site ID (dec); `Wacn` WACN ID (hex); `Type` channel type (dec).

#### 9.5.3 EDACS

`Data` = `"<data>"` — message data, 28 bits, `0000000`–`FFFFFFF` hex.

| Message | D1 | D2 | D3 | D4 |
|---|---|---|---|---|
| Site ID | `Site:` | | `Lcn:` | |
| Talkgroup Voice Channel Grant | `Tid:` | `Uid:` | `Lcn:` | `Sts:` |
| Talkgroup Voice Channel Grant Update | `Tid:` | | `Lcn:` | `Sts:` |
| I-Call Voice Channel Grant Update | `Uid:` | | `Lcn:` | `Sts:` |
| Patch Voice Channel Grant | `Pid:` | `Uid:` | `Lcn:` | `Sts:` |
| Patch Voice Channel Grant Update | `Pid:` | | `Lcn:` | `Sts:` |
| Patch List | `Pid:` | `Mid:` | | |
| First OSW | | | | |
| Receive Error | | | | |

Labels: `Site` site ID (dec); `Tid` talkgroup — **decimal 1–2047 is AFS format, decimal 2048–65535 is plain decimal**; `Uid` unit ID (dec); `Pid` patch ID (dec); `Mid` patch member ID (dec); `Lcn` LCN (dec).

`Sts` values: Normal Talkgroup · Talkgroup Patch · Emergency · Emergency Patch · Digital Talkgroup · Digital Patch · Digital Emergency · Digital Emergency Patch · I-Call · Digital I-Call.

#### 9.5.4 LTR

`Data` = `<area_code>/<goto>/<home>/<id>/<free>` — area code 0 or 1; goto repeater 0–31; home repeater 0–31; id field 0–255; free repeater 0–31.

| Message | D1 | D2 | D3 | D4 |
|---|---|---|---|---|
| Repeater Idle | `Tid:` | `Rpt:` | `Goto:` | `Free:` |
| Talkgroup Voice Channel Grant Update | `Tid:` | `Rpt:` | `Goto:` | `Free:` |
| Turn-off Code | `Tid:` | `Rpt:` | `Goto:` | `Free:` |

Labels: `Tid` talkgroup as Area‑Home‑Id; `Rpt` transmitting repeater; `Goto` goto repeater; `Free` free repeater.

#### 9.5.5 DMR / MotoTRBO

`Data` = `"<opcode>/<fid>/<id>/<ch>/<slot>/<prv>/<emergency>"`

| Part | Range |
|---|---|
| `opcode` | Full/Short Link Control opcode, or Control Signal Block opcode, `00`–`3F` hex |
| `fid` | Feature ID: `00` = DMR, `06` = Connect Plus, `10` = Capacity Plus (hex) |
| `id` | TGID 0–16777215 (dec) |
| `ch` | LCN 0–4095 (dec) |
| `slot` | TDMA slot `1`, `2`, or `15` = none (dec) |
| `prv` | Privacy 0 or 1 |
| `emergency` | Emergency 0 or 1 |

| Message | D1 | D2 | D3 | D4 | D5 |
|---|---|---|---|---|---|
| Talkgroup Voice Channel Grant | `Tid:` | `Uid:` | `Color Code:` | `Lcn:` | `Slot:` |
| Talkgroup Voice Channel Link Control | `Tid:` | `Uid:` | `Color Code:` | `Lcn:` | `Slot:` |
| Unit to Unit Voice Channel Grant | `Uid Src:` | `Uid Dst:` | `Color Code:` | `Lcn:` | `Slot:` |
| Unit to Unit Voice Channel Link Control | `Uid Src:` | `Uid Dst:` | `Color Code:` | `Lcn:` | `Slot:` |
| Broadcast Talkgroup Voice Channel Grant | `Tid:` | `Uid:` | `Color Code:` | `Lcn:` | `Slot:` |
| Capacity Plus Voice Channel Grant | `Tid:` | `Uid:` | `Color Code:` | `Lcn:` | `Slot:` |
| Capacity Plus Update | `Sid:` | `Site:` | `Color Code:` | `Lcn:` | `Slot:` |
| Capacity Plus Site ID | `Sid:` | `Site:` | `Color Code:` | `Lcn:` | `Slot:` |
| Linked Capacity Plus Site ID | `Sid:` | `Site:` | `Color Code:` | `Lcn:` | `Slot:` |
| Connect Plus Voice Channel Grant | `Tid:` | `Uid:` | `Color Code:` | `Lcn:` | `Slot:` |
| Connect Plus Update | | | | | |
| Connect Plus Network ID | `Sid:` | `Site:` | `Color Code:` | `Lcn:` | `Slot:` |
| DMR Network ID | `Sid:` | `Site:` | `Color Code:` | `Lcn:` | `Slot:` |
| Idle | | | | | |

Labels: `Sid` network ID (hex); `Site` site ID (dec); `Tid` talkgroup (dec); `Uid` unit ID (dec); `Uid Src` source unit ID (dec); `Uid Dst` destination unit ID (dec); `Color Code` (dec); `Lcn` (dec); `Slot` TDMA slot (dec).

#### 9.5.6 NXDN

`Data` = `"<call type>/<home ch>/<id>/<ch>/<prv>/<emergency>"`

| Part | Range |
|---|---|
| `call type` | 0–7 (dec) |
| `home ch` | 0–31 (IDAS only, dec) |
| `id` | TGID — NEXEDGE 0–65535, IDAS 0–2047 (dec) |
| `ch` | LCN 0–1023 (dec) |
| `prv` | Privacy 0 or 1 |
| `emergency` | Emergency 0 or 1 |

Several messages have **two** row variants: a group-call form and an individual-call form.

| Message | D1 | D2 | D3 | D4 |
|---|---|---|---|---|
| Replying to requesting communication (group) | `Tid:` | `Uid:` | `Area Code:` | `Home Ch:` |
| Replying to requesting communication (individual) | `Uid Src:` | `Uid Dst:` | `Area Code:` | `Home Ch:` |
| Performing voice communication (group) | `Tid:` | `Uid:` | `RAN:` or `Area Code:` | |
| Performing voice communication (individual) | `Uid Src:` | `Uid Dst:` | `RAN:` or `Area Code:` | |
| Sending Encryption init vector | | | | |
| Assignment of traffic channel to VC (group) | `Tid:` | `Uid:` | `RAN:` or `Area Code:` | `LCN:` or `Home Ch:` |
| Assignment of traffic channel to VC (individual) | `Uid Src:` | `Uid Dst:` | `RAN:` or `Area Code:` | `LCN:` or `Home Ch:` |
| Existence of assigned traffic channel to VC (group) | `Tid:` | `Uid:` | `RAN:` or `Area Code:` | `LCN:` or `Home Ch:` |
| Existence of assigned traffic channel to VC (individual) | `Uid Src:` | `Uid Dst:` | `RAN:` or `Area Code:` | `LCN:` or `Home Ch:` |
| Transmission released | | | | |
| Idle | | | | |
| Disconnecting | | | | |
| Site configuration information | `Sys:` | `Site:` | `RAN:` | `Cch LCN:` or `DFA` |
| Service information which site provides | `Sys:` | `Site:` | `RAN:` | |
| Information of site's control channel | `Sys:` | `Site:` | `RAN:` | `Cch LCN:` or `DFA` |
| IDAS go to Repeater (group) | `Tid:` | `Uid:` | `Area Code:` | `Go to Repeater:` |
| IDAS go to Repeater (individual) | `Uid Src:` | `Uid Dst:` | `Area Code:` | `Go to Repeater:` |

Labels: `Sys` system ID (dec); `Site` site ID (dec); `Tid` talkgroup (dec); `Uid` unit ID (dec); `Uid Src` individual-call source unit ID (dec); `Uid Dst` individual-call destination unit ID (dec); `RAN` NEXEDGE RAN (dec); `Area Code` IDAS area code (dec); `LCN` NEXEDGE LCN (dec); `Go to Repeater` IDAS repeater channel (dec); `Home Ch` IDAS home channel (dec); `Cch LCN` NEXEDGE control channel (dec); `DFA` NEXEDGE direct frequency assignment.

### 9.6 Band Scope — **removed in SDS100**

```
→ AST,BAND_SCOPE,[Center frequency],[Span],[Step],[Modulation]\r
← AST,BAND_SCOPE,[Frequency],[RSSI_LEVEL]\r      (once per frequency change)
```

Data is sent at a 10 ms interval; the spec adds "the data is output every time the frequency is changed".

| Param | Values |
|---|---|
| `Center frequency` | `250000`–`13000000` (units of 100 Hz → 25 MHz – 1300 MHz) |
| `Span` | `200`, `400`, `600`, `800`, `1000`, `2000`, `4000`, `6000`, `8000`, `10000`, `20000`, `40000`, `60000`, `80000`, `100000`, `200000` — kHz, i.e. 0.2 MHz … 200 MHz |
| `Step` | `500`, `625`, `750`, `833`, `1000`, `1500`, `2000`, `2500`, `5000`, `10000` — Hz (`833` means 8.33 kHz) |
| `Modulation` | `Auto`, `AM`, `NFM`, `FM`, `WFM`, `FMB` |

Response fields: `Frequency`, then RSSI level `0`–`100` (the spec's parameter list calls the second field `Status`).

### 9.7 Raw Data Output — **removed in SDS100**, USB only

```
→ AST,RAW_DATA_OUTPUT,[Frequency],[Modulation],[Filter],[Global Attenuator]\r
← (raw binary stream)
```

| Param | Values |
|---|---|
| `Frequency` | `250000`–`13000000` (100 Hz units) |
| `Modulation` | `Auto`, `AM`, `NFM`, `FM`, `WFM`, `FMB` |
| `Filter` | `1` = On, `0` = Off |
| `Global Attenuator` | `1` = On, `0` = Off |

Output is **discriminator A/D sampling raw data, 10-bit signed**, split across two bytes:

| Byte | b7 | b6 | b5 | b4 | b3 | b2 | b1 | b0 |
|---|---|---|---|---|---|---|---|---|
| **H** | **1** | 0 | 0 | bit9 | bit8 | bit7 | bit6 | bit5 |
| **L** | **0** | 0 | 0 | bit4 | bit3 | bit2 | bit1 | bit0 |

Decoder: a byte with b7 = 1 is a high byte; b7 = 0 is a low byte. Sample = `((H & 0x1F) << 5) | (L & 0x1F)`, then sign-extend from bit 9 (subtract 1024 if bit 9 set). Byte-sync on the b7 flag — do not assume alignment.

> ※ The interface for raw-data-output mode is the USB port only. If you want to use another remote command, send the pause command first.

### 9.8 System Status

```
→ AST,SYSTEM_STATUS,[site_index]\r
← AST,OK\r
```

The data itself arrives as the `<SystemStatus>` element of `GSI`/`PSI` (§7.5), with `V_Screen="analyze_system_status"`. Poll `GSI` to read it.

### 9.9 RF Power Plot — **removed in SDS100**

```
→ AST,RF_POWER_PLOT,[Frequency],[Modulation],[Sampling Rate]\r
← AST,OK\r
```

| Param | Values |
|---|---|
| `Frequency` | `250000`–`13000000` (100 Hz units) |
| `Modulation` | `Auto`, `AM`, `NFM`, `FM`, `WFM`, `FMB` |
| `Sampling Rate` | `100`, `200`, `400`, `800` (ms) |

Data arrives as the `<RfPowerPlot>` element of `GSI`/`PSI` — 34 buckets `B01`–`B34`, each 0–100 (§7.5).

---

## 10. Waterfall

Added in V1.01 (`JPM,WF_MODE`, `PWF`, `GWF`), extended in V1.02 (`GST`) and V2.00 (`GW2`).

### 10.1 Entering waterfall mode

```
→ JPM,WF_MODE,\r
← JPM,OK\r
```

`INDEX` is reserved for `WF_MODE` — send it empty. Confirm with `GST`: `WF_MODE` field = `1`. (`GSI` reports `V_Screen="waterfall"`, which — see §7.2 — is not in the published enumeration.)

Leave waterfall mode with another `JPM` (e.g. `JPM,SCN_MODE,4294967295`).

### 10.2 Geometry

The waterfall is **240 FFT bins** wide, always. The mapping from bin index to frequency comes from `GST`:

```
bin_width = (UPPER - LOWER) / 240
freq(i)   = LOWER + (i + 0.5) * bin_width        for i in 0..239
```

`CF` is the centre frequency, `MF_POS` is the marker's bin index (0–239) and `FREQ` the marker's frequency, so `MF_POS` and `FREQ` give an independent check of the mapping. `FFT_SIZE` (25/50/75/100 %) is how much of the screen the FFT panel occupies — it does **not** change the bin count. `COLOR_MODE` selects the palette. The band's `Lower`/`Center`/`Upper`/`Mod`/`Step`/`Span`/`Limit` and the marker frequency and `Gain` are also available as the `WaterfallBand` and `WaterfallSettings` elements in `GSI` (§7.5).

### 10.3 `PWF` — push mode

```
→ PWF,[FFT_TYPE],[ON/OFF]\r
← PWF,[DATA1],[DATA2],…,[DATA_n],\r     (repeating, unsolicited)
```

`FFT_TYPE` = `1` (Displayed FFT, 240 bins) is the only defined type. `n` is 240 for type 1.

### 10.4 `GWF` — polled, ASCII

```
→ GWF,[TYPE],[ON/OFF]\r
← GWF,[DATA1],[DATA2],…,[DATA240],\r
```

**HW:** each `DATA` value is always **two hexadecimal digits** (`00`–`FF`), zero-padded, upper case — never one digit, never decimal (§14). So the payload after `GWF,` is exactly 240 × 3 = 720 characters (`XX,` repeated), then CR.

Parse as:

```
payload = datagram[4:-1]            # strip "GWF," and the CR
bins    = [int(t, 16) for t in payload.rstrip(',').split(',')]
assert len(bins) == 240
```

Each value is a relative magnitude: 0 = noise floor, 255 = full scale. The spec gives no dB mapping; scale linearly to the palette.

### 10.5 `GW2` — polled, binary

```
→ GW2,[TYPE],[ON/OFF]\r
← GW2,[FFT_DATA]\r
```

`FFT_DATA` is 240 raw bytes, one per bin, same magnitude scale as `GWF`. This is the command to use for a high-rate display: 244 bytes per frame instead of 725.

Because the payload is binary, **do not scan for CR** — read exactly 240 bytes after the four-byte `GW2,` prefix.

(Remember the spec prints `GWF` in the `GW2` section body; the token is `GW2`.)

### 10.6 Choosing between them

| Need | Use |
|---|---|
| Lowest bandwidth, highest frame rate | `GW2` |
| Firmware that predates V2.00 | `GWF` |
| Free-running stream without polling | `PWF` |
| Marker, span, colour mode, screen text | `GST` (poll alongside) |

---

## 11. (reserved)

---

## 12. Service types

`SVC` sends and receives 47 fields: `PST1`…`PST37` then `CST1`…`CST10` (§4.17). Rows marked *non* are unused slots that still occupy their position.

### 12.1 Pre-set service types

| id | Service type name | | id | Service type name |
|---|---|---|---|---|
| PST1 | Multi-Dispatch | | PST20 | Railroad |
| PST2 | Law Dispatch | | PST21 | Other |
| PST3 | Fire Dispatch | | PST22 | Multi-Talk |
| PST4 | EMS Dispatch | | PST23 | Law Talk |
| PST5 | *non* | | PST24 | Fire-Talk |
| PST6 | Multi-Tac | | PST25 | EMS-Talk |
| PST7 | Law Tac | | PST26 | Transportation |
| PST8 | Fire-Tac | | PST27 | *non* |
| PST9 | EMS-Tac | | PST28 | *non* |
| PST10 | *non* | | PST29 | Emergency Ops |
| PST11 | Interop | | PST30 | Military |
| PST12 | Hospital | | PST31 | Media |
| PST13 | Ham | | PST32 | Schools |
| PST14 | Public Works | | PST33 | Security |
| PST15 | Aircraft | | PST34 | Utilities |
| PST16 | Federal | | PST35 | *non* |
| PST17 | Business | | PST36 | *non* |
| PST18 | *non* | | PST37 | Corrections |
| PST19 | *non* | | | |

Unused slots: PST5, PST10, PST18, PST19, PST27, PST28, PST35, PST36 — eight of the 37.

### 12.2 Custom service types

| id | Service type name |
|---|---|
| ST1 | Custom 1 |
| ST2 | Custom 2 |
| ST3 | Custom 3 |
| ST4 | Custom 4 |
| ST5 | Custom 5 |
| ST6 | Custom 6 |
| ST7 | Custom 7 |
| ST8 | Custom 8 |
| ST9 | Racing Officials |
| ST10 | Racing Teams |

Note the naming mismatch: the `SVC` command calls these fields `CST1`–`CST10` while the service-type sheet calls them `ST1`–`ST10`. They are the same ten slots, in the same order.

The `SvcType` attribute in `GSI`/`GLT` carries the **name**, not the id (e.g. `SvcType="Fire Dispatch"`).

---

## 13. `KEY` codes

The spec's sheet has four columns: **Key code**, **BCD536HP**, **SDS100**, **Note**. There is no separate SDS200 column; the SDS100 column covers the whole SDS series, which is the source of several of the discrepancies in §14.

| Key code | BCD536HP | SDS100 / SDS200 (per spec) | Note | **HW on SDS200E** |
|---|---|---|---|---|
| `M` | MENU | Menu | Menu Key | **Squelch-knob push** (§14) |
| `F` | (Rotary knob) | Func | F Key | F / Function |
| `L` | AVOID | AVOID | Avoid Key | Avoid |
| `1` | 1 | 1 | 1 Key | 1 |
| `2` | 2 | 2 | 2 Key | 2 |
| `3` | 3 | 3 | 3 Key | 3 |
| `4` | 4 | 4 | 4 Key | 4 |
| `5` | 5 | 5 | 5 Key | 5 |
| `6` | 6 | 6 | 6 Key | 6 |
| `7` | 7 | 7 | 7 Key | 7 |
| `8` | 8 | 8 | 8 Key | 8 |
| `9` | 9 | 9 | 9 Key | 9 |
| `0` | 0 | 0 | 0 Key | 0 |
| `.` | . NO | . NO | Dot key | . / No |
| `E` | E yes | E yes | Enter Key | E / Yes |
| `>` | (Rotary knob) | (Rotary knob) | Rotary Right | Rotary right |
| `<` | (Rotary knob) | (Rotary knob) | Rotary Left | Rotary left |
| `^` | (Rotary knob) | (Rotary knob) | Rotary knob push | Rotary push |
| `V` | VOL | Backlight | Volume knob push | Volume-knob push / backlight |
| `Q` | SQ | *(none)* | Squelch knob push | **Not a key — treated as digit entry** (§14) |
| `Y` | REPLAY | REPLAY | Replay Key | Replay |
| `A` | SOFT 1 | SOFT 1 | BCD436HP: System | Soft key 1 |
| `B` | SOFT 2 | SOFT 2 | BCD436HP: Dept | Soft key 2 |
| `C` | SOFT 3 | SOFT 3 | BCD436HP: Channel | Soft key 3 |
| `Z` | ZIP | Zip | Zip Key | Zip |
| `T` | SREV | *(none)* | Service Type Key | **Not a key — treated as digit entry** (§14). Use `F` then `Z` |
| `R` | RANG | RANG | Range Key | **Not a key — treated as digit entry** (§14) |

Codes are **case-sensitive single characters**. The spec italicises *(none)* for `Q` and `T` on the SDS100 column, confirming those two have no physical counterpart; `R` is listed as present but is not (§14).

### 13.1 `KEY_MODE`

Not enumerated anywhere in either specification. The only literal shown is `P`, in `KEY,E,P` (the popup-button example, §7.6). The inherited Uniden set is:

| Mode | Intent |
|---|---|
| `P` | Press — press and release |
| `L` | Long press |
| `H` | Hold — key down, no release |
| `R` | Release — key up |

**HW: all four behave as a single short press on SDS200E 1.23.15.** Send `P` and do not attempt to express long-press behaviour through `KEY` (§14).

---

## 14. Verified on hardware (SDS200E fw 1.23.15)

The following come from live testing and **override the specification where they conflict**.

1. **`KAL` returns `ERR`.** The V2.00 claim that the radio sends no response to Keep Alive does not hold on this firmware; the radio answers `ERR`. Do not rely on `KAL` to keep a session alive — poll something cheap such as `GST` instead.

2. **Key codes `T`, `R` and `Q` are not SDS200 keys.** The radio acknowledges them with `KEY,OK`, but it treats them as digit entry and opens Quick Key Navigation. `KEY,OK` is therefore not confirmation that a key did anything.

3. **Service Types is `FUNC`+`Z`.** Send `KEY,F,P` then `KEY,Z,P` — not `KEY,T,P`.

4. **Squelch-knob push is `M`**, not `Q`.

5. **`KEY` modes `P`, `L`, `H` and `R` all behave like a single press.** Long-press functions cannot be sent remotely.

6. **`GWF` values are always two-digit hex** — zero-padded, so the payload length is fixed and parsing can be positional.

7. **`GSI` lags a hold change.** After a `HLD`, `GSI` reflects the new object only after roughly 0.2–0.4 s for a channel, and up to 2.4 s for a system or department. Poll until the expected `Index`/`Hold` appears rather than reading once.

8. **`HLD` toggles.** Issuing `HLD` for the object already held releases the hold. Read the current `Hold` attribute from `GSI` before deciding whether to send it.

9. **`NXT`/`PRV` need the live index.** Passing a stale index makes the radio jump somewhere else entirely. Refresh the index from `GSI` immediately before stepping.

10. **A key press while a `PopupScreen` is shown only dismisses the popup.** It does not reach the underlying screen. Detect `PopupScreen` in `GSI`, send one key to clear it, then re-send the intended key.

---

## 15. Specification gaps and defects

### 15.1 Gaps — information absent from both PDFs

| Area | What is missing |
|---|---|
| **Transport** | UDP port, framing, retry and timeout behaviour are not documented at all |
| **`CMD,<XML>,` and `<Footer>`** | Neither the marker datagram's role nor the fragmentation footer is described in prose; the marker is only visible in examples and the footer not at all |
| **`PSI` request syntax** | "User can change interval by parameter" — the parameter is never specified. Needs hardware testing |
| **`KEY_MODE`** | No enumeration; only `P` appears, in one example |
| **"Sub Audio" sheet** | `SAS`/`SAD` reference a sheet that is not in either PDF. Value set unknown beyond `All` and `None` |
| **"Font Data Specification"** | Referenced by V0.01's `STS` for non-ASCII character codes; not supplied |
| **`DispFormat` element** | Listed as an all-mode element; no attribute table anywhere |
| **`CC_Counters` element** | In the element list and the mode matrix; no attribute table |
| **`GST` `MUTE` field** | Named in the response template; not in the field list. Value encoding unknown |
| **`Property Backlight`** | In the worked example; not in the attribute table |
| **`DTM DayLightSaving`** | Encoding not stated |
| **`LCR RANGE`** | Unit not stated |
| **`SearchRange Step`** | Value set not given |
| **`ReplayMode Mode`** | Only `USER_REC` shown; the instant-replay value is not given |
| **`MSI Selected`** | Attribute listed with no value set |
| **`PWF`/`GWF`/`GW2` `ON/OFF`** | Literal values not given (`1`/`0` assumed) |
| **Error handling** | No generic error response is defined; most failures are silent |
| **Menu indices** | No stable id scheme for menu items — must be discovered live |

### 15.2 Defects in V2.00 worth coding around

| Location | Defect |
|---|---|
| `GW2` section | Request and response both printed as `GWF`; the token is `GW2` |
| `AVD` | Missing comma before `[STATUS]` in the syntax line (example confirms it is required) |
| `VOL` / `SQL` set | Acknowledgement shown as bare `VOL` / `SQL` rather than `,OK` |
| `SQL` | Parameter labelled "Volume Level" |
| `NXT` / `PRV` | Response numbered "(2)" with no "(1)" |
| `WaterfallBand Step` | `833kHz` should be `8.33kHz` |
| Current Activity example | Attribute `Tgidype` instead of `TgidType` |
| LCN Monitor | Attribute genuinely named `ReceiveStaus` (misspelling is in the firmware, not just the doc) |
| LCN Finder example | `<LcnFinder …` elements with no closing `/>` |
| `MSB` | `RETURN_PREVOUS_MODE` — misspelling must be reproduced exactly |
| V_Screen enumeration | `waterfall` missing although the mode matrix has a `waterfall` column |
| Mode matrix | No element row for the `rf_power_plot` column, although `RfPowerPlot` has an attribute table |
| Command list | "Program Mode Only" column present but empty for all 34 commands (vestigial from the BCD536HP spec) |
| `GCS` | Only response terminated with `\n`; only response using `key=value` |

---

## 16. What changed between V0.01 and V2.00

### 16.1 Commands added

`GST` (V1.02) · `PWF`, `GWF` (V1.01) · `GW2` (V2.00) · `KAL` (V2.00) · `POF`, `GCS` (V1.03) · `VOL`, `SQL` (present in V2.00, version of introduction not recorded).

No command was removed.

### 16.2 Contradictions and substantive changes

| Item | V0.01 | V2.00 |
|---|---|---|
| Models | `SDS100`, `SDS200` | `SDS100`, `SDS200`, **`SDS150GBT`** |
| `STS` guidance | "STS Command is compatible with old scanner. **PSI is better than STS.** See *Font Data Specification* for not ascii character code." | All three notes **deleted**; replaced by a full field list and a worked example |
| `JPM` modes | No `WF_MODE` | **`WF_MODE` added** (index reserved) |
| V_Screen enum | includes `band_scope` | `band_scope` **removed**; `waterfall` used in the matrix but **not added to the enum** |
| Mode-matrix element rows | `BandScope`, `BandScopeRange`, `RfPowerPlot` | `BandScope`/`BandScopeRange` **replaced** by `WaterfallBand`/`WaterfallSettings`; **`RfPowerPlot` row deleted** while its attribute table was kept |
| `BandScope` attributes | `Msg1`, `Msg2`, `Span` (0.2 MHz…200 MHz), `Hold`, `Att`; `BandScopeRange`: `Lower`, `Upper`, `Mod`, `Step` | Gone. `WaterfallBand`: `Lower`, **`Center`**, `Upper`, `Mod`, `Step`, **`Span` in kHz/MHz steps of 360 kHz…17.28 MHz**, **`Limit`**; `WaterfallSettings`: **`MF`**, **`Gain`** |
| `BAND_SCOPE` target keyword | Live | **Struck through** |
| Band Scope / Raw Data Output / RF Power Plot analyse modes | Normal | **Marked "Removed in SDS100"** (greyed and crossed out) |
| `AVD` syntax | `AVD,[tkw],[xxx1],[xxx2][STATUS1:Permanent Avoid` (badly mangled) | `AVD,[tkw],[xxx1],[xxx2][STATUS]` — still missing the comma, but legible |
| V0.01 completeness | **Ends at page 34 mid-sentence**: the Band Scope parameter list, Raw Data Output, System Status, RF Power Plot, the whole `APR` detail, the MSI attribute sheet, the Service Type sheet and the KEY code sheet are all **absent** | All present |

**Net effect for an implementer:** V0.01 is useful only for historical comparison. Build against V2.00, then apply §14.

### 16.3 Which document to trust per topic

| Topic | Authority |
|---|---|
| Everything, by default | V2.00 |
| `STS` vs `PSI` advice | V0.01's "PSI is better than STS" still holds in spirit; `GST` is better than both |
| Band scope | Neither — the feature is removed on the SDS series |
| Key codes, key modes, hold/next/previous timing, `KAL`, `GWF` encoding | **§14 (hardware), over both documents** |
| Transport | §1 (operational), as neither document covers it |
