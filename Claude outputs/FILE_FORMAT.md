# Uniden SDS100 / SDS200 microSD File Format — Writer's Reference

Derived **solely** from `SDSx00_File_Specification_V1_03.pdf` (Uniden/Fujitsu General "SDS100/200 FILE SPECIFICATION", Version 1.03, issued 2019‑01‑18, 50 pages). No external sources, no reverse engineering, no sample files were used.

**Purpose:** enough detail to *write* valid Favorites List files (`f_NNNNNN.hpd` + `f_list.cfg`) without the PDF.

**Conventions used in this document**

| Marker | Meaning |
|---|---|
| `⇥` | a single literal TAB character (0x09) |
| *(null)* | an empty field — nothing between the two surrounding TABs |
| **[SPEC]** | stated verbatim or unambiguously in the specification |
| **[INFERRED]** | not stated; deduced from structure or from other parts of the spec. Verify against a scanner‑written file. |
| **[UNDEFINED]** | the specification does not say, and no safe deduction is possible |

> **Critical caveat about the PDF itself.** Page 11 of the spec carries a three‑line legend that colour‑codes every field in every format table:
> * "…is parameter which **Favorites List** doesn't need. It is just null."
> * "…is parameter which **HPDB** doesn't need. It is just null."
> * "…is parameter which **user inputs** don't need. It is just null. In favorites list this on need."
>
> **The colour swatches and the corresponding cell shading are absent from the PDF.** Inspection of every fill colour in the file shows only revision highlights (amber/orange, marking items added in V1.02/V1.03) and no legend swatches at all. Therefore the per‑field "null in Favorites / null in HPDB / null for user input" classification **cannot be recovered from this document**. Section 4 below reconstructs it from the explicit prose notes only, and every remaining field is marked accordingly. This is the single largest gap in the spec.

---

## 1. microSD card layout

All scanner data lives under one top‑level directory. Despite the SDS100/SDS200 branding, the directory is named for the predecessor models.

```
\ (card root)
└── BCDx36HP\
    ├── scanner.inf              Scanner information (ESN, model name, firmware versions)
    ├── profile.cfg              Profile: owner info, weather, location, global settings, Limit/Custom Search
    ├── discvery.cfg             Conventional and Trunking Discovery configuration  [sic — see note]
    ├── app_data.cfg             Scanner resume state (settings the scanner reads to resume its last mode)
    ├── audio\
    │   ├── inner_rec\
    │   │   └── YYYY-MM-DD_hh-mm-ss.wav
    │   └── user_rec\
    │       └── XXXXXXXX\        sub-folder named with a hexadecimal FAT timestamp (see 1.4)
    │           └── YYYY-MM-DD_hh-mm-ss.wav
    ├── alert\
    │   └── YYYY-MM-DD_hh-mm-ss.wav
    ├── favorites_lists\
    │   ├── f_list.cfg           index of all Favorites Lists
    │   ├── f_000001.hpd         a Favorites List
    │   ├── f_000002.hpd
    │   └── f_000003.hpd         …
    ├── firmware\
    │   ├── BCD536HP_Vx_xx_xx.scn
    │   ├── BCD436HP_Vx_xx_xx.scn
    │   ├── ZipTable_Vx_xx_xx.dat
    │   └── CityTable_Vx_xx_xx.dat
    ├── HPDB\
    │   ├── hpdb.cfg             State list, County list, Locate‑Me (LM) info
    │   ├── s_000000.hpd         scan channel data, one file per state
    │   ├── s_000001.hpd
    │   │   …                    (observed indices in the spec: 000000–000113, 000181)
    │   ├── s_000000.avd         avoid list, one file per state
    │   ├── s_000001.avd
    │   │   …
    │   └── s_000181.avd
    ├── discovery\
    │   ├── Conventional\
    │   │   └── SessionNNNN_RunNNNN\
    │   │       ├── summary.log
    │   │       ├── detail.log
    │   │       └── <Frequency>_<Modulation>_<Audio>\     one folder per discovered channel
    │   │           └── ss-mm-hh_DD-MM-YYYY.wav
    │   └── Trunk\
    │       └── SessionNNNN_RunNNNN\
    │           ├── summary.log
    │           ├── detail.log
    │           └── <TGID>_<AudioMode>\
    │               └── ss-mm-hh_DD-MM-YYYY.wav
    └── activity_log\
        └── YYYY-MM-DD_hh-mm-ss_SystemName.log
```

### 1.1 Mandatory rule for PC applications

> **`app_data.cfg` — "Scanner settings data which scanner reads when scanner resumes last mode. PC application must delete this file, when it writes program data to scanner."** **[SPEC]**

A PC application that writes **any** program data (favourites, `f_list.cfg`, `profile.cfg`, HPDB files) **must delete `BCDx36HP\app_data.cfg`** as part of the same operation. If it is left in place the scanner resumes from a stale snapshot that does not match the data just written. The spec does not define a replacement file — the scanner regenerates it. Do not attempt to rewrite it.

### 1.2 Favourites naming rules

| Item | Rule | Source |
|---|---|---|
| Folder | `favorites_lists` (plural, underscore) | **[SPEC]** directory image |
| Index file | `f_list.cfg` | **[SPEC]** |
| Favourites file | `f_NNNNNN.hpd`, `NNNNNN` = `000001`–`999999`, zero‑filled, 6 digits | **[SPEC]** |
| Authority | "The name of file is managed by `favorite_list.config`" — i.e. by `f_list.cfg`. A `.hpd` file is only visible to the scanner if an `F-List` record in `f_list.cfg` names it. | **[SPEC]** |
| Numbering | Need not be contiguous; the directory image shows `f_000001`…`f_000003` but the index is authoritative. **[INFERRED]** — spec gives no explicit rule. |

> **Spec inconsistency:** the callout balloon in the directory image reads `favorites_xxxxxx.hpd (xxxxxx = 000001-999999)` while the tree itself shows `f_000001.hpd`. The tree (and the `f_list.cfg` `Filename` field, "include extension") is the form to follow: **`f_NNNNNN.hpd`**. Likewise the callout says `favorite_list.config` while the tree says `f_list.cfg`; the real filename is **`f_list.cfg`**. The section heading in the spec body is also `favorite_list.config`, and the scanner info section is headed `Scaner.info` while the file is `scanner.inf`, and the discovery config file is spelled `discvery.cfg` (missing "o") in the directory image. Use the names from the directory image: `scanner.inf`, `profile.cfg`, `discvery.cfg`, `app_data.cfg`, `f_list.cfg`.

### 1.3 HPDB naming rules

| Item | Rule |
|---|---|
| `s_NNNNNN.hpd` | Scan channel data for one state. `NNNNNN` is the StateId, zero‑filled to 6 digits. **[INFERRED]** from the state‑per‑file description and the observed index range. |
| `s_NNNNNN.avd` | Avoid list for the same state. Contains only `Avoid` records (see 4.26). |
| `hpdb.cfg` | `DateModified`, `StateInfo`, `CountyInfo`, `LM`, `LM_Frequency` records. |

HPDB files use the same record grammar as `.hpd` favourites but **do** carry `MyId` / `ParentId` values and **do** carry `AreaState` / `AreaCounty` records.

### 1.4 Audio / log naming rules

* Recording filenames: `YYYY-MM-DD_hh-mm-ss.wav` (hh = hour, mm = minute, ss = second, DD = day, MM = month, YYYY = year).
* Discovery recording filenames reverse the order: `ss-mm-hh_DD-MM-YYYY.wav`.
* Discovery per‑channel folder name: *Frequency* + `_` + *Modulation* + `_` + *CTCSS/DCS/NAC/ColorCode/RAN/Area*, e.g. `25000000_AM_None`, `225000000_FM_C100.0`, `225000000_NFM_D023`, `851012500_NFM_NFF`. The audio token uses the **"Discovery folder name String"** column of the audio table (Appendix C), which differs from the `.hpd` form (`C100.0` not `TONE=C100.0`; `NFF` = NAC FF).
* `user_rec` sub‑folder names are a hexadecimal FAT timestamp:

```
bit 31..25  year − 1980
bit 24..21  month
bit 20..16  day
bit 15..11  hour
bit 10..5   minute
bit  4..0   second ÷ 2
```
Example from the spec: `4D786CA` → `2018-11-24_13-37-24`.

* Activity log: `YYYY-MM-DD_hh-mm-ss_SystemName.log`. In the system‑name part, each of `\ / : * ? " < > |` is replaced by `_`, and the filename is truncated to 64 characters.

---

## 2. General encoding rules

These apply to **every** "HomePatrol data file": `.hpd`, `.avd`, `f_list.cfg`, `profile.cfg`, `scanner.inf`, `hpdb.cfg`, `discvery.cfg`, and the discovery logs.

### 2.1 Character encoding and line structure

| Rule | Detail | Source |
|---|---|---|
| Encoding | ASCII | **[SPEC]** |
| Line structure | "One line has one sentence." One **record** per line. | **[SPEC]** |
| Record structure | "One sentence has one command and several parameters." | **[SPEC]** |
| Separator | **TAB (0x09)** divides the command from the parameters, and the parameters from each other. | **[SPEC]** (the spec writes it as "Tab(/t)", a typo for `\t`) |
| Sub‑separators | Within a single parameter, the sub‑separators are `=`, `/` and `,`. | **[SPEC]** |
| Line terminator | **[UNDEFINED]**. The spec does not state CR, LF or CRLF. **[INFERRED]** — write `CRLF` (`\r\n`), the FAT/Windows convention used by Sentinel‑written cards, and accept either on read. |
| Trailing TAB | **[UNDEFINED]** whether a record may end with a TAB. **[INFERRED]** — do not emit one. |
| Blank lines / comments | Not defined; do not emit any. **[INFERRED]** |
| BOM | Not defined; do not emit one (ASCII only). **[INFERRED]** |

### 2.2 Mandatory header information

> "All HomePatrol data file has the following information: Target Model name / File type / File format version." **[SPEC]**

* **Target model name** → the `TargetModel` record.
* **File format version** → the `FormatVersion` record.
* **File type** → **[UNDEFINED]**. No `FileType` sentence is defined anywhere in the spec. The file's *type* is conveyed by its name/extension and directory (`f_*.hpd` = Favorites List, `s_*.hpd` = HPDB, `s_*.avd` = avoid list). Do not invent a record for it.

Both `TargetModel` and `FormatVersion` are marked **"cannot omit"**. **[INFERRED]** they are the first two lines of the file, in that order.

### 2.3 Null / omitted fields

| Situation | Representation | Source |
|---|---|---|
| `MyId` / `ParentId` in a **Favorites List** `.hpd` | "The hpd file which is Favorites List doesn't need MyId/ParentId. **just space**" | **[SPEC]** |
| `Reserve` fields | always null | **[SPEC]** by name; value not stated |
| Any field the legend marked as not‑needed | "It is just null." | **[SPEC]** (but which fields — see caveat above) |
| Literal interpretation of "null" / "just space" | **[INFERRED]** — an **empty field**, i.e. two consecutive TABs with nothing between them. The field position is still occupied, so the field *count* of a record never changes. A literal single space character (`0x20`) is the alternative reading of "just space"; the empty‑field reading is the one consistent with "It is just null" used for all other omitted fields, and is what this document's example uses. **This is a genuine ambiguity — verify against a scanner‑written file before shipping.** |
| Omitted whole records | Only three omissions are explicitly sanctioned: `AreaState` and `AreaCounty` ("Favorites List doesn't need … sentence"), `AvoidTgids` (omit entirely if no TGID is avoided in ID Search mode), and `BandPlan_P25` ("If Band Plan is undesignated, `[BandPlan_P25]` is omissible"). **[SPEC]** |
| Dropping trailing fields | **Never.** A record always carries its full field count. **[INFERRED]** — the spec defines fixed‑width format tables with no optional tail. |

