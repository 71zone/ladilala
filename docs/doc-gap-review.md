# Doc gap review (worker docaudit, 16 Nov 2026)

Audit and proposal only. Nothing else was edited. V = vault dir NoteBook/WinMacro, R = repo. "WM" = V/WinMacro.md, "FA" = V/Full Assessment.md.
Dates are file mtimes. The repo has no commits yet, so there is no git history to date anything.

## 1 Inventory

| Path | Purpose | mtime | Lines | Status |
|---|---|---|---|---|
| V/WinMacro.md (WM) | Monolith: stack, architecture, UI plan, R1-R12, actions, risks, engines, naming, UI review | 11-16 | 263 | current in parts; sections 1, 3, 6, 9 stale (see 2) |
| V/Full Assessment.md (FA) | Pre-scope-change assessment: stack comparison, edge cases, MVP | 11-15 | 234 | superseded (macro tool only, chromedp, no profiles) |
| R/docs/macro-schema.md | Macro JSON v1 | 11-16 | 81 | current |
| R/docs/stock-engine.md | Tier 1 build and Windows results | 11-16 | 53 | current (newest engine truth) |
| R/docs/canvas-hang.md | fingerprint-chromium hang matrix | 11-16 | 52 | current but about a rejected engine |
| R/docs/windows-spikes.md | R3, R4, R8 results | 11-16 | 27 | partly stale (R8 engine rejected) |
| R/docs/engine-candidates.md | Round 1 engine table (Fury, Apostate, Clearcote) | 11-16 | 44 | superseded by engine-research-2 and WM s11 |
| R/docs/engine-research-2/{mature,build,stock}.md | Round 2 research | 11-16 | 57/67/61 | current evidence; stock.md Mac-only, Windows in stock-engine.md |
| R/docs/i18n-glossary.md | Key naming, VI terms | 11-16 | 48 | current, user review pending |
| R/docs/naming.md, naming-vn.md | Name rounds 1 and 2 | 11-16 | 78/91 | current, no decision, no legal clearance |
| R/docs/ui-gap-review.md | Prototype gap list | 11-16 | 119 | current, partly contradicts prototype by design |
| R/ui/PROTOTYPE.md | How to run the prototype | 11-16 | 28 | stale ("rev 1 to 5", WM says rev 7) |
| R/ui/STYLING.md | shadcn-vue and Tailwind rules | 11-16 | 17 | current; the only design-system doc |
| R/.orca/playbooks/winmacro-task.md | Worker spawn recipe | 11-16 | 52 | current; no eval rule text seen by me beyond the brief |
| R/.orca/briefs/*.md (24), log.md, state.json | Worker briefs and orchestration state | 11-16 | n/a | process records, not product docs |
| R/core/inject/NOTICE | Apache-2.0 attribution (fingerprint-suite) | 11-16 | n/a | current; only licence file in the repo |

No README.md, LICENSE, CHANGELOG or AGENTS.md exists in the repo root.

## 2 Contradictions and stale statements

| # | File, section | Current claim | Correct statement | Source |
|---|---|---|---|---|
| 1 | WM s1 row "Browser control" | chromedp + cdproto is the driver | Tier 1 and 2 use our own raw CDP client; chromedp sends Runtime.enable | WM s11 bullet 2; core/driver/rawcdp.go; docs/stock-engine.md |
| 2 | WM s1 row "Browser engine" | fingerprint-chromium is default, Chrome/Edge fallback | Three tiers; stock Chrome then Edge first; fingerprint-chromium rejected | WM s11 table and "Rejected for good" |
| 3 | WM s3 diagram | `BrowserDriver (chromedp)`, "Engine manager" as a component | Raw CDP over pipe; engine manager (CfT lookup) lives in core/driver/cft.go, core/engine is empty | core/driver/cft.go; ls core/engine |
| 4 | WM s3 vs repo | Architecture implies core/engine and core/data hold code | Both dirs are empty. Item 1a marks them done as folders only | ls core/engine core/data |
| 5 | go.mod, core/driver/canvasprobe.go, emulation.go | chromedp still a dependency | Still used by the legacy fingerprint-chromium path and EmulationPlan types. Needs a decision: drop or keep cdproto only | grep chromedp core |
| 6 | WM s6 item 2 | "Windows run still open" while ticked [x] | R3, R4, R8 PASS on winpc; stock E2E passes on winpc | WM s5 R3, R4, R8; docs/windows-spikes.md |
| 7 | WM s5 R8 and s8 risk 4 | canvas hang "about 40 %" | docs/windows-spikes.md says 40 %; docs/canvas-hang.md and WM s10 say about 50 %; 148 and 144 give 0 | docs/canvas-hang.md |
| 8 | WM s5 R10, R11, R9 | Open, no result | R10 relay tests pass with fake upstreams (item 3); R11 largely answered by rebrowser and sannysoft runs; R9 partly (CreepJS lies on audio and client rects) | WM item 3; docs/stock-engine.md |
| 9 | WM s5 R5, R12 | R5 open, R12 open | R5 only technically answered (CfT runs from a user folder); legal terms still unread. R12 effort estimated in build.md, not tested | docs/stock-engine.md; engine-research-2/build.md |
| 10 | WM s9 (Engine candidates) | Ranks Fury 1, Apostate 2, Clearcote 3; asks AGPL/GPL questions | All three rejected by the user; questions moot. Section and docs/engine-candidates.md are superseded | WM s10, s11 |
| 11 | WM s10 last bullet | "stock.md still in progress", "Still to come" | stock.md is in; s11 used it | engine-research-2/stock.md |
| 12 | WM s11 intro vs bullet | "Not yet approved by the user" then "Approved by the user" | Tier 1 approved; tiers 2 and 3 approval not recorded | WM s11 lines 182 and 198 |
| 13 | WM s6 actions | No item for tier 1 stock engine, tier 2 CfT, worker auto-attach | Built and measured (core/inject, rawcdp.go, stock.go, cft.go). Item list does not match code | docs/stock-engine.md |
| 14 | WM s4 Engines row, ui/src/engines.ts lines 22 and 156 | fingerprint-chromium 150.0.7871.186 shown as the default engine | Rework to three tiers (open item 13, P0) | WM s13 P0 last bullet |
| 15 | WM s8 risk "Engine security lag (7 majors)" | Mitigation is item 10 | Only applies to tier 3; stock Chrome and Edge are vendor-updated | WM s11 |
| 16 | WM s1 row "Shell" and FA s1 F3 | Wails "v3.0.0 (26 Oct 2026)", pin the version | FA says v3.1.0 shipped 1 Nov with weekly releases; no version is pinned, no Wails code or go.mod entry, app/ is empty | FA F3; go.mod; ls app |
| 17 | WM s1 "Core: Go 1.26" and go.mod 1.26.2 | Go 1.26 | winpc runs Go 1.27.3; toolchain policy undefined | docs/windows-spikes.md |
| 18 | WM s13 "UI polish" and macro editor | Step values use `{first_name}` | Schema uses `{{first_name}}` with pipes. Not rechecked in the prototype | docs/macro-schema.md line 65 |
| 19 | ui/PROTOTYPE.md | "rev 1 to 5"; delete when UI chosen; hard-coded http://192.168.1.34:5190 | WM says rev 7 with update channel; UI kit and style are decided so "throwaway" is half true; LAN IP is machine specific | WM s4 |
| 20 | WM s6 item 12 vs s12b | Candidate Ovanero, runner-up Tovinel, plus the VN-ease requirement | Round 2 list has 46 names tuned for Vietnamese; round 1 names are "not tuned". Item 12 mixes both. Names in code (module github.com/8bu/winmacro, `winmacro.locale`, `%APPDATA%\WinMacro`) will need a rename plan | WM s12b; docs/i18n-glossary.md |
| 21 | FA (whole file) | chromedp, Electron compared, "Wails or Chrome app-mode", single-profile world | Replaced by WM; WM line 14 still points to it as "Full detail" | WM line 14 |
| 22 | WM s8 policy note | "No CAPTCHA solving" | Only one line; no product stance doc | see 3 Legal |
| 23 | WM s14 glossary | Terms "to review" | Review pending; glossary has no recorded owner decision | docs/i18n-glossary.md |

## 3 Missing documents

Status: E exists, P partial, M missing. Priority P0 before next big piece (Wails shell, SQLite, run engine), P1 before first user build, P2 later. Who: A agent can draft, U needs the user.

| Document | Status | Pri | Must contain (max 5) | Reuse | Who |
|---|---|---|---|---|---|
| Product vision, scope, non-goals | P | P0 | one-paragraph vision; in/out of scope; non-goals (CAPTCHA, cloud sync, team); target OS | WM intro, s8 policy | U |
| Personas and use cases | M | P1 | 2-3 personas; top jobs; example macro per persona | FA s7 | A+U |
| Requirements with acceptance | M | P0 | numbered reqs; pass test each; maps to R1-R12 | WM s5, s6, s13 | A |
| Roadmap and release plan | P | P1 | milestones; M0 stock shell; tier 3 gate; beta channel | WM s6 | U |
| Competitor notes | M | P2 | GoLogin, AdsPower, Kameleo features, price, licence stance | engine-research-2/mature.md | A |
| Architecture overview | P | P0 | components, boundaries, owner package; fix diagram (row 3) | WM s3, stock-engine.md | A |
| Data model (SQLite, profile, macro) | P | P0 | tables; ledger columns; profile JSON; migrations | core/profile/types.go, macro-schema.md | A |
| UI-to-core API contract | M | P0 | Wails bindings list; events (run progress, engine update); error shape | ui/src/store.ts mock | A |
| Engine abstraction and tiers | P | P0 | BrowserEngine interface; tier rules; selection; version pin and known-bad list | WM s11, stock-engine.md | A |
| Run engine and ledger state machine | P | P0 | states Pending..Uncertain; commitPoint; crash recovery; resume under 15 s | WM s3 rule 7, s13 | A |
| Proxy relay design | P | P1 | SOCKS5 login; fail closed; DNS; WebRTC; geo from IP | core/proxy, R10 | A |
| Fingerprint model and coherence rules | P | P0 | preset from seed; coherence matrix; per-tier capability table; advanced toggles | core/profile, stock-engine.md | A |
| Update manager design (app and engine) | P | P1 | signed manifest; channels; atomic swap; rollback; hosting | WM item 11, appupdate.ts | A+U |
| Logging and diagnostics bundle | M | P1 | log levels; redaction; support bundle content | Doctor UI in engines.ts | A |
| Performance budgets | M | P2 | launch time; CDP latency (260 to 360 ms seen); memory per profile | stock-engine.md | A |
| Platform and version matrix | M | P1 | Win 10/11; WebView2; Chrome, Edge, CfT majors; managed PC policy | R7, stock-engine.md | A |
| ADR log + research index | M | P0 | one ADR per decision in 4 below; index of engine-research-2 | WM s1, s9-s11, FA s2 | A |
| Threat model | M | P0 | assets; imported macro; local relay; update channel; per-profile isolation | WM s3 rule 8, s8 | A+U |
| Secrets handling | M | P1 | Credential Manager; proxy passwords in SQLite never; export encryption | WM s1 | A |
| Update signing and engine integrity | M | P1 | CfT has no checksum (record first hash); Wails signature; code signing cert | stock-engine.md | U |
| Telemetry and privacy stance | M | P1 | default off; crash reports opt in; data kept local | none | U |
| Acceptable use and abuse position | M | P0 | what product will and will not do; dual use; no CAPTCHA solving | WM s8 | U |
| Test strategy | P | P0 | unit, winpc E2E, detector suite, canvas gate, 1000-row crash kill | docs/windows-spikes.md, canvas-hang.md | A |
| winpc runbook | M | P0 | wake-winbox, ssh port 22, scp tarball, Go path, ssh over herdr for tokens, pitfalls | WM s7, windows-spikes.md method | A |
| CI, build, signing, release, versioning, changelog | M | P1 | pipeline; cert; semver; update server | WM item 11 | A+U |
| Repo layout and conventions, dev setup | M | P0 | no README; core packages; Mac vs Windows; bun and Go commands | ui/PROTOTYPE.md | A |
| Worker (orca) process | P | P0 | brief template, eval rule, ping-back, state.json | .orca/playbooks, briefs/_TEMPLATE.md | A |
| Design system | E | n/a | ui/STYLING.md covers kit and tokens; add layout rules | ui/STYLING.md | A |
| i18n guide and glossary | E | P1 | glossary exists; add UX copy rules and approval | docs/i18n-glossary.md | A+U |
| Error message catalogue | M | P1 | code, VI and EN text, fix action | engines.ts Simulate | A |
| User docs (install, quick start, per screen, FAQ), VI and EN | M | P1 | write after Wails shell | WM s4 | A |
| Licence matrix and NOTICE | P | P0 | apify (Apache-2.0), CfT, Wails, shadcn-vue, Reka UI, Tailwind, AGPL/GPL notes | core/inject/NOTICE | A |
| Name clearance plan | P | P1 | register search classes 9, 42, 35; rename plan in code | docs/naming*.md | U |
| EULA, privacy policy | M | P1 | after name and stance; needs counsel | none | U |

## 4 Proposed vault structure

Rule: the vault holds decisions, plans and the user's reading; the repo `docs/` holds anything a build or test depends on and that must version with code (schemas, engine measurements, glossary, runbook, STYLING). Vault notes link to repo paths and never copy them. An ADR is written in the vault and mirrored as one line in `docs/adr-index.md` only if code cites it.

| Vault path | Purpose | From WM |
|---|---|---|
| WinMacro/00 Index.md | Short STE index: status line, links to everything, open decisions | new |
| 01 Product/Vision and scope.md, Requirements.md, Roadmap.md, Competitors.md | what and why | intro, s6, s8 |
| 02 Architecture/Overview.md, Data model.md, API contract.md, Run engine.md, Fingerprint.md, Update manager.md | how | s1, s3, s11 |
| 03 Decisions/ADR-0001..: Wails, own CDP, three tiers, rejected engines, SQLite, fail-closed relay | why | s1, s2, s9, s11 |
| 04 Research/Index.md, Engine round 1, Engine round 2, Canvas hang, Spikes R1-R12 | evidence | s5, s9, s10, FA |
| 05 Security/Threat model.md, Secrets.md, Integrity.md, Privacy and acceptable use.md | trust | s8 |
| 06 Engineering and Ops/winpc runbook.md, Test strategy.md, CI and release.md, Worker process.md | do | s7 |
| 07 UX/Design system.md, UX copy, UI gap review, Error catalogue | look | s4, s13 |
| 08 User docs/ | VI and EN guides | new |
| 09 Legal/Licences.md, Naming and clearance.md | rights | s12, s12b |
| Log/ | dated changelog, one line per day | s6 status text |
| Archive/Full Assessment.md | superseded | FA |

## 5 First 10 documents, in order

Wave A (parallel, agents, no user input): 1 ADR log + research index (fixes rows 1-3, 10, 11 at source). 2 Repo README and dev setup. 3 winpc runbook. 4 Test strategy (merge canvas gate and detector suite).
Wave B (parallel, after wave A): 5 Architecture overview with corrected diagram and package map. 6 Data model (SQLite schema, ledger). 7 UI-to-core API contract (blocks the Wails shell). 8 Run engine and ledger state machine.
Wave C (needs the user): 9 Acceptable use and product scope (dual use stance, non-goals). 10 Threat model, with the licence matrix drafted by an agent in the same pass.
Reasons: 1 to 4 stop new workers repeating old claims; 5 to 8 are P0 gates for item 5 and 8; 9 and 10 decide what the app may ship.

## 6 What I could not check

- Whether rev 7 changed `{first_name}` or the prototype Engines tab beyond engines.ts lines 22 and 156 (row 18, 14).
- Key counts (732) and i18n parity; I did not run `bun run i18n:check`.
- docs/engine-candidates.md Recommendation and the three research files were read only by header; their detail was not audited line by line.
- Contents of .orca/briefs, log.md, state.json beyond the playbook; no check of the eval rule text in the playbook (memory says it was added).
- Test claims (E2E pass counts, 20 launches) were not re-run; Wails availability and versions were not looked up online.
- Vault files other than the two notes (iCloud folder lists only these two).
