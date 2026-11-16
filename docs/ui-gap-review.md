# UI prototype gap review (16 Nov 2026)
Reviewed http://192.168.1.34:5190/ at 1280x800 (and 1000 px width) in a headless browser: every screen, tab, sheet and dialog was opened. 10 screenshots used. Mock data only. Not checked: light mode contrast, real drag and drop (the mapping view was read, not dragged), keyboard-only flow.

## 1. Coverage matrix (yes / partial / no)
| Area | Present | Where | Gap note |
|---|---|---|---|
| Record | partial | Macro editor, Record | Toggles a "Recording in profile X" banner. No live step capture, no pick-profile or start-URL dialog |
| Edit steps | partial | Macro editor Steps | Table is read-only: no add, delete, reorder, inline edit. Only the commit-point toggle works |
| Replay | partial | Runs | Start Run animates 3 rows. No test on one row, no dry run, no step speed |
| Pause/resume | partial | Runs row | Icon-only pause button, no label or tooltip seen. Pause of whole run missing (only Stop All) |
| Manual takeover | partial | Runs row | Takeover / Hand back works. "Hand back" label clipped to "...". No CAPTCHA or 2FA prompt flow |
| Recover after crash | partial | Runs Uncertain queue | Good: Mark submitted / Retry / Skip. No auto-restart of a crashed profile, no resume-from-checkpoint |
| Retries, checkpoints | no | | Schema has retries and onFail; UI shows neither. No checkpoint or resume point view |
| Duplicate-submit protection | partial | Editor guard "ledger: not already submitted" | Text only. No ledger view, no duplicate rules |
| Visual mapping | yes | Macro, Mapping and data | Badges, bind, unbind, preview row. Single page only, no multi-page or iframe status, no unbound-field warning at run |
| Data: CSV, Excel, JSON, REST, Sheets | partial | Mapping and data | Tabs show one canned source each. No file picker, no auth flow, no sheet or column choice, no refresh |
| Schema validation | partial | Mapping and data | Row warnings listed. No schema editor, no types or required flags, no "fix rows" or exclude |
| Field mapping | partial | Mapping and data | Badge names equal columns. No column rename, transform or default UI |
| Batch execution | partial | Runs | Round-robin note, concurrency slider. Cap of 10 profiles in the picker, no row range, no progress per row table |
| Scheduling | no | | None |
| Profiles CRUD | partial | Profiles, editor sheet | Create and edit. No delete, clone, archive |
| Groups and tags | partial | Profiles filters, editor | Filter and edit only. No group management, no color |
| Bulk actions | partial | Profiles (select) | Only Start and Stop. No delete, move to group, tag, assign proxy, assign macro |
| Proxies | partial | Proxies | List, import textarea, remove. No edit, no assign, no groups |
| Proxy health | partial | Proxies | Health Check All with status and latency. No history, no auto-recheck, no leak or DNS test |
| Fingerprint editor | yes | Editor, Fingerprint | Preset, seed, UA, screen, GPU, noise modes. No presets library, no per-field lock |
| Consistency check | yes | Editor, Checks | 9 checks, warnings do not block. No "fix for me" button |
| Cookies / session import-export | partial | Editor, Cookies | Count plus Import, Export, Clear. No format choice (JSON, Netscape), no domain filter |
| Extensions per profile | no | | Needed (CfT is the only channel that loads extensions) |
| Engines and updates | yes | Engines | List, update flow, pin, channel, roll back. Conflicts with vault, see part 4 |
| Doctor | yes | Engines, Doctor | Diagnostics, Test launch, common fixes. Layout cramped (part 3) |
| App updates | yes | Settings, What's new | Channels, skip, roll back. Release notes sheet is empty |
| Credentials | partial | Settings Secrets | Text only. No list, no change, no export warning |
| Logs and audit trail | partial | Runs log, Engines log | Log pane shows 1 line. No filter, export, per-row history, no audit of who did what |
| Notifications | no | | No toasts for done, uncertain, proxy down, update ready |
| Import/export of macros, profiles | partial | Profile detail Import/Export is cookies only | No macro import/export, no profile export |
| Backup, restore, data isolation | no | Settings | Data folder only. No backup, no per-profile disk usage or path, no clear data |
| Onboarding / first run | no | | No welcome, no engine setup, no first-profile wizard |
| Empty states | no | | Not seen: no empty list, no first macro, no no-results text |
| Error and loading states | partial | Simulate menu, Engines | Good for engine and app update. None for profile launch, proxy or data errors, no list skeletons |
| Keyboard shortcuts | partial | Ctrl+K palette | Palette lists profiles but no macros, proxies or run actions. No other shortcuts |
| Accessibility | partial | | Checkbox labels exist. Icon-only buttons unnamed (Pause). Focus ring not visible on nav links |
| Localization (vi, en) | no | | English only. No language setting. Vietnamese text not tested for fit |
| Licensing, telemetry consent, crash reporter, help, team sharing | no | | None (team sharing is P2) |