### 2.4 Name tags

| Rule | Value | Source |
|---|---|---|
| Character set | ASCII `0x20`–`0x7E` only | **[SPEC]** |
| Maximum length | 64 characters | **[SPEC]** |
| Forbidden characters | TAB (0x09) is the field separator and is outside `0x20–0x7E`, so it cannot appear. No escaping mechanism is defined. **[SPEC]** by implication |
| `=`, `/`, `,` inside a name tag | The spec declares these as *sub*‑separators but never forbids them in a name tag and defines no escape. **[INFERRED]** — safe in a name tag (name tags have no sub‑structure), but avoid them if you can. |
| Trimming | Not defined. **[INFERRED]** — do not emit leading/trailing spaces. |
| `ConvDiscovery` / `TrunkDiscovery` `Name` | max **32** characters (not 64) | **[SPEC]** |
| `OwnerInfo` `Msg1st`–`Msg4th` | max **24** characters each | **[SPEC]** |

### 2.5 Frequency and step units

> **1 = 1 Hz.** **[SPEC]**

| Human value | File value |
|---|---|
| 851.0125 MHz | `851012500` |
| 6.25 kHz | `6250` |
| 446.00625 MHz | `446006250` |
| 453.7250 MHz | `453725000` |

Integer, no decimal point, no leading zeros required, no unit suffix. **[INFERRED]** that no zero‑padding is applied (spec examples show none).

Exceptions that are *not* in Hz:
* `ToneOut` `Tone A` / `Tone B`: `0, 2500–35000` where **1 = 0.1 Hz** — the spec explicitly flags this as "illegal frequency format".
* `CustomSearch` / `QuickSearch` `Frequency`: given as a hex range `0x00250000 – 0x13000000`, i.e. 25 MHz–1300 MHz expressed in Hz; **[INFERRED]** the field is still written in decimal Hz like every other frequency, the hex being only the spec's way of writing the bounds.
* `CustomBroadcastScreen` `Band x Lower/Upper`: stated as "0‑1300MHz". **[INFERRED]** still written in Hz.

### 2.6 Latitude / longitude

| Rule | Value |
|---|---|
| Format | DEGREE (decimal degrees) **[SPEC]** |
| Precision | up to 6 decimal places **[SPEC]** |
| Sign | **[INFERRED]** leading `-` for south/west; the spec does not state it, but decimal‑degree format and the `GpsOption` `DMS/Decimal` display choice imply signed decimals. |
| Padding / fixed decimals | **[UNDEFINED]**. **[INFERRED]** write 6 decimals always, e.g. `53.683000`, `-1.498000`. |

`Range` is in **miles**: `1 = 1 mile`, to 1 decimal place (`Site`, `C-Group`, `T-Group`). `CurrentLocation` `Range` is `0.0–50.0` in 0.5‑mile steps; `InterestingLocation` `Range` is `0–50` in 0.5‑mile steps.

### 2.7 Timestamp

> `"MM/DD/YYYY hh:mm:ss"` **[SPEC]**

US order, 24‑hour clock. **[INFERRED]** zero‑padded to two digits per component. Used by `DateModified` (in `hpdb.cfg`) and by `Detail_C_Log` / `Detail_T_Log` discovery logs. **Not** used in favourites files.

### 2.8 MyId / ParentId

Every HPDB resource carries its own id and its parent's id, written `xxId=nn`:

| Resource | Id keyword |
|---|---|
| Country | `CountryId` |
| State | `StateId` |
| County | `CountyId` |
| Agency | `AgencyId` |
| Trunk System | `TrunkId` |
| cats (category) | — (none) |
| subcat (department, conventional) | `CGroupId` |
| Conventional frequency | `CFreqId` |
| Trunk site | `SiteId` |
| Trunk frequency | `TFreqId` |
| Talk Group ID Group (department, trunked) | `TGroupId` |
| Talk Group ID channel | `Tid` |
| RRDB ids | not used by the scanner |

Example values: `CountyId=5`, `AgencyId=15`.

**In a Favorites List `.hpd`, every `MyId` and `ParentId` is null.** **[SPEC]** Consequence: the parent/child structure of a favourites file is carried **by record order alone** — each `C-Group` belongs to the most recent preceding `Conventional`, each `C-Freq` to the most recent preceding `C-Group`, and so on. **[INFERRED]** — the spec never states the ordering rule, but with all ids null there is no other possible linkage.

One `MyId`‑like field is *not* nulled: `T-Freq`'s first parameter is `Reserve(MyId)` and is **always the literal `TFreqId=0`**. **[SPEC]**

---

## 3. `f_list.cfg` — the Favorites List index

Location: `BCDx36HP\favorites_lists\f_list.cfg`.

