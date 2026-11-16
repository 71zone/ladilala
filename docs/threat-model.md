# Threat model

Status date: 2026-11-16. Most of the app is not built. Rows marked "done" are code that exists in this repo. Rows marked "planned" are decisions with no code. Rows marked "open" have no decision. Statements I could not check are marked [INFERENCE].

## Assets

| Asset | Where it lives | Why it matters |
|---|---|---|
| Profile data (user-data-dir, cookies, local storage, logins) | One folder per profile on disk, mode 0700 | Holds signed-in sessions |
| Proxy credentials | Planned: Windows Credential Manager (ADR 0013). In memory in `profile.Proxy.Password`, tagged `json:"-"` | Paid access, can be abused |
| Session cookies | Inside profile data | Account takeover |
| Macros | JSON files, later SQLite | A macro acts as the signed-in user |
| Data sources | CSV, Excel, JSON, REST, Google Sheets rows | Personal data, card numbers in some uses |
| Google tokens | Planned for Sheets. Not built. ADR 0016 is open | Read access to the user's sheets |
| Run ledger | Planned SQLite | Proves what was submitted |
| Update feed and signing key | Planned | Control of every install |

## Actors

| Actor | What they can do |
|---|---|
| Malicious macro file | Arrives by import or share. Tries to send data to another site or to act in a signed-in session |
| Malicious website | Runs inside a profile. Tries to detect automation, read recorder traces, or attack the CDP link |
| Local malware | Runs as the same Windows user. Can read files, processes and loopback ports |
| Curious second user | Uses the same Windows account or a shared PC |
| Supply chain | Poisoned Go or npm dependency, poisoned engine download, poisoned update |
| Abusive user | Uses the product for spam, fraud or ban evasion |

## Trust boundaries

1. UI to Go core. The UI is a WebView2 page. It is untrusted for validation. The Go core checks every input. Not built yet.
2. Go core to browser. The link is the CDP pipe. The browser and every page in it are untrusted.
3. Go core to relay to upstream proxy. The relay is trusted code. The upstream proxy is not.
4. App to update server and engine download servers. Everything fetched is untrusted until verified.
5. App to files in the data folder. Local malware can reach them.

## What we defend

| Defence | State | Evidence |
|---|---|---|
| `allowedOrigins` is required in every macro. Navigate URLs must fit it | done | `core/macro/validate.go`, `Macro.AllowsURL`, `testdata/invalid_navigate_outside_origin.json` |
| The player refuses a navigate or a frame outside `allowedOrigins` (`OriginError`) | done | `core/player/player.go` lines 111 and 262, `vars.go` line 74 |
| No JavaScript step in the macro schema | done | Action set in `core/macro/macro.go`. Unknown fields and actions fail validation |
| Closed set of value transforms. No code in expressions | done | `core/macro/expr.go` |
| Recorder never stores a password. It writes `{{password}}` and declares the variable | done | `core/recorder/recorder.js`, `convert.go` |
| CDP runs over `--remote-debugging-pipe`. No TCP debug port | done | `core/driver/stock.go` |
| Profile data dir must be absolute and not the default Chrome dir | done | `core/driver/datadir.go` |
| One Job Object kills all browsers when the app dies | done on Windows | `core/driver/job_windows.go`, R4 pass |
| Relay fails closed. No direct dial when the upstream fails | done, fake upstreams only | `core/proxy/relay.go`, `TestUpstreamDownNeverDialsDirect` |
| Relay listens on `127.0.0.1` with a random port. Direct mode needs opt-in | done | `relay.go`, `TestDirectRequiresOptIn` |
| Relay removes any client `Proxy-Authorization` and sets its own | done | `relay.go` line 326 |
| Proxy password not serialised to JSON | done | `core/profile/types.go` |
| Managed-PC policy `RemoteDebuggingAllowed=0` gives a clear error | done | `core/driver/policy_windows.go`, `ErrDebuggingBlocked` |
| DPAPI or Credential Manager for secrets | planned | ADR 0013. [INFERENCE] The brief says DPAPI. The ADR names Credential Manager, which uses DPAPI underneath |
| Signed app updates and signed engine manifest | planned | Update manager note. No code |
| Redaction of secrets in logs and ledger | planned | No logging or ledger code exists |
| Commit point and ledger against duplicate submit | planned | ADR 0014. Schema has `commitPoint`. Engine is in progress |

## What we do NOT defend

- Local malware running as the same Windows user. It can read profile folders, cookies and memory. DPAPI does not stop it.
- A user who exports a macro or data and sends it away.
- A browser zero-day. Stock Chrome and Edge update through their vendors. Own builds do not exist.
- A fully compromised Windows PC.
- Detection by a site. No design can promise this. See the policy risk below.
- Physical access to an unlocked PC.

## Threat table

STRIDE: S spoofing, T tampering, R repudiation, I information disclosure, D denial of service, E elevation of privilege.