## 2. Missing features

### P0 (blocks the product promise)
- Editable step list (add, delete, reorder, edit action, value, locators, onFail, retries, timeout). Why: a macro tool whose steps cannot be edited cannot ship. Screen: Macro editor.
- Real Record flow with start URL and profile pick, steps appearing live, stop and review. Screen: Macro editor.
- Data source setup: file picker, sheet choice, REST URL and auth, Sheets OAuth, schema (types, required), column to variable mapping, refresh. Screen: Mapping and data.
- Run setup: row range, test on one row, dry run (no commit), confirm before first commit step. Screen: Runs.
- Per-row ledger view: done, failed, skipped, uncertain per row, filter, retry failed, export. Why: 1000-row batches need it. Screen: Runs.
- Pause and resume the whole run, resume after app restart from the ledger. Screen: Runs.
- Profile delete and clone, bulk assign proxy, bulk move to group and tag. Why: 50 profiles. Screen: Profiles.
- Proxy assign per profile from Proxies, edit proxy, auto-assign pool to profiles, relay fail-closed status per profile. Screen: Proxies, Profiles.
- Empty, loading and error states for every list and run. Screen: all.
- Engine strategy rework to match the vault tiers (part 4). Screen: Engines.

### P1 (expected by users)
- Profile templates and "new profile from template" with random-but-coherent shuffle. Why: speed. Screen: Profiles.
- Profile status and notes in the list (last used, last IP, health badge, running since). Screen: Profiles.
- Proxy rotation rules, provider import (host:port:user:pass, URL, API), geo target, sticky session. Screen: Proxies.
- Proxy leak test (WebRTC, DNS, timezone vs IP) in the editor. Screen: Profile editor.
- Run history list with per-run screenshots on failure and replay of the log. Screen: Runs.
- Step debugger: run to step, breakpoints, variable inspector, step over. Screen: Macro editor.
- Selector healing UI: "step failed, re-pick this field" with the overlay, show which locator won. Screen: Runs, Macro editor.
- Macro variables panel (names, types, defaults, required) and allowedOrigins editor. Why: both are required by the schema. Screen: Macro editor.
- Human-like timing and rate limits per macro or per profile (delay range, max rows per hour). Screen: Runs, Macro.
- Duplicate-detection rules (which column is the key) and a "submitted before" warning. Screen: Runs.
- CAPTCHA and 2FA handoff: pause that row, notify, wait for the user, continue. Screen: Runs.
- Notifications (toasts and Windows toast) for done, uncertain, proxy down, update ready. Screen: global.
- Macro and profile import and export (encrypted file for profiles, no secrets in plain text). Screen: Macros, Profiles.
- Backup and restore of the data folder, with schedule. Screen: Settings.
- Language setting (English, Vietnamese). Screen: Settings.

### P2 (later)
- Run scheduling with calendar and recurring rules. Screen: Runs.
- Macro versioning with diff, extensions per profile, session warm-up, licensing, telemetry consent, crash reporter, per-profile disk usage, fingerprint test launcher, webhooks, team sharing and roles. Screens: Macros, Profile editor, Settings.