**[INFERRED]** the file begins with `TargetModel` and `FormatVersion` like every other HomePatrol data file (the spec's "All HomePatrol data file has the following information" rule), followed by one `F-List` record per Favorites List.

### `F-List` record

**117 TAB‑separated tokens: the command plus 116 parameters.**

| # | Field | Allowed values | Notes |
|---|---|---|---|
| — | `F-List` | literal | command |
| 1 | UserName | Name Tag (ASCII 0x20–0x7E, ≤64) | the display name shown on the scanner |
| 2 | Filename | e.g. `f_000001.hpd` | **"include extension"** — filename only, no path |
| 3 | LocationControl | `On` (=Yes) / `Off` (=No) | |
| 4 | Monitor | `On` (=Monitor) / `Off` | |
| 5 | Quick key | `Off` / `0`–`99` | favourites‑list quick key |
| 6 | NumberTag | `Off` / `0`–`99` | favourites‑list number tag |
| 7–16 | Startup key 0 … Startup key 9 | `Off` / `On` | start‑up configuration keys, 10 fields in order 0→9 |
| 17–116 | S‑Qkey_00 … S‑Qkey_99 | `Off` / `On` | 100 fields in order 00→99 — the system quick‑key enable states for this list |

Total: 6 + 10 + 100 = **116 parameters**.

---

## 4. Record types in a `.hpd` file

### 4.0 Reading the tables

For each record the **exact token order** is given as a numbered list. Token 0 is always the command keyword; parameters follow, TAB‑separated.

Three status columns are used:

* **Req** — required in a Favorites List file.
* **Fav‑null** — must be null in a Favorites List file.
* **HPDB‑null** — must be null in an HPDB file.

Where the spec's colour legend was the only source (see the caveat in the header), the cell reads **[UNDEFINED]**. Only the prose notes are authoritative.

Records that may appear in a `.hpd` file, in the order the spec defines them:
`TargetModel`, `FormatVersion`, `Conventional`, `Trunk`, `AreaState`, `AreaCounty`, `FleetMap`, `UnitIds`, `AvoidTgids`, `Site`, `Rectangle`, `BandPlan_Mot`, `BandPlan_P25`, `DQKs_Status`, `C-Group`, `T-Group`, `C-Freq`, `TGID`, `T-Freq`.

**[INFERRED] hierarchical ordering for a Favorites List:**

```
TargetModel
FormatVersion
  Conventional                     ← a conventional system
    [FleetMap]                     (Motorola Type I only — see 4.7)
    [DQKs_Status]
    C-Group                        ← a department
      [Rectangle]                  (only if Location Type = Rectangles)
      C-Freq                       ← a channel
      C-Freq …
    C-Group …
  Trunk                            ← a trunked system
    [UnitIds] …
    [AvoidTgids] …
    [DQKs_Status]
    Site
      [Rectangle]
      [BandPlan_Mot]               (only if Mot Band Type = Custom)
      [BandPlan_P25]               (optional)
      T-Freq …                     ← control/voice frequencies of that site
    Site …
    T-Group                        ← a department
      [Rectangle]
      TGID …                       ← a talkgroup
    T-Group …
```

---

### 4.1 `TargetModel`

| # | Field | Values |
|---|---|---|
| 0 | `TargetModel` | literal |
| 1 | ModelName | `BCDx36HP` |

* **Cannot be omitted.** **[SPEC]**
* Note in spec: "BCD436HP/BCD536HP set `BCDx36HP`". **[SPEC]**
* **Caution:** this is the only value the spec lists, and the SDS card directory is also named `BCDx36HP`, so `BCDx36HP` is the value to write for the SDS100/SDS200. The spec does **not** define an SDS‑specific target‑model string. **[INFERRED]**, low risk.

```
TargetModel⇥BCDx36HP
```

### 4.2 `FormatVersion`

| # | Field | Values |
|---|---|---|
| 0 | `FormatVersion` | literal |
| 1 | Version | `x.xx` |

* **Cannot be omitted.** **[SPEC]**
* **[UNDEFINED]** which version string the SDS100/200 expects. The *document* is V1.03; the *file format* version is a separate number and the spec never gives its value. **[INFERRED]** — read the value from a `.hpd` the scanner itself wrote and echo it back; `1.00` is the conservative default if none is available.

```
FormatVersion⇥1.00
```

### 4.3 `Conventional` — conventional system

**15 tokens (14 parameters).**

| # | Field | Allowed values | Req | Fav‑null | HPDB‑null |
|---|---|---|---|---|---|
| 0 | `Conventional` | literal | yes | — | — |
| 1 | MyId | `CountyId=nn` or `AgencyId=nn` | — | **yes** | no |
| 2 | ParentId | `StateId=nn` | — | **yes** | no |
| 3 | Name Tag | ASCII 0x20–0x7E, ≤64 | yes | no | no |
| 4 | Avoid | `On` / `Off` | yes | no | always `Off` in HPDB (managed by `.avd`) |
| 5 | Reserve | — | — | null | null |
| 6 | System Type | `Conventional` | yes | no | no |
| 7 | Quick Key | `Off` / `0`–`99` | yes | no | [UNDEFINED] |
| 8 | Number tag | `Off` / `0`–`99` | yes | no | [UNDEFINED] |
| 9 | System Hold Time | `0`–`255` (seconds) | yes | no | [UNDEFINED] |
| 10 | Analog AGC | `Off` / `On` | yes | no | [UNDEFINED] |
| 11 | Digital AGC | `Off` / `On` | yes | no | [UNDEFINED] |
| 12 | Digital Waiting Time | `0`–`1000` (1 = 1 ms) | yes | no | [UNDEFINED] |
| 13 | Digital Threshold Mode | `Auto` / `Manual` / `Default` | yes | no | [UNDEFINED] |
| 14 | Digital Threshold Level | `5`–`13` | yes | no | [UNDEFINED] |

Note: "In HPDB, avoid parameter is always `Off`. It is managed by `*.avoid` file." (the spec says `*.avoid`; the actual extension in the directory image is `.avd`).

`System Type` for a `Conventional` record: the spec's parameter block leaves the value list blank here and gives the full list under `Trunk`. **[INFERRED]** write `Conventional`.

### 4.4 `Trunk` — trunked system

**21 tokens (20 parameters).**

| # | Field | Allowed values | Req | Fav‑null | HPDB‑null |
|---|---|---|---|---|---|
| 0 | `Trunk` | literal | yes | — | — |
| 1 | MyId | `TrunkId=nn` | — | **yes** | no |
| 2 | ParentId | `StateId=nn` | — | **yes** | no |
| 3 | Name Tag | ASCII string, ≤64 | yes | no | no |
| 4 | Avoid | `On` / `Off` | yes | no | always `Off` in HPDB |
| 5 | Reserve | — | — | null | null |
| 6 | System Type | see list below | yes | no | no |
| 7 | ID Search | `Off` / `On` | yes | no | [UNDEFINED] |
| 8 | Alert Tone | `Off` / `0` / `1` … `9` — Emergency Alert Tone | yes | no | [UNDEFINED] |
| 9 | Alert Volume | `Auto` / `1` … `15` | yes | no | [UNDEFINED] |
| 10 | Status Bit | `Yes` / `Ignore` | yes | no | [UNDEFINED] |
| 11 | Reserve(NAC) | `Srch` / `0`–`FFF` (hex, no `0x`) | yes | no | [UNDEFINED] |
| 12 | Quick Key | `Off` / `0`–`99` | yes | no | [UNDEFINED] |
| 13 | Number tag | `Off` / `0`–`99` | yes | no | [UNDEFINED] |
| 14 | Site Hold Time | `0`–`255` | yes | no | [UNDEFINED] |
| 15 | Analog AGC | `Off` / `On` | yes | no | [UNDEFINED] |
| 16 | Digital AGC | `Off` / `On` | yes | no | [UNDEFINED] |
| 17 | End Code | `Analog` / `Analog+Digital` / `Ignore` | yes | no | [UNDEFINED] |
| 18 | Priority ID Scan | `Off` / `On` | yes | no | [UNDEFINED] |
| 19 | Alert Color | `Off`/`Blue`/`Red`/`Magenta`/`Green`/`Cyan`/`Yellow`/`White` | yes | no | [UNDEFINED] |
| 20 | Alert Pattern | `On` / `Slow Blink` / `Fast Blink` | yes | no | [UNDEFINED] |
| 21 | TGID Format | `NEXEDGE` / `IDAS` | yes | no | [UNDEFINED] |

Format table rows in the spec (6 + 5 + 5 + 5 = 21 parameters, 22 tokens):

```
row 1: Trunk | MyId | ParentId | Name Tag | Avoid | Reserve | System Type        →
row 2:   →    ID Search | Alert Tone | Alert Volume | Status Bit | Reserve(NAC)  →
row 3:   →    Quick Key | Number tag | Site Hold Time | Analog AGC | Digital AGC →
row 4:   →    End Code | Priority ID Scan | Alert Color | Alert Pattern | TGID Format
```

**`System Type` enumeration** (used by `Trunk`, `ScanTrunkSystem`, `TrunkDiscovery`, `RunInfo_T`):

```
Conventional
Motorola
Edacs
Scat                (reserve parameter — do not use)
Ltr
P25Standard         (= P25 Phase I & II)
P25OneFrequency
P25X2_TDMA
MotoTrbo            (spelled "MotoTRBO" in the Discovery sections — see Gotchas)
DmrOneFrequency
Nxdn
NxdnOneFrequency
```

`TGID Format` (`NEXEDGE` / `IDAS`) was **added in spec V1.03** and applies to NXDN systems.

### 4.5 `AreaState`

**3 tokens.** *HPDB only.*

| # | Field | Values |
|---|---|---|
| 0 | `AreaState` | literal |
| 1 | MyId | `CountyId=nn` / `AgencyId=nn` / `TrunkId=nn` |
| 2 | StateId | numeric state id |

> "Favorites List doesn't need AreaState sentence." **[SPEC]** — **omit the whole record** from a favourites file.

### 4.6 `AreaCounty`

**3 tokens.** *HPDB only.*

| # | Field | Values |
|---|---|---|
| 0 | `AreaCounty` | literal |
| 1 | MyId | `CountyId=nn` / `AgencyId=nn` / `TrunkId=nn` |
| 2 | CountyId | numeric county id |

> "Favorites List doesn't need AreaCounty sentence." **[SPEC]** — **omit the whole record** from a favourites file.

### 4.7 `FleetMap`

**10 tokens (9 parameters).** Motorola Type I / Type IIi fleet map.

| # | Field | Allowed values |
|---|---|---|
| 0 | `FleetMap` | literal |
| 1 | MyId | id of the owning system. **[INFERRED]** `TrunkId=nn`; the spec leaves the value blank. Null in a Favorites List. |
| 2 | B0 | `0` / `1`–`9` / `A` / `B` / `C` / `D` / `E` |
| 3 | B1 | same |
| 4 | B2 | same |
| 5 | B3 | same |
| 6 | B4 | same |
| 7 | B5 | same |
| 8 | B6 | same |
| 9 | B7 | same |

> Note: "If block size is all 0 (TYPE 2)" — i.e. `B0`…`B7` all `0` means the system is Motorola Type II, not Type I. **[SPEC]**

### 4.8 `UnitIds`

**9 tokens (8 parameters).** Named unit (radio) IDs with per‑unit alerts.

| # | Field | Allowed values |
|---|---|---|
| 0 | `UnitIds` | literal |
| 1 | Reserve | null |
| 2 | Reserve | null |
| 3 | Name Tag | ASCII string, ≤64 |
| 4 | Unit ID | `1`–`16777215` |
| 5 | Alert Tone | `Off` / `1`–`9` |
| 6 | Alert Volue *(sic — Alert Volume)* | `Auto` / `1`–`15` |
| 7 | Alert Color | Alert Light Color (see 4.30) |
| 8 | Alert Pattern | Alert Light Pattern (see 4.30) |

Note: `UnitIds` carries **no** MyId/ParentId at all — the first two fields are `Reserve`. Its owning system is therefore positional. **[INFERRED]** place it directly after its `Trunk` record.

### 4.9 `AvoidTgids`

**18 tokens (17 parameters).**

| # | Field | Allowed values |
|---|---|---|
| 0 | `AvoidTgids` | literal |
| 1 | MyId | owning system id; null in a Favorites List |
| 2–17 | TGID 1 … TGID 16 | TGID character strings (section 5) |

> * "If TGID is avoided on ID Search mode doesn't exist, AvoidTgids sentence isn't outputted `*.hpd` file." — **omit the record entirely** when nothing is avoided. **[SPEC]**
> * "If over 16 TGIDs are avoided on ID Search mode, 2 or more AvoidTgids sentence are outputted to `*.hpd` file." — **emit multiple records**, 16 TGIDs each. **[SPEC]**
> * **[UNDEFINED]** how a partially filled record pads its unused slots. **[INFERRED]** leave them null (empty fields), keeping all 17 parameters present.

### 4.10 `Site` — trunked site

**19 tokens (18 parameters).**

```
row 1: Site | MyId | ParentId | Name Tag | Avoid | Latitude | Longitude          →
row 2:   →   Range | Modulation | Mot Band Type | Edacs Band Type                →
row 3:   →   Location Type | Attenuator                                          →
row 4:   →   Digital Waiting Time | Digital Threshold Mode | Digital Threshold Level | Quick Key →
row 5:   →   NAC | Filter
```

| # | Field | Allowed values | Notes |
|---|---|---|---|
| 0 | `Site` | literal | |
| 1 | MyId | `SiteId=nn` | null in Favorites List |
| 2 | ParentId | `TrunkId=nn` | null in Favorites List |
| 3 | Name Tag | ASCII, ≤64 | |
| 4 | Avoid | `On` / `Off` | always `Off` in HPDB |
| 5 | Latitude | decimal degrees, ≤6 dp | |
| 6 | Longitude | decimal degrees, ≤6 dp | |
| 7 | Range | miles, 1 dp (`1` = 1 mile) | |
| 8 | Modulation | `AUTO` / `NFM` / `FM` | no AM/WFM/FMB for a site |
| 9 | Mot Band Type | `Standard` / `Sprinter` / `Custom` | **set `Standard` if System Type ≠ Motorola** |
| 10 | Edacs Band Type | `Wide` / `Narrow` | **set `Wide` if System Type ≠ Edacs** |
| 11 | Location Type | `Circle` / `Rectangles` | `Rectangles` requires a following `Rectangle` record |
| 12 | Attenuator | `On` / `Off` | |
| 13 | Digital Waiting Time | `0`–`1000` (ms) | |
| 14 | Digital Threshold Mode | `Auto` / `Manual` / `Default` | |
| 15 | Digital Threshold Level | `5`–`13` | |
| 16 | Quick Key | `Off` / `0`–`99` | |
| 17 | NAC | `Srch` / `0`–`FFF` | hex, no `0x` |
| 18 | Filter | `Global` / `Normal` / `Invert` / `Auto` | **added in spec V1.03** |

### 4.11 `Rectangle`

**6 tokens (5 parameters).**

| # | Field | Allowed values |
|---|---|---|
| 0 | `Rectangle` | literal |
| 1 | MyId | `SiteId=nn`, `TGroupId=nn` or `CGroupId=nn`; null in a Favorites List |
| 2 | Latitude 1 | decimal degrees, ≤6 dp |
| 3 | Longitude 1 | decimal degrees, ≤6 dp |
| 4 | Latitude 2 | decimal degrees, ≤6 dp |
| 5 | Longitude 2 | decimal degrees, ≤6 dp |

> "Rectangle sentence is addition information for Department or Site." **[SPEC]** — emit it only when the owning `Site` / `C-Group` / `T-Group` has `Location Type = Rectangles`. **[INFERRED]** it immediately follows its owner.

### 4.12 `BandPlan_Mot` — Motorola custom band plan

**26 tokens (25 parameters).** Six band‑plan entries of four fields each.

| # | Field | Allowed values |
|---|---|---|
| 0 | `BandPlan_Mot` | literal |
| 1 | MyId | `SiteId` of the owning `Site`; null in a Favorites List |
| 2 | Lower 0 | frequency, Hz |
| 3 | Upper 0 | frequency, Hz |
| 4 | Spacing 0 | from the spacing set below |
| 5 | Offset 0 | `-1023` … `1023` |
| 6–9 | Lower 1 / Upper 1 / Spacing 1 / Offset 1 | |
| 10–13 | …2 | |
| 14–17 | …3 | |
| 18–21 | …4 | |
| 22–25 | Lower 5 / Upper 5 / Spacing 5 / Offset 5 | |

**Spacing enumeration** (Hz) — "5k and 6.25K multiples. Up to 100K":

```
5000  6250  10000 12500 15000 18750 20000 25000
30000 31250 35000 37500 40000 43750 45000 50000
55000 56250 60000 62500 65000 68750 70000 75000
80000 81250 85000 87500 90000 93750 95000 100000
```

> "[BandPlan_Mot] is valid only Mot Band Type is CUSTOM." **[SPEC]** — omit otherwise. **[INFERRED]** that unused band slots are written as null fields.

### 4.13 `BandPlan_P25` — P25 band plan

**34 tokens (33 parameters).** Sixteen band plans (0…F) of two fields each.

| # | Field | Allowed values |
|---|---|---|
| 0 | `BandPlan_P25` | literal |
| 1 | MyId | `SiteId`; null in a Favorites List |
| 2 | Base 0 | frequency, Hz (refer to the scanner's Band Coverage) |
| 3 | Spacing 0 | frequency, Hz; `125` ≤ Spacing ≤ `128000` in 125 Hz steps (0.1250 kHz – 128.0000 kHz) |
| 4–5 | Base 1 / Spacing 1 | |
| … | … | |
| 32–33 | Base F / Spacing F | |

> "If Band Plan is undesignated, [BandPlan_P25] is omissible." **[SPEC]**

"Refer to Band Coverage" — **[UNDEFINED]**: the Band Coverage table is not in this document.

### 4.14 `DQKs_Status` — department quick‑key status

**102 tokens (101 parameters).** Description in the spec: "Favorites List Quick Status".

| # | Field | Allowed values |
|---|---|---|
| 0 | `DQKs_Status` | literal |
| 1 | MyId | owning system id; null in a Favorites List |
| 2–101 | D‑Qkey_00 … D‑Qkey_99 | `Off` / `On` |

**[INFERRED]** one record per system, recording which of that system's department quick keys are enabled.

### 4.15 `C-Group` — conventional department

**11 tokens (10 parameters).**

| # | Field | Allowed values | Notes |
|---|---|---|---|
| 0 | `C-Group` | literal | |
| 1 | MyId | `CGroupId=nn` | null in a Favorites List |
| 2 | ParentId | `CountyId=nn` / `AgencyId=nn` | null in a Favorites List |
| 3 | Name Tag | ASCII, ≤64 | |
| 4 | Avoid | `On` / `Off` | always `Off` in HPDB |
| 5 | Latitude | decimal degrees, ≤6 dp | |
| 6 | Longitude | decimal degrees, ≤6 dp | |
| 7 | Range | miles, 1 dp | |
| 8 | Location Type | `Circle` / `Rectangles` | |
| 9 | Quick Key | `Off` / `0`–`99` | |
| 10 | Filter | `Global` / `Normal` / `Invert` / `Auto` | **C‑Group only**; added in V1.03 |

### 4.16 `T-Group` — trunked department

**10 tokens (9 parameters).** Identical to `C-Group` but **without** the trailing `Filter` field.

| # | Field | Allowed values |
|---|---|---|
| 0 | `T-Group` | literal |
| 1 | MyId | `TGroupId=nn`; null in a Favorites List |
| 2 | ParentId | `TrunkId=nn`; null in a Favorites List |
| 3 | Name Tag | ASCII, ≤64 |
| 4 | Avoid | `On` / `Off` |
| 5 | Latitude | decimal degrees |
| 6 | Longitude | decimal degrees |
| 7 | Range | miles, 1 dp |
| 8 | Location Type | `Circle` / `Rectangles` |
| 9 | Quick Key | `Off` / `0`–`99` |

> "Filter is C‑Group only." **[SPEC]**

### 4.17 `C-Freq` — conventional channel

**18 tokens (17 parameters).**

```
row 1: C-Freq | MyId | ParentId | Name Tag | Avoid | Frequency | Modulation   →
row 2:   →     Audio Option | FuncTagId | Attenuator | Delay | Volume Offset  →
row 3:   →     Alert Tone | Alert Volume | Alert Color | Alert Pattern        →
row 4:   →     Number tag | Priority Channel
```

| # | Field | Allowed values | Notes |
|---|---|---|---|
| 0 | `C-Freq` | literal | |
| 1 | MyId | `CFreqId=nn` | null in a Favorites List |
| 2 | ParentId | `CGroupId=nn` | null in a Favorites List |
| 3 | Name Tag | ASCII, ≤64 | |
| 4 | Avoid | `On` / `Off` | always `Off` in HPDB |
| 5 | Frequency | Hz (1 = 1 Hz) | |
| 6 | Modulation | `AUTO` / `AM` / `NFM` / `FM` | **no WFM / FMB on a channel** |
| 7 | Audio Option | null (= All), or `Tone=xxxx`, `NAC=xxx`, `ColorCode=xx`, `RAN=xx`, `Area=x` | see Appendix C for exact token strings |
| 8 | FuncTagId | `1`–`37`, `208`–`218` | service type, Appendix B |
| 9 | Attenuator | `On` / `Off` | |
| 10 | Delay | `30`,`10`,`5`,`4`,`3`,`2`,`1`,`0`,`-5`,`-10` (seconds) | |
| 11 | Volume Offset | `-3`/`-2`/`-1`/`0`/`1`/`2`/`3` | |
| 12 | Alert Tone | `Off` / `1`–`9` | |
| 13 | Alert Volume | `Auto` / `1`–`15` | |
| 14 | Alert Color | Alert Light Color | |
| 15 | Alert Pattern | Alert Light Pattern | |
| 16 | Number tag | `Off` / `0`–`999` | note: 0–**999**, unlike the 0–99 of system/quick keys |
| 17 | Priority Channel | `Off` / `On` | |

The parameter list writes `Audio Option` as `Tone=xxxx` while the audio table (Appendix C) gives the actual file token as `TONE=C100.0` / `TONE=D023` / `TONE=Srch`. **Use the audio table's `File` column verbatim.** The spec is internally inconsistent on the case of `Tone`/`TONE`; the audio table (the more specific source) uses uppercase `TONE=`. **[INFERRED]** — use `TONE=`.

### 4.18 `TGID` — talkgroup channel

**17 tokens (16 parameters).**

```
row 1: TGID | MyId | ParentId | Name Tag | Avoid | TGID | Audio Type     →
row 2:   →    FuncTagId | Delay | Volume Offset                          →
row 3:   →    Alert Tone | Alert Volume | Alert Color | Alert Pattern     →
row 4:   →    Number tag | Priority Channel | TDMA Slot
```

| # | Field | Allowed values | Notes |
|---|---|---|---|
| 0 | `TGID` | literal | |
| 1 | MyId | `Tid=nn` | null in a Favorites List |
| 2 | ParentId | `TGroupId=nn` | null in a Favorites List |
| 3 | Name Tag | ASCII, ≤64 | |
| 4 | Avoid | `On` / `Off` | always `Off` in HPDB |
| 5 | TGID | TGID character string — **format depends on the parent `Trunk` System Type**, section 5 | |
| 6 | Audio Type | `ALL` / `ANALOG` / `DIGITAL` | |
| 7 | FuncTagId | `1`–`37`, `208`–`218` | |
| 8 | Delay | `30`,`10`,`5`,`4`,`3`,`2`,`1`,`0`,`-5`,`-10` | |
| 9 | Volume Offset | `-3`…`3` | |
| 10 | Alert Tone | `Off` / `1`–`9` | |
| 11 | Alert Volume | `Auto` / `1`–`15` | |
| 12 | Alert Color | Alert Light Color | |
| 13 | Alert Pattern | Alert Light Pattern | |
| 14 | Number tag | `Off` / `0`–`999` | |
| 15 | Priority Channel | `Off` / `On` | |
| 16 | TDMA Slot | `1` / `2` / `Any` | |

**`TGID` has no `Attenuator` field** — unlike `C-Freq`. Do not add one.

### 4.19 `T-Freq` — trunked site frequency

**8 tokens (7 parameters).**

| # | Field | Allowed values | Notes |
|---|---|---|---|
| 0 | `T-Freq` | literal | |
| 1 | Reserve(MyId) | **always the literal `TFreqId=0`** | **[SPEC]** — not nulled, even in a Favorites List |
| 2 | ParentId | `SiteId=nn` | null in a Favorites List **[INFERRED]** |
| 3 | Reserve | null | |
| 4 | Reserve(Avoid) | **always `Off`** | **[SPEC]** |
| 5 | Frequency | Hz (1 = 1 Hz) | |
| 6 | LCN | `0`, `1`–`30` / `1`–`20` / `1`–`4094` / `1`–`1023` — range depends on system type | **[UNDEFINED]** which range maps to which system type |
| 7 | Color Code / RAN / Area | `0`–`15` (DMR colour code), `RAN=0`–`RAN=63`, `Area=0`/`Area=1`, or `Srch` | |

### 4.20 `Scanner` — in `scanner.inf`

**10 tokens (9 parameters).**

| # | Field | Value |
|---|---|---|
| 0 | `Scanner` | literal |
| 1 | Model | model name (`UB375Z` → `BCD536HP`) |
| 2 | ESN | electronic serial number |
| 3 | F_Ver | firmware version |
| 4 | R_Ver | registry version |
| 5 | Reserve | null |
| 6 | Zip_Ver | ZIP table version |
| 7 | City_Ver | city table version |
| 8 | WiFi_Ver | Wi‑Fi module version |
| 9 | SCPU_Ver | sub‑CPU version (**added in spec V1.03**) |

Read‑only from a PC application's point of view. **[INFERRED]**

### 4.21 `DateModified` — in `hpdb.cfg`

| # | Field | Value |
|---|---|---|
| 0 | `DateModified` | literal |
| 1 | Time stamp | `MM/DD/YYYY hh:mm:ss` |

HPDB generation time stamp. Not used in favourites.

### 4.22 `StateInfo` — in `hpdb.cfg`

| # | Field | Value |
|---|---|---|
| 0 | `StateInfo` | literal |
| 1 | StateId | `StateId=nn` |
| 2 | CountryId | `CountryId=nn` |
| 3 | NameTag | state name |
| 4 | ShortName | exactly 2 characters, e.g. `AL`, `TX` |

### 4.23 `CountyInfo` — in `hpdb.cfg`

**Note the command keyword is `County`, not `CountyInfo`** (the section is headed `CountyInfo` but the format line reads `County`). **[SPEC]** — a spec inconsistency; HPDB is read‑only for most writers.

| # | Field | Value |
|---|---|---|
| 0 | `County` | literal |
| 1 | CountyId | `CountyId=nn` |
| 2 | StateId | `StateId=nn` |
| 3 | NameTag | county name |

### 4.24 `LM` — Locate‑Me, in `hpdb.cfg`

**9 tokens (8 parameters).**

| # | Field | Value |
|---|---|---|
| 0 | `LM` | literal |
| 1 | StateId | `StateId=nn` |
| 2 | CountyId | `CountyId=nn` |
| 3 | TruniId *(sic — TrunkId)* | `TruniId=nn` as spelled in the spec |
| 4 | SiteId | `SiteId=nn` |
| 5 | LM_SystemID | character data |
| 6 | LM_SiteID | character data |
| 7 | latitude | decimal degrees |
| 8 | longitude | decimal degrees |

Sort order: primary key `LM_SystemID`, secondary key `LM_SiteID`.

### 4.25 `LM_Frequency` — in `hpdb.cfg`

| # | Field | Value |
|---|---|---|
| 0 | `LM_Frequency` | literal |
| 1 | Frequency | Hz |
| 2 | Reserve (LmSystemType) | `Motorola` / `P25Standard` / `Both` |
| 3 | LmTypeArray | three binary digits `xxx`: bit 1 = Motorola, bit 2 = P25 Standard, bit 3 = P25 X2‑TDMA; `1` = detected, `0` = not detected |

### 4.26 `Avoid` — in `s_NNNNNN.avd`

Four variants, distinguished by field count:

| Variant | Format | Meaning |
|---|---|---|
| 1 | `Avoid⇥StateId⇥SystemId⇥DeptId⇥ChannelId` | one channel (conventional frequency or TGID) |
| 2 | `Avoid⇥StateId⇥SystemId⇥DeptId` | a department |
| 3 | `Avoid⇥StateId⇥SystemId⇥SiteId` | a site |
| 4 | `Avoid⇥StateId⇥SystemId` | a system |

> **Variants 2 and 3 are indistinguishable by shape** — both have three parameters. **[UNDEFINED]** how a reader tells a department avoid from a site avoid. **[INFERRED]** the id prefix (`CGroupId=`/`TGroupId=` vs `SiteId=`) disambiguates, but the spec shows no prefixes here.

### 4.27 Records in `profile.cfg`

`profile.cfg` holds global/profile settings and Limit/Custom Search. Not needed to write favourites, documented here for completeness.

**`ProductName`** — `ProductName⇥Model`. `UB383Z` → `"SDS100"`.

**`GlobalSetting`** — 20 tokens (19 parameters):

| # | Field | Allowed values |
|---|---|---|
| 0 | `GlobalSetting` | literal |
| 1 | ScanHpdb | `Off` / `On` |
| 2 | G‑Attenuator | `On` / `Off` (global attenuator) |
| 3 | Reserve | null |
| 4 | Priority Scan mode | `Off` / `DND` / `Priority` |
| 5 | Close Call mode | `Off` / `DND` / `Priority` |
| 6 | WX Priority_mode | `Off` / `Priority` |
| 7 | Priority Interval | `1`–`10` (sec) |
| 8 | Ppriority Max Channels *(sic)* | `1`–`100` |
| 9 | Srch Key 1 | `Off`, `Custom0`…`Custom9`, `ToneOut`, `CloseCall` |
| 10 | Srch Key 2 | same |
| 11 | Srch Key 3 | same |
| 12 | Key Lock | `Off` / `On` |
| 13 | Key Beep | `Off` / `1`–`15` / `Auto` |
| 14 | Volume | `0`–`29` (mobile = SDS200) / `0`–`15` (handheld = SDS100) |
| 15 | Squelch | `0`–`19` (mobile) / `0`–`15` (handheld) |
| 16 | SearchWithScanList | `Off` / `On` |
| 17 | SearchWithScan System Avoid | `Off` / `On` |
| 18 | Site NAC Operation | `Ignore` / `Compare` (**added V1.02**) |
| 19 | Global Auto Filter | `Normal` / `Invert` / `Auto` (**added V1.03**) |

**`SearchCommon`** — 11 tokens (10 parameters): `SearchCommon`, Reserve, Repeater Find (`Off`/`On`), Attenuator (`Off`/`On`), Delay (`30,10,5,4,3,2,1,0,-5,-10`), Modulation (`Auto`/`AM`/`NFM`/`FM`/`WFM`/`FMB`), agc_analog (`Off`/`On`), agc_digital (`Off`/`On`), Digital_waiting_time (`0`–`1000`), Digital_threshold_Mode (`Auto`/`Manual`/`Default`), Digital_threshold_Level (`5`–`13`), Filter (`Global`/`Normal`/`Invert`/`Auto`, **added V1.03**).
*Field count note:* the format table lists Reserve + 10 named fields = **11 parameters**, 12 tokens.

**`PresetBroadcastScreen`** — `PresetBroadcastScreen⇥Pager⇥FM⇥UHF TV⇥VHF TV⇥NOAA WX`, each `Off`/`On`. 6 tokens.

**`CustomBroadcastScreen`** — 31 tokens: command + 10 groups of (Band x Enable `Off`/`On`, Band x Lower, Band x Upper) for x = 0…9. Lower/Upper cover 0–1300 MHz.

**`CurrentLocation`** — `CurrentLocation⇥latitude⇥longitude⇥Range`. Range `0.0`–`50.0` in 0.5‑mile steps.

**`GpsOption`** — `GpsOption⇥GpsFormat⇥BaudRate`. GpsFormat `DMS`/`Decimal`; BaudRate `Off`/`4800`/`9600`/`19200`/`38400`/`57600`/`115200`.

**`InterestingLocation`** — `InterestingLocation⇥Name⇥latitude⇥longitude⇥Range`. Range `0`–`50`, 0.5‑mile steps.

**`ServiceType`** — `ServiceType` + 37 parameters `Id01_state`…`Id37_state`, each `Off`/`On`. ID = FuncTagId (Appendix B).

**`CustomServiceType`** — `CustomServiceType` + 10 parameters `Id01_state`…`Id10_state`, each `Off`/`On`. Id01–Id08 = Custom 1–8, Id09 = Racing Officials, Id10 = Racing Teams (FuncTagId 208–217).

**`Weather`** — `Weather⇥Reserve⇥Delay⇥Attenuator⇥agc_analog`. Delay `30,10,5,4,3,2,1,0,-5,-10`; Attenuator and agc_analog `Off`/`On`.

**`WxSameList`** — `WxSameList⇥SameId⇥Name⇥FIPS 0 … FIPS 7`. `SameId=1`…`SameId=5`; each FIPS is 6 digits `"000000"`–`"999999"`, or `"------"` for none.

**`DisplayOption`** — 14 tokens: `DisplayOption`, Reserve ×5, MotTgidFormat (`DEC`/`HEX`), Reserve ×2, Reserve (Upside_down), Reserve, Simple mode (`Off`/`On`, 3‑line mode), EdacTgidFormat (`AFS`/`DEC`), Color Mode (`COLOR`/`BLACK`/`WHITE`). `ScnDisp Mode` (`Mode 1`/`Mode 2`) is listed in the parameter block but does not appear in the format row — **[UNDEFINED]** which Reserve slot it occupies. Upside_down has no effect on SDS100.

**`Backlight`** — `Backlight`, Reserve ×5, Brightness (`10`…`100` in 10s, SDS100 only), Key_Backlight (`Off`/`On`, SDS100), FlashLed (`Off`/`On`), Ext_PWR_Light (`Off`/`On`, SDS100), SQ Light (`Off`/`5`/`10`/`15`/`OpenSquelch`), Key Light (`15`/`30`/`60`/`120`/`Infinite`), Dimmer_mode (`Auto`/`Manual`, SDS200), Auto_Polarity (`Auto+`/`Auto-`, SDS200), Manual_Level (`Low`/`Middle`/`High`, SDS200), Dimmer_L / Dimmer_M / Dimmer_H (`10`…`100`, SDS200), Key_Backlight2 (`Off`/`On`, SDS200).

**`QuickKeys`** — `QuickKeys` + 100 parameters `F-Qkey_00`…`F-Qkey_99`, each `Off`/`On`. ("Favorites List Quick Status".)

**`Battery`** — `Battery⇥Battery Save⇥Reserve⇥Reserve⇥Alert Interval⇥Alert Tone⇥Alert Volume`. Battery Save `Off`/`On`; Alert Interval `1`–`60`; Alert Tone `640`/`780`/`920`/`1060`/`1200`; Alert Volume `Off`/`1`–`15`/`Auto`. **Handheld (SDS100) only.**

**`OwnerInfo`** — `OwnerInfo⇥Msg1st⇥Msg2nd⇥Msg3rd⇥Msg4th`, each ≤24 characters.

**`ClockOption`** — `ClockOption⇥Reserve⇥TimeZone⇥Daylight Save`. TimeZone `-14.0`…`14.0` in 0.5 steps (0.5 = 30 min). Daylight Save `On`/`Off`.

**`RecordingOption`** — `RecordingOption⇥Replay Duration⇥User Rec`. Replay Duration `0`, then `30`–`240` in 30 steps (1 = 1 s, `0` = Off). User Rec `On`/`Off` (**added V1.03**).

**`LimitSearch`** — 20 tokens (19 parameters):

| # | Field | Allowed values |
|---|---|---|
| 0 | `LimitSearch` | literal |
| 1 | MyId | `SrchId=nn` |
| 2 | Name | Name Tag |
| 3 | Lower | Hz |
| 4 | Upper | Hz |
| 5 | Modulation | `Auto`/`AM`/`NFM`/`FM`/`WFM`/`FMB` |
| 6 | Step | `Auto`/`5000`/`6250`/`7500`/`8333`/`10000`/`12500`/`15000`/`20000`/`25000`/`50000`/`100000` |
| 7 | Delay | `30,10,5,4,3,2,1,0,-5,-10` |
| 8 | Attenuator | `Off`/`On` |
| 9 | BankStatus | `Off`/`On` |
| 10 | Avoid | `Off`/`On` |
| 11 | Reserve | null |
| 12 | Reserve | null |
| 13 | Reserve | null |
| 14 | Reserve | null |
| 15 | Hold Time | `0`–`255` |
| 16 | Analog AGC | `Off`/`On` |
| 17 | Digital AGC | `Off`/`On` |
| 18 | Digital Waiting Time | `0`–`1000` |
| 19 | Digital Threshold Mode | `Auto`/`Manual`/`Default` |
| 20 | Digital Threshold Level | `5`–`13` |

*(That is 20 parameters / 21 tokens — the format table's rows give 6 + 6 + 5 + 3 = 20 parameters.)*

**`CloseCall`** (in `profile.cfg`) — `CloseCall`, Reserve, Reserve, Avoid (`Off`/`On`, for search‑with‑scan), pause_time (`3`,`5`,`10`,`15`,`30`,`45`,`60`,`Infinite`), Alert Type, Alert Volume, Alert Color, Alert Pattern, band_vhf_l1, band_vhf_l2, band_air, band_vhf_h1, band_vhf_h2, band_uhf, band_800 (each `Off`/`On`), Reserve, Reserve, Hold Time (`0`–`255`). 20 parameters.

**`ToneOut`** (in `profile.cfg`) — `ToneOut`, MyId (`ToneOutId=nn`), Name Tag, Reserve, Frequency (Hz), Modulation (`AUTO`/`NFM`/`FM` — **no AM/WFM/FMB**), Tone A (`0`, `2500`–`35000`, 1 = 0.1 Hz), Tone B (same), Attenuator (`Off`/`On`), delay (`0`,`1`,`2`,`3`,`4`,`5`,`10`,`30`,`Infinite` — **no negative delay**), agc_analog (`Off`/`On`), Alert Tone, Alert Volume, Alert Color, Alert Pattern. 14 parameters.

**`AvoidFreqs`** — `AvoidFreqs⇥Reserve⇥Freq 1 … Freq 16`, frequencies in Hz. 17 parameters.

**`IfxFreqs`** — `IfxFreqs⇥Reserve⇥Freq 1 … Freq 16`. IF‑exchanger frequency list. 17 parameters.

**`BandDefault`** — `BandDefault⇥MyId⇥Modulation⇥Step`. MyId `BandId=nn`; Modulation `AM`/`NFM`/`FM`/`WFM`/`FMB`; Step from the step enumeration (no `Auto`).

**`DispOptItems`** — `DispOptItems⇥DispOptId⇥DispLayoutId⇥OptItem1⇥OptItem2⇥…⇥OptItem n`.
**`DispColors`** — `DispColors⇥DispColorId⇥ColorLayoutId⇥TextColor1⇥BackColor1⇥…⇥TextColor n⇥BackColor n`. Colours are 6‑hex‑digit RGB with no `#`, e.g. `ff4600`, `000000`.

`DispLayoutId` / `ColorLayoutId` mapping:

| Mode | DispLayoutId | ColorLayoutId |
|---|---|---|
| Simple Conventional | 1 | 1 |
| Simple Trunk | 2 | 6 |
| Detail Conventional | 3 | 2 |
| Detail Trunk | 4 | 7 |
| Search / CC | 5 | 3 |
| Weather | 6 | 4 |
| Tone Out | 7 | 5 |

`DispOptId`: 1 = huge area, 2 = large area, 3 = small area, 4 = icon area. Item counts per group depend on the layout; the full item and colour code lists are in the spec's "Color, Item code" and "DiplsyCustomSDS100" sheets (partially reproduced in the PDF appendix).

### 4.28 Discovery records — in `discvery.cfg`

**`ConvDiscovery`** — 12 tokens (11 parameters):

| # | Field | Allowed values |
|---|---|---|
| 0 | `ConvDiscovery` | literal |
| 1 | Reserve | null |
| 2 | Name | max 32 characters |
| 3 | Lower | Hz |
| 4 | Upper | Hz |
| 5 | Modulation | `AUTO`/`AM`/`NFM`/`FM`/`WFM`/`FMB` |
| 6 | Step | `Auto`/`5000`/`6250`/`7500`/`8333`/`10000`/`12500`/`15000`/`20000`/`25000`/`50000`/`100000` |
| 7 | Delay | `5`,`4`,`3`,`2`,`1`,`0` (**no negative delay**) |
| 8 | Logging | `All` / `New` |
| 9 | Compare DB | `On` / `Off` |
| 10 | Duration | `0`/`30`/`60`/`90`/`120`/`150`/`180`/`300`/`600` (1 = 1 s recording duration) |
| 11 | Time‑Out | `0`/`10`/`30`/`60` (`0` = Off) |
| 12 | Auto Store | `Off` / `On` |

*(13 tokens / 12 parameters — the format rows give 5 + 3 + 4.)*

**`TrunkDiscovery`** — the section is headed `DiscoveryTrunk` but the command keyword in the format line is **`TrunkDiscovery`**. **[SPEC]** inconsistency; use `TrunkDiscovery`.

| # | Field | Allowed values |
|---|---|---|
| 0 | `TrunkDiscovery` | literal |
| 1 | Reverse *(sic — Reserve)* | null |
| 2 | Name | max 32 characters |
| 3 | Delay | `5`,`4`,`3`,`2`,`1`,`0` |
| 4 | Logging | `All` / `New` |
| 5 | Compare DB | `On` / `Off` |
| 6 | Duration | `0`/`30`/`60`/`90`/`120`/`150`/`180`/`300`/`600` |
| 7 | Fav Name | Favorites List name; **NULL if Full Database** |
| 8 | System Name | system name tag |
| 9 | Trunk ID | `TrunkId=nn` if Database = HPDB; **null if Database = Favorites List** |
| 10 | System Type | the System Type enumeration (4.4) |
| 11 | Site ID | `SiteId=nn` if Database = HPDB; **null if Database = Favorites List** |
| 12 | Site Name | site name tag |
| 13 | Time‑Out | `0`/`10`/`30`/`60` |
| 14 | Auto Store | `Off` / `On` |

> "Fav Name, System Name, Trunk ID and System Type, SiteId, Site Name are set by HomePatrol only. PC‑App doesn't modify. It reads those one, then writes same value to file. When PC‑App creates new Trunking Discovery, those parameters are set NULL." **[SPEC]**

### 4.29 Discovery log records (`summary.log`, `detail.log`)

**`RunInfo_C`** (first line of a conventional `summary.log`): `RunInfo_C⇥Latitude⇥Longitude⇥Range⇥Lower⇥Upper⇥Modulation⇥Step⇥Delay⇥Logging⇥Compare DB⇥Duration⇥Time-Out`. Delay `0,1,2,3,4,5`.

**`RunInfo_T`**: `RunInfo_T⇥Latitude⇥Longitude⇥Range⇥Delay⇥Logging⇥Compare DB⇥Duration⇥Fav Name⇥System Name⇥TrunkId⇥System Type⇥SiteId⇥Site Name⇥Time-Out`. Delay `0,2,5`. Fav Name is the list name when Database = Favorites List and null when Database = HPDB (**the inverse of `TrunkDiscovery`'s rule as written in the spec — see Gotchas**).

**`Summary_C_Log`**: `Summary_C_Log⇥Freq⇥Audio Option⇥Modulation⇥Duration⇥Hits⇥Name⇥Type⇥Known⇥Avoid`. Audio Option: `TONE=Cxxx.x`, `TONE=Dxxx`, `NAC=xxxx`, `ColorCode=xx`, `RAN=xx`, `Area=x`, `None`. Modulation `AM`/`NFM`/`FM`/`WFM`/`FMB`. Duration `0`–`600`. Hits `1`–`65535`. Type `S` (site freq) / `C` (conventional freq). Known `1`/`0`. Avoid `1`/`0`.

**`Summary_T_Log`**: `Summary_T_Log⇥TGID⇥Audio Mode⇥Duration⇥Hits⇥Name⇥Known⇥Avoid`. Audio Mode `ANALOG`/`DIGITAL`.

**`Detail_C_Log`**: `Detail_C_Log⇥Time⇥Freq⇥Audio Option⇥Modulation⇥Duration⇥Name⇥Type`. Time `MM/DD/YYYY hh:mm:ss`. Audio Option here uses the spaced form `"Color Code=xx"` in the Detail list (vs `ColorCode=xx` in Summary) — **spec inconsistency**.

**`Detail_T_Log`**: `Detail_T_Log⇥Time⇥TGID⇥Audio Mode⇥Duration⇥Unit ID⇥Reserve⇥Name⇥NAC/Color Code/RAN/Area⇥TDMA Slot`. Unit ID `1`–`16777215`, `0` if not detected. NAC/CC/RAN/Area: `xxxh` / `0-15` / `0-63` / `0-1` / `None`. TDMA Slot `1`, `2`, `None`.

### 4.30 Shared value formats

**Alert Tone Type** (`Alert Type`): `Off` / `1` … `9`.
(The `Trunk` emergency Alert Tone additionally allows `0`: `Off/0/1/…/9`.)

**Alert Tone Volume** (`Alert Volume`): `Auto` / `1` … `15`.

**Alert Light Color**: `Off`, `Blue`, `Red`, `Magenta`, `Green`, `Cyan`, `Yellow`, `White`.

**Alert Light Pattern**: `On`, `Slow Blink`, `Fast Blink`.
(The strings contain a space — they are still single TAB‑delimited fields.)

**Digital Waiting Time**: `0`–`1000`, 1 = 1 ms.

**Digital Threshold Mode**: `Auto`, `Manual`, `Default`.

**Digital Threshold Level**: `5`–`13`.

**Delay** (channel/search): `30`, `10`, `5`, `4`, `3`, `2`, `1`, `0`, `-5`, `-10`.

**Step**: `Auto`, `5000`, `6250`, `7500`, `8333`, `10000`, `12500`, `15000`, `20000`, `25000`, `50000`, `100000`.

**Filter** (V1.03): `Global`, `Normal`, `Invert`, `Auto`.

### 4.31 `app_data.cfg` records (resume state)

Not written by a PC application — the file must be **deleted**. Listed for recognition only: `ModeInfo`, `ScanListType`, `ScanConvSystem`, `ScanTrunkSystem`, `ScanC-Group`, `ScanT-Group`, `ScanC-Freq`, `ScanTGID`, `ScanSite`, `ScanT-Freq`, `SearchWithScan`, `CustomSearch`, `QuickSearch`, `ToneOut`, `CloseCall`.

Each `Scan*` record is the corresponding `.hpd` record **prefixed by a hold flag and the original command keyword**, e.g.:

```
ScanConvSystem⇥<System Hold>⇥Conventional⇥<MyId>⇥<ParentId>⇥…
ScanC-Freq⇥C-Freq⇥<MyId>⇥<ParentId>⇥…
ScanSite⇥<Site Hold>⇥Site⇥<MyId>⇥…
ScanT-Freq⇥T-Freq⇥<Reserve(MyId)>⇥<ParentId>⇥…
```

`ModeInfo⇥Mode⇥System Hold⇥Dept Hold`, Mode ∈ `Scan`, `ScanHold`, `ToneOut`, `ToneOutHold`, `CustomSearch`, `CustomSearchHold`, `QuickSearch`, `QuickSearchHold`, `IDsearch`, `IDsearchhold`, `IDscan`, `IDmanual`, `EdacsIDSearch`, `EdacsIDSearchHold`, `EdacsIDScan`, `EdacsIDManual`, `LTRIDSearch`, `LTRIDSearchHold`, `LTRIDScan`, `LTRIDManual`, `CloseCallOnly`, `WeatherScan`, `WeatherHold`.

`ScanListType⇥Type⇥Name`, Type ∈ `FullDatabase`, `FavoritesList`, `SearchWithScan`.

---

## 5. TGID formats by trunking type

The `TGID` field of a `TGID` record (and the `TGID n` fields of `AvoidTgids`) is a **string** whose syntax is fixed by the parent `Trunk` record's `System Type`. Reproduced verbatim from the spec, with worked examples.

### 5.1 Motorola Type I

| # | Form | Meaning | Rules | Example |
|---|---|---|---|---|
| 1 | `BFF-SS` | Normal ID | B = 1 digit; FF = 2 digits, zero‑filled; SS = 1–2 digits | `100-12` |
| 2 | `BFFF-S` | Normal ID when FLEET is 100–127 | B = 1 digit; FFF = 3 digits; S = 1 digit | `1100-3` |
| 3 | `B-` | Partial ID (free FLEET and SUB‑FLEET) | B = 1 digit | `1-` |
| 4 | `BFF-` | Partial ID (free SUB‑FLEET) | B = 1 digit; FF = 2 digits, zero‑filled | `103-` |
| 5 | `BFFF-` | Partial ID (free SUB‑FLEET, FLEET 100–127) | B = 1 digit; FFF = 3 digits | `1100-` |
| 6 | `NNNNN` | Normal ID when SIZE CODE is 0 | N = 1–5 digits | `12345` |
| 7 | `iNNNNN` | I‑CALL ID | N = 1–5 digits | `i1234` |

> **"TYPE I format is no check."** **[SPEC]** — the scanner performs no validation on Type I TGID strings. Getting this wrong is silent.

### 5.2 Motorola Type II

| # | Form | Range | Example |
|---|---|---|---|
| 1 | `NNNNN` — Normal ID | 1–5 digits, value `1`–`65535` | `1234` |
| 2 | `iNNNNN` — I‑CALL ID | 1–5 digits, `i0`–`i65535` | `i1234` |

### 5.3 P25 (`P25Standard`, `P25OneFrequency`, `P25X2_TDMA`)

| # | Form | Range | Example |
|---|---|---|---|
| 1 | `NNNNN` — Normal ID | 1–5 digits, value `1`–`65535` | `40961` |
| 2 | `iNNNNNNNN` — I‑CALL ID | 1–8 digits, `i0`–`i16777215` | `i1234567` |

> "P25 format cannot support PARTIAL ID." **[SPEC]**

### 5.4 DMR / MotoTRBO (`MotoTrbo`, `DmrOneFrequency`)

| # | Form | Range | Example |
|---|---|---|---|
| 1 | `NNNNNNNN` — Normal ID | 1–8 digits, value `1`–`16777215` | `2345678` |
| 2 | `iNNNNNNNN` — I‑CALL ID | 1–8 digits, `i0`–`i16777215` | `i2345678` |

> "DMR format cannot support PARTIAL ID." **[SPEC]**

### 5.5 NXDN (`Nxdn`, `NxdnOneFrequency`)

| # | Form | Range | Example |
|---|---|---|---|
| 1 | `NNNNN` — Normal ID | 1–5 digits, value `0`–`65535` (note: **0 is valid**, unlike Motorola Type II) | `501` |
| 2 | `iNNNNN` — I‑CALL ID | 1–5 digits, `i0`–`i65535` | `i501` |

> "NXDN does not support PARTIAL ID." **[SPEC]**
> The parent `Trunk` record's `TGID Format` field (`NEXEDGE` / `IDAS`) selects how the scanner *displays/interprets* NXDN ids.

### 5.6 EDACS

| # | Form | Meaning | Rules | Example |
|---|---|---|---|---|
| 1 | `AA-FFS` | Normal ID (AFS) | decimal 1–2047 overall; AA = AGENCY 2 digits `00`–`15` zero‑filled; FF = FLEET 2 digits `00`–`15` zero‑filled; S = SUB‑FLEET 1 digit `0`–`7` | `03-121` |
| 2 | `AA----` | Partial ID (free FLEET and SUB‑FLEET) | | `03----` |
| 3 | `AA-FF-` | Partial ID (free SUB‑FLEET) | | `03-12-` |
| 4 | `iNNNNNN` | I‑CALL ID | NNNNNN = 1–7 digits, `i0`–`i1048575` | `i104857` |
| 5 | `NNNNN` | EA (Extended Addressing) system | 1–5 digits, value `2048`–`65535` | `4096` |

> `ALL0` (`00-00-0`) is invalid. **[SPEC]**
> Note the literal hyphens are part of the string, and form 2 uses **four** trailing hyphens.
> The `DisplayOption` `EdacTgidFormat` (`AFS`/`DEC`) controls display only.

### 5.7 LTR

| # | Form | Meaning | Rules | Example |
|---|---|---|---|---|
| 1 | `A-RR-NNN` | Normal ID | A = AREA CODE, 1 digit (`0` or `1`); RR = HOME REPEATER, 2 digits `01`–`20` zero‑filled; NNN = ID, 3 digits `000`–`254` zero‑filled | `0-05-123` |
| 2 | `A-RR----` | Partial ID | | `0-05----` |

> "LTR System has no I‑CALL ID." **[SPEC]**
> Partial form takes **four** trailing hyphens after the repeater.

### 5.8 Conventional / Scat

`Conventional` systems use `C-Freq` records, not `TGID` records. `Scat` is marked "(reserve parameter)" — do not emit it.

---

## 6. Worked example — minimal valid Favorites List

One conventional system, one department, two NFM channels: **446.00625 MHz** and **453.7250 MHz**.

### 6.1 `BCDx36HP\favorites_lists\f_000001.hpd`

Shown with `⇥` for each TAB and `␀` for each null (empty) field. The actual file contains neither symbol — `⇥` is a single 0x09 byte and `␀` is nothing at all.

```
TargetModel⇥BCDx36HP
FormatVersion⇥1.00
Conventional⇥␀⇥␀⇥UK Test System⇥Off⇥␀⇥Conventional⇥Off⇥Off⇥0⇥Off⇥Off⇥400⇥Auto⇥8
C-Group⇥␀⇥␀⇥Department 1⇥Off⇥0.000000⇥0.000000⇥0.0⇥Circle⇥Off⇥Global
C-Freq⇥␀⇥␀⇥PMR446 Ch1⇥Off⇥446006250⇥NFM⇥␀⇥21⇥Off⇥2⇥0⇥Off⇥Auto⇥Off⇥On⇥Off⇥Off
C-Freq⇥␀⇥␀⇥UHF 453.725⇥Off⇥453725000⇥NFM⇥␀⇥21⇥Off⇥2⇥0⇥Off⇥Auto⇥Off⇥On⇥Off⇥Off
```

Same thing with literal tabs, copy‑pasteable (each line must end CRLF):

```
TargetModel	BCDx36HP
FormatVersion	1.00
Conventional			UK Test System	Off		Conventional	Off	Off	0	Off	Off	400	Auto	8
C-Group			Department 1	Off	0.000000	0.000000	0.0	Circle	Off	Global
C-Freq			PMR446 Ch1	Off	446006250	NFM		21	Off	2	0	Off	Auto	Off	On	Off	Off
C-Freq			UHF 453.725	Off	453725000	NFM		21	Off	2	0	Off	Auto	Off	On	Off	Off
```

**Token counts to verify when generating:**

| Record | Tokens (command + params) |
|---|---|
| `TargetModel` | 2 |
| `FormatVersion` | 2 |
| `Conventional` | 15 |
| `C-Group` | 11 |
| `C-Freq` | 18 |

### 6.2 Field‑by‑field for the example

**`Conventional`**

| # | Field | Value | Basis |
|---|---|---|---|
| 1 | MyId | *(null)* | **[SPEC]** Favorites List |
| 2 | ParentId | *(null)* | **[SPEC]** Favorites List |
| 3 | Name Tag | `UK Test System` | |
| 4 | Avoid | `Off` | |
| 5 | Reserve | *(null)* | **[SPEC]** |
| 6 | System Type | `Conventional` | **[INFERRED]** — spec leaves this value list blank for `Conventional` |
| 7 | Quick Key | `Off` | |
| 8 | Number tag | `Off` | |
| 9 | System Hold Time | `0` | |
| 10 | Analog AGC | `Off` | |
| 11 | Digital AGC | `Off` | |
| 12 | Digital Waiting Time | `400` | **[INFERRED]** — any value 0–1000 is legal; the spec states no default |
| 13 | Digital Threshold Mode | `Auto` | |
| 14 | Digital Threshold Level | `8` | **[INFERRED]** — any value 5–13 is legal; spec states no default |

**`C-Group`**

| # | Field | Value | Basis |
|---|---|---|---|
| 1 | MyId | *(null)* | **[SPEC]** |
| 2 | ParentId | *(null)* | **[SPEC]** |
| 3 | Name Tag | `Department 1` | |
| 4 | Avoid | `Off` | |
| 5 | Latitude | `0.000000` | **[INFERRED]** — the spec defines no "no location" encoding. `0.000000` is used here; a null field is the alternative reading. **Verify.** |
| 6 | Longitude | `0.000000` | **[INFERRED]** — as above |
| 7 | Range | `0.0` | **[INFERRED]** — as above |
| 8 | Location Type | `Circle` | **[INFERRED]** — `Circle` avoids needing a `Rectangle` record |
| 9 | Quick Key | `Off` | |
| 10 | Filter | `Global` | V1.03 field; `Global` defers to the profile's Global Auto Filter |

**`C-Freq` (channel 1)**

| # | Field | Value | Basis |
|---|---|---|---|
| 1 | MyId | *(null)* | **[SPEC]** |
| 2 | ParentId | *(null)* | **[SPEC]** |
| 3 | Name Tag | `PMR446 Ch1` | |
| 4 | Avoid | `Off` | |
| 5 | Frequency | `446006250` | 446.00625 MHz × 1 000 000 |
| 6 | Modulation | `NFM` | |
| 7 | Audio Option | *(null)* | **[SPEC]** null = "All" (no CTCSS/DCS/NAC squelch) |
| 8 | FuncTagId | `21` | `21` = "Other" (Appendix B) |
| 9 | Attenuator | `Off` | |
| 10 | Delay | `2` | |
| 11 | Volume Offset | `0` | |
| 12 | Alert Tone | `Off` | |
| 13 | Alert Volume | `Auto` | **[INFERRED]** — `Auto` is legal even with Alert Tone `Off`; the spec does not define what to write when alerting is off |
| 14 | Alert Color | `Off` | |
| 15 | Alert Pattern | `On` | **[INFERRED]** — the pattern enum has no "Off" member, so `On` is the only neutral value |
| 16 | Number tag | `Off` | |
| 17 | Priority Channel | `Off` | |

**`C-Freq` (channel 2)** — identical but Name Tag `UHF 453.725` and Frequency `453725000`.

### 6.3 `BCDx36HP\favorites_lists\f_list.cfg`

117 tokens on the `F-List` line: `F-List`, 6 named parameters, 10 startup keys, 100 S‑Qkeys.

```
TargetModel	BCDx36HP
FormatVersion	1.00
F-List	My Favourites	f_000001.hpd	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	On	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off	Off
```

Layout of that line:

| Position | Value |
|---|---|
| 1 UserName | `My Favourites` |
| 2 Filename | `f_000001.hpd` |
| 3 LocationControl | `Off` (= No) |
| 4 Monitor | `Off` |
| 5 Quick key | `Off` |
| 6 NumberTag | `Off` |
| 7–16 Startup key 0–9 | all `Off` |
| 17 S‑Qkey_00 | **`On`** — enables system quick key 0, which is what the `Conventional` record's `Quick Key = Off` falls back to **[INFERRED]** |
| 18–116 S‑Qkey_01…99 | all `Off` |

**[INFERRED] / ambiguous in the example — summary**

| Field | Why inferred |
|---|---|
| `FormatVersion` value `1.00` | Spec gives only the pattern `x.xx`, never a value |
| `Conventional` System Type `Conventional` | Spec leaves that parameter's value list blank |
| Digital Waiting Time `400`, Threshold Level `8` | No defaults stated; any in‑range value is legal |
| `C-Group` Latitude/Longitude/Range `0.000000`/`0.000000`/`0.0` | No "no location" encoding defined |
| Alert Volume `Auto`, Alert Pattern `On` when alerting is off | No "off" member in those enums |
| S‑Qkey_00 = `On` | Spec never says which quick keys must be on for a list's systems to scan |
| Null = empty field (not a literal space) | "just space" vs "It is just null" |
| CRLF line endings | Not specified |
| `TargetModel`/`FormatVersion` present in `f_list.cfg` | Spec's "all HomePatrol data file" rule, applied by inference |
| Record ordering = hierarchy | Forced by all ids being null, but never stated |

### 6.4 Generator pseudocode

```python
TAB = "\t"
NL  = "\r\n"

def rec(cmd, *fields):
    return cmd + TAB + TAB.join("" if f is None else str(f) for f in fields) + NL

hpd  = rec("TargetModel",   "BCDx36HP")
hpd += rec("FormatVersion", "1.00")
hpd += rec("Conventional", None, None, "UK Test System", "Off", None,
           "Conventional", "Off", "Off", 0, "Off", "Off", 400, "Auto", 8)
hpd += rec("C-Group", None, None, "Department 1", "Off",
           "0.000000", "0.000000", "0.0", "Circle", "Off", "Global")
for name, hz in (("PMR446 Ch1", 446006250), ("UHF 453.725", 453725000)):
    hpd += rec("C-Freq", None, None, name, "Off", hz, "NFM", None, 21,
               "Off", 2, 0, "Off", "Auto", "Off", "On", "Off", "Off")

flist  = rec("TargetModel",   "BCDx36HP")
flist += rec("FormatVersion", "1.00")
sqkeys = ["On"] + ["Off"] * 99          # S-Qkey_00 .. S-Qkey_99
flist += rec("F-List", "My Favourites", "f_000001.hpd",
             "Off", "Off", "Off", "Off",
             *(["Off"] * 10),            # Startup key 0..9
             *sqkeys)

# write as ASCII, then DELETE BCDx36HP/app_data.cfg
```

---

## 7. Gotchas — things that silently corrupt a file or get it rejected

1. **Leaving `app_data.cfg` in place.** The spec is explicit: a PC application **must delete** `BCDx36HP\app_data.cfg` whenever it writes program data. Leaving it means the scanner resumes from a snapshot that references systems/channels that no longer exist.
2. **Dropping trailing fields.** Every record carries its full field count even when fields are null. A `C-Freq` with 15 tokens instead of 18 will mis‑align every field after the first omission.
3. **Spaces instead of tabs.** Several field values contain spaces (`Slow Blink`, `Fast Blink`, `Analog+Digital` has none but `MM/DD/YYYY hh:mm:ss` does). Only TAB separates fields; a naive whitespace split corrupts the record.
4. **Writing `MyId`/`ParentId` into a Favorites List.** Explicitly forbidden — they must be null. Conversely, `T-Freq`'s first field is **not** nulled: it is always the literal `TFreqId=0`.
5. **Emitting `AreaState` or `AreaCounty` in a Favorites List.** Explicitly not needed.
6. **Record order is the only hierarchy.** With ids null, a `C-Freq` attaches to the preceding `C-Group`, which attaches to the preceding `Conventional`. Reordering records silently reparents channels.
7. **Frequencies in anything other than Hz.** `446.00625` or `44600625` instead of `446006250` is accepted as a syntactically valid integer and lands on the wrong frequency.
8. **Mixing up `Tone A`/`Tone B` units.** `ToneOut` tones are in 0.1 Hz (`2500`–`35000`), not Hz. The spec itself calls this "illegal frequency format".
9. **Name tags outside 0x20–0x7E or over 64 characters.** There is no escaping mechanism, and no defined truncation behaviour for over‑length tags in a data file (only for activity‑log *filenames*).
10. **`Modulation` enums differ by record.** `C-Freq`: `AUTO/AM/NFM/FM`. `Site`: `AUTO/NFM/FM` (no AM). `LimitSearch`/`SearchCommon`/`ConvDiscovery`: `Auto/AM/NFM/FM/WFM/FMB`. `BandDefault`: `AM/NFM/FM/WFM/FMB` (no Auto). `ToneOut`: `AUTO/NFM/FM`. Note also the case difference: `AUTO` in `.hpd` records, `Auto` in search records.
11. **`Delay` enums differ by record.** Channels and search: `30,10,5,4,3,2,1,0,-5,-10`. Discovery: `5,4,3,2,1,0` only (spec: "Not support minus delay"). `ToneOut`: `0,1,2,3,4,5,10,30,Infinite`. `RunInfo_T`: `0,2,5`.
12. **`Number tag` ranges differ.** Systems, sites, groups and `F-List`: `Off/0-99`. Channels (`C-Freq`, `TGID`): `Off/0-999`.
13. **`TGID` has no `Attenuator` field** but `C-Freq` does. Copy‑pasting a channel writer between the two shifts every subsequent field.
14. **`C-Group` has a trailing `Filter` field; `T-Group` does not.** "Filter is C‑Group only."
15. **TGID string syntax is system‑type dependent and Type I is unchecked.** "TYPE I format is no check" — a malformed Motorola Type I TGID will be stored and will simply never match.
16. **EDACS `00-00-0` is invalid.** Also, EDACS partial IDs use four hyphens (`AA----`, `AA-FF-`), and LTR partial IDs use four (`A-RR----`). Getting the hyphen count wrong is invisible in a diff.
17. **Zero‑filling matters.** Motorola Type I `FF`, EDACS `AA`/`FF`, LTR `RR` and `NNN` are all explicitly zero‑filled fixed‑width. `AgencyId`‑style ids and frequencies are not.
18. **`Mot Band Type` and `Edacs Band Type` must be neutralised on non‑matching systems.** "If `[Trunk]` System Type isn't Motorola, Mot Band Type is set `Standard`. If `[Trunk]` System Type isn't Edacs, Edacs Band Type is set `Wide`."
19. **`BandPlan_Mot` only when `Mot Band Type = Custom`.** Writing it otherwise is out of spec.
20. **`AvoidTgids` must be omitted entirely when empty**, and split into multiple records past 16 entries — not extended to 17+ fields.
21. **`Location Type = Rectangles` requires a `Rectangle` record.** With no `Rectangle`, the location filter has no bounds.
22. **V1.03 fields on older firmware.** `Filter` (Site, C‑Group, SearchCommon), `TGID Format` (Trunk), `Global Auto Filter` and `Site NAC Operation` (GlobalSetting), `SCPU_Ver` (Scanner), `User Rec` (RecordingOption) were added in spec V1.02/V1.03. Firmware older than those revisions may reject the extra field.
23. **Spelling traps in the spec that must be reproduced or avoided:** the command is `TrunkDiscovery` though the section is headed `DiscoveryTrunk`; the command is `County` though the section is headed `CountyInfo`; the file is `discvery.cfg` (missing "o"); the directory is `favorites_lists`; `UnitIds` field 6 is spelled `Alert Volue` in the parameter list but is the Alert Volume value set; `LM` field 3 is spelled `TruniId`; `TrunkDiscovery` field 1 is spelled `Reverse` but is a Reserve.
24. **`System Type` casing is inconsistent across the spec:** `MotoTrbo` in `Trunk`/`ScanTrunkSystem`, `MotoTRBO` in `TrunkDiscovery`/`RunInfo_T`. **[INFERRED]** use `MotoTrbo` in `.hpd` files, matching the `Trunk` record definition.
25. **`Fav Name` null‑direction contradiction.** `TrunkDiscovery` says Fav Name is "set NULL if Full Database"; `RunInfo_T` says Fav Name is the list name when Database = Favorites List and "null (Database=HPDB)". These agree in substance but are worded as opposites in the two places — read carefully before writing discovery files.
26. **Audio Option token case.** The `C-Freq` parameter block writes `Tone=xxxx`; the audio table writes `TONE=C100.0`. Use the table's `File` column verbatim.
27. **`Avoid` in HPDB is always `Off`.** Real avoid state lives in `s_NNNNNN.avd`. In a Favorites List, `Avoid` in the record *is* the live value.
28. **Two `Avoid` record shapes in `.avd` are ambiguous** (3‑parameter department vs 3‑parameter site). Do not generate `.avd` files from this spec alone.
29. **`Alert Light Pattern` has no "off" value** (`On`/`Slow Blink`/`Fast Blink`), so a channel with alerting disabled still needs a pattern written.
30. **Over‑long `f_list.cfg` lines.** The `F-List` record is 117 tokens; a parser or editor that wraps or truncates long lines will corrupt it.

---

## Appendix A — field‑count quick reference (`.hpd`)

| Record | Command + parameters = tokens |
|---|---|
| `TargetModel` | 1 + 1 = 2 |
| `FormatVersion` | 1 + 1 = 2 |
| `Conventional` | 1 + 14 = 15 |
| `Trunk` | 1 + 21 = 22 |
| `AreaState` | 1 + 2 = 3 (HPDB only) |
| `AreaCounty` | 1 + 2 = 3 (HPDB only) |
| `FleetMap` | 1 + 9 = 10 |
| `UnitIds` | 1 + 8 = 9 |
| `AvoidTgids` | 1 + 17 = 18 |
| `Site` | 1 + 18 = 19 |
| `Rectangle` | 1 + 5 = 6 |
| `BandPlan_Mot` | 1 + 25 = 26 |
| `BandPlan_P25` | 1 + 33 = 34 |
| `DQKs_Status` | 1 + 101 = 102 |
| `C-Group` | 1 + 10 = 11 |
| `T-Group` | 1 + 9 = 10 |
| `C-Freq` | 1 + 17 = 18 |
| `TGID` | 1 + 16 = 17 |
| `T-Freq` | 1 + 7 = 8 |
| `F-List` (`f_list.cfg`) | 1 + 116 = 117 |

---

## Appendix B — `FuncTagId` (Service Type) values

| Id | Service Type | Id | Service Type |
|---|---|---|---|
| 1 | Multi‑Dispatch | 20 | Railroad |
| 2 | Law Dispatch | 21 | Other |
| 3 | Fire Dispatch | 22 | Multi‑Talk |
| 4 | EMS Dispatch | 23 | Law Talk |
| 5 | *(unused)* | 24 | Fire‑Talk |
| 6 | Multi‑Tac | 25 | EMS‑Talk |
| 7 | Law Tac | 26 | Transportation |
| 8 | Fire‑Tac | 27 | *(unused)* |
| 9 | EMS‑Tac | 28 | *(unused)* |
| 10 | *(unused)* | 29 | Emergency Ops |
| 11 | Interop | 30 | Military |
| 12 | Hospital | 31 | Media |
| 13 | Ham | 32 | Schools |
| 14 | Public Works | 33 | Security |
| 15 | Aircraft | 34 | Utilities |
| 16 | Federal | 35 | *(unused)* |
| 17 | Business | 36 | *(unused)* |
| 18 | *(unused)* | 37 | Corrections |
| 19 | *(unused)* | | |

Custom service types:

| Id | Name | Id | Name |
|---|---|---|---|
| 208 | Custom 1 | 213 | Custom 6 |
| 209 | Custom 2 | 214 | Custom 7 |
| 210 | Custom 3 | 215 | Custom 8 |
| 211 | Custom 4 | 216 | Racing Officials |
| 212 | Custom 5 | 217 | Racing Teams |

> The spec's `C-Freq`/`TGID` parameter blocks give the range as `1-37, 208-218`, but the Service Type table stops at **217**. `218` is **[UNDEFINED]**. Do not use it.
> The spec's `CustomServiceType` record has 10 slots (Id01–Id10) mapping to FuncTagId 208–217.

---

## Appendix C — `Audio Option` tokens (`C-Freq`, field 7)

The value written to the file is the table's **File** column. Null (empty field) = "All" (no sub‑audio squelch).

| Category | File token form | Values |
|---|---|---|
| All | *(null)* | — |
| Tone search | `TONE=Srch` | — |
| CTCSS | `TONE=C<freq>` | see list below (50 tones, one decimal place) |
| DCS | `TONE=D<code>` | see list below (112 codes, 3 digits) |
| P25 NAC search | `NAC=Srch` | — |
| P25 NAC | `NAC=<hex>` | `NAC=0` … `NAC=FFF` (hex, no zero‑fill in the file token: the table shows `NAC=0`, `NAC=A`, `NAC=10`, `NAC=FFF`) |
| DMR colour code | `ColorCode=<n>` | `ColorCode=0` … `ColorCode=15` |
| NXDN RAN | `RAN=<n>` | `RAN=0` … `RAN=63` |
| NXDN Area | `Area=<n>` | `Area=0`, `Area=1` |

**Tone lockout:** append `,L/O` after the CTCSS/DCS token when the channel has the Tone L/O setting, e.g. `TONE=C100.0,L/O`. **[SPEC]** — the spec's worked example ("Ex.") is blank in the PDF, so the exact placement is **[INFERRED]** from the sentence "`,L/O` is appended after ctcss/dcs".

**CTCSS values (Hz), in spec order:**

```
67.0  69.3  71.9  74.4  77.0  79.7  82.5  85.4  88.5  91.5
94.8  97.4 100.0 103.5 107.2 110.9 114.8 118.8 123.0 127.3
131.8 136.5 141.3 146.2 151.4 156.7 159.8 162.2 165.5 167.9
171.3 173.8 177.3 179.9 183.5 186.2 189.9 192.8 196.6 199.5
203.5 206.5 210.7 218.1 225.7 229.1 233.6 241.8 250.3 254.1
```

**DCS codes, in spec order:**

```
006 007 015 017 021 023 025 026 031 032 036 043 047 050 051 053 054 065
071 072 073 074 114 115 116 122 125 131 132 134 141 143 145 152 155 156
162 165 172 174 205 212 214 223 225 226 243 244 245 246 251 252 255 261
263 265 266 271 274 306 311 315 325 331 332 343 346 351 356 364 365 371
411 412 413 423 431 432 445 446 452 454 455 462 464 465 466 503 506 516
523 526 532 546 565 606 612 624 627 631 632 654 662 664 703 712 723 731
732 734 743 754
```

**Discovery folder‑name variants** (used only in `discovery\…` directory names, *not* in `.hpd`): `None`, `C67.0` … `C254.1`, `D006` … `D754`, `N0` … `NFFF` (NAC), `ColorCode0` … `ColorCode15`, `RAN0` … `RAN63`, `Area0`, `Area1`.
The discovery **log** Audio Option column uses yet another variant: `None`, `TONE=Cxxx.x`, `TONE=Dxxx`, `NAC=xxxx`, `ColorCode=xx` (Summary) or `Color Code=xx` (Detail), `RAN=xx`, `Area=x`.

---

## Appendix D — what the specification leaves undefined

| Item | Status |
|---|---|
| Per‑field "null in Favorites / null in HPDB / null for user input" colour coding | **Lost** — swatches absent from the PDF. Only the prose notes survive. |
| Line terminator (CR / LF / CRLF) | Not stated |
| Whether "null" is an empty field or a literal space | "just space" vs "It is just null" — contradictory |
| `FormatVersion` value for SDS100/SDS200 | Never given, only the pattern `x.xx` |
| Whether SDS100/200 use a `TargetModel` other than `BCDx36HP` | Not stated |
| The "File type" header the Basic Rule demands | No such record is defined |
| Record ordering / nesting rules inside a `.hpd` | Never stated; forced by null ids |
| Default values for any numeric field | Never given |
| Encoding for "no location" on a `C-Group`/`T-Group`/`Site` | Not stated |
| How partially used `AvoidTgids` / `BandPlan_Mot` / `BandPlan_P25` slots are padded | Not stated |
| `T-Freq` `LCN` — which of the four ranges applies to which system type | Not stated |
| Band Coverage table (needed for `BandPlan_P25` Base) | Referenced, not included |
| "Sub Audio" sheet | Referenced; the audio table in the appendix appears to be it, but is not labelled as such |
| "DiplsyCustomSDS100" and "Color, Item code" sheets | Referenced; only partially reproduced |
| Distinguishing department vs site `Avoid` records in `.avd` | Both are 3‑parameter; no disambiguator shown |
| `DisplayOption` `ScnDisp Mode` field position | Listed in parameters, absent from the format row |
| Maximum counts (systems per list, departments per system, channels per department, lists per card beyond 999999 filenames) | Not stated anywhere |
| File size limits | Not stated |
| `FuncTagId` 218 | In the stated range, absent from the table |
| Behaviour on malformed input (reject file, reject record, ignore field) | Not stated |