| ID | Threat | STRIDE | Mitigation | Status |
|---|---|---|---|---|
| T-1 | Imported macro navigates to an attacker site and leaks row data | I | `allowedOrigins` check on navigate and frames. Import review screen shows origins | done in core. Review screen planned |
| T-2 | Imported macro clicks buy or delete in a signed-in session | E | `allowedOrigins`, no JS step, commit points need a ledger, user runs first row alone | partly done |
| T-3 | Macro file carries unknown fields or code | T | Strict decode. Unknown fields are errors | done |
| T-4 | Duplicate submit after crash | T, R | `commitPoint`, `submitting` ledger row, `Uncertain` state | planned |
| T-5 | Proxy password stored in clear text | I | Not in SQLite. Credential Manager. `json:"-"` | partly done. Store not built |
| T-6 | Relay used by local malware as an open proxy with paid credentials | E | Loopback only. [INFERENCE] The relay has no local client authentication. Any local process that finds the port can use it | open |
| T-7 | Relay leaks traffic when the upstream dies | I | Fail closed | done, fake upstreams only |
| T-8 | DNS or WebRTC leaks the real IP | I | Hostnames go to the upstream unresolved. WebRTC needs flags and prefs, not built | partly done. WebRTC open |
| T-9 | Malicious page attacks the CDP link | E | Pipe, not TCP. No port to scan | done |
| T-10 | Page detects the recorder overlay | I | One extra element is visible. User accepted this risk | accepted |
| T-11 | Page detects CDP use or injector | I | No `Runtime.enable`. Test asserts it. Detector pages are a manual gate | done for tier 1 |
| T-12 | Tampered Chrome for Testing download | T | HTTPS, size, SHA-256 recorded on first download, own manifest. CfT has no published checksum | planned |
| T-13 | Tampered app update | T | Signed releases. Wails updater. Hosting open | planned |
| T-14 | Update server or signing key stolen | S, T | Key on a separate machine, rotation plan | open |
| T-15 | Poisoned Go or npm dependency | T | Lock files (`go.sum`, `bun.lock`). Review on update. [INFERENCE] No `govulncheck` run exists | partly done |
| T-16 | Secrets in logs, ledger, error text or support bundle | I | Redaction layer, test with canary secrets | planned |
| T-17 | Second Windows user reads profile data | I | Folder under the user profile with 0700 on creation. NTFS ACL not set by us | partly done |
| T-18 | Google token stolen from disk | I | Store in Credential Manager. Minimum scope | open (ADR 0016) |
| T-19 | A data row injects `{{` text into a macro value | T | Row values are data, not expressions. `EscapeLiteral` for recorded text. [INFERENCE] Not fuzzed | partly done |
| T-20 | Unsigned binary triggers SmartScreen or is replaced | S, T | Code signing certificate. None yet | open |
| T-21 | Wails WebView2 page loads remote content | E | Serve UI from embedded assets, strict CSP, no remote navigation | planned |
| T-22 | Run hangs a profile and holds a lock | D | Job Object, lockfile probe, timeouts per step | partly done |
| T-23 | Abuse: spam, fraud, ban evasion, account farming | n/a | See the policy risk below. Options in `docs/acceptable-use.md` | open |
| T-24 | Site terms forbid automation or multi-account use | n/a | See below | open |

## Anti-bot and site-terms policy risk

This product is in the same category as GoLogin, AdsPower and Multilogin. Many sites forbid automation or many accounts in their terms. A user can lose an account. The product cannot prevent that.

- Detection is a moving fight. Tier 1 passes sannysoft, the rebrowser checks and BrowserScan today. CreepJS flags audio and client-rect noise. This can change at any site update.
- The injector hides the real device. This is the product feature. It is also what a bad actor wants.
- The app must not promise that a site will not detect it. The installer and About screen must say this plainly.
- No CAPTCHA solving. This is a stated non-goal in the vault.

## Abuse risk

- The same features serve a small business and a fraud farm. We cannot tell them apart from inside the app.
- Built-in guards can raise the cost of bulk abuse. Examples: row rate limit, `allowedOrigins` review, a warning for many profiles on one site. None is built.
- Legal liability is a question for counsel. This file gives none. See `docs/acceptable-use.md`.
- Open: telemetry is off by default (vault stance is open). With no telemetry, we cannot see abuse. [INFERENCE]

## Open work

1. Decide local client authentication for the relay (T-6).
2. Build secrets storage and redaction before the first store or log code ships.
3. Write the import review screen contract (T-1, T-2).
4. Get a code signing certificate and decide update hosting (T-13, T-14, T-20).
5. Run a real proxy test and a managed-PC test (R7, R10).

## Checked against

`core/macro/macro.go`, `validate.go`, `expr.go`, `core/player/player.go`, `vars.go`, `errors.go`, `core/recorder/recorder.go`, `recorder.js`, `convert.go`, `core/driver/stock.go`, `datadir.go`, `launcher.go`, `policy_windows.go`, `job_windows.go`, `core/proxy/relay.go`, `core/profile/types.go`, vault ADRs 0011 to 0016 and `product/risks.md`.