## 3. UI quality bugs and polish
- Macros: New Macro does nothing (list stays at 4). Run buttons open the editor, not a run; in the editor Run gives no feedback and Record adds no steps.
- Macro editor: step values use `{first_name}`, the schema uses `{{first_name}}` with pipes. Mock locators are shown as raw CSS text.
- Macro mapping: the mock page URL is /checkout while the macro's first step navigates to /signup. Row 14 warning shows but only 4 rows are visible, no way to scroll to it.
- Runs: shows 3 running rows before pressing Start Run (stale state), and the page header says "0 of 412 rows done" with no progress ever advancing.
- Runs: profile picker is hard-limited to 10 of 24 profiles, no search, no select all.
- Runs: Concurrency 3 but 6 profiles are ticked, the relation is unexplained. Log shows a single line during a run.
- Runs: "Hand back" button is clipped to "..." in the takeover state. Pause is an icon-only button with no name.
- Engines, Doctor: the Common problems list is squeezed into a narrow right column, titles wrap to 3 lines. Fix buttons wrap awkwardly.
- Engines: after a successful update the row still shows the old path `...\150.0.7871.186\chrome.exe`. After a reload the update is gone (mock state resets).
- Engines and Profiles at 1000 px: install paths, UA and summary are cut with no tooltip or copy button. Settings: data folder path shows doubled backslashes (known). Dark mode switch duplicates the sidebar toggle. What's new sheet has no release notes.
- Profile editor: the sheet covers only half the window and hides the detail it edits. Notes textarea has a different fill and contrast from the inputs. Tags are plain text, not chips.
- Profile editor: no dirty marker, Escape closes the sheet without an unsaved-changes warning.
- Profiles: list shows country codes that do not match proxy (Shopee VN 1 is JP, eBay UK 02 is FR). Bulk bar has only Start and Stop. No right-click menu.
- Proxies: bulk import textarea imports immediately and silently; the "bad line here" row from the placeholder gives no error and the pool count changes 18 to 20 without a message.
- Accessibility: nav links show no focus outline. Icon buttons lack names.

## 4. Conflicts with the vault or requirements
- Engines: the UI makes fingerprint-chromium the "default spoofing engine" and tells the user to update it to 157. The user rejected fingerprint-chromium and its latest release is 150 (148 or 144 are the safe pins). The vault proposal is three tiers: stock Chrome and Edge (tier 1, our injector), Chrome for Testing (tier 2), own build (tier 3, optional).
- Engines: System Chrome, Edge and Chrome for Testing are labelled "no spoofing". In the proposal they are the primary engines with injector-based spoofing, so the label should be "injected fingerprint" and a "patched engine" tier should be the optional one.
- Engines: the "7 versions behind" alarm only makes sense for tier 3 or a bundled engine. For stock Chrome the vendor auto-updates.
- Macro editor shows raw locators (`#first_name`, `iframe#pay >> #cc`). Requirement: no selectors written by the user. Show a friendly target label, hide locators under "Advanced".
- Macro editor lacks `allowedOrigins`, variable declarations, guards list, retries and onFail, which `docs/macro-schema.md` makes part of every macro. Actions `check`, `uploadFile`, `press`, `scroll`, `assert`, `manual` are not visible in the mock.
- REST and Sheets data sources are shown ready. Vault R6 (Sheets scope verification) is open, so mark Sheets as "needs setup" until answered.
- Fingerprint tab shows 6 noise toggles per profile. Vault rule 2 says no attribute is random on its own: one preset from a seed. The UI lets each attribute be switched off or real, which can create an inconsistent profile. Keep it under "Advanced" with a warning.
- Profile editor has no engine choice per profile although tiers exist. Roll back and Pin Versions exist for engines, but the vault says a canvas hang was fixed only by pinning 148 or 144. The Pin Versions page should show known-bad versions.
- No Windows Job Object or relay status is shown (vault rules 3 and 4).

## 5. Ten highest-value additions for the next prototype round
1. Editable step table with add, delete, reorder and a step side panel (locators under Advanced).
2. Record flow: profile and URL dialog, live steps, stop and review.
3. Data source wizard: pick file or URL, sheet, schema types, column map, validation report with "exclude bad rows".
4. Run setup upgrade: row range, test one row, dry run, run summary before start.
5. Run ledger table (per row status, filter, retry failed, export) plus run history with failure screenshots.
6. Profiles bulk toolbar and context menu: clone, delete, move group, tag, assign proxy, assign macro.
7. Proxy assign and rotation rules, provider import with a validation preview.
8. Engines rework to the three-tier model, drop fingerprint-chromium as default, show injector spoofing status.
9. Empty, loading and error states plus toasts for every list and long action.
10. First-run onboarding (engine check, first profile, first macro) and a Vietnamese and English language setting.
