# Engine candidates (16 Nov 2026)

Need: Chromium engine with CDP, Windows build, per-profile fingerprint seed, redistributable or auto-downloadable, current major. Upstream is 157. Our fingerprint-chromium is 150 (45 days old), with the canvas hang (docs/windows-spikes.md).
Sources: GitHub API, READMEs, LICENSE and NOTICE files, read 16 Nov. "Unverified" means not checked. Not legal advice.

## 1. Verified candidates

| Engine | Patch source open? | Licence / redistribution | CDP | Chromium (age) | Fingerprint controls | Supply-chain notes |
|---|---|---|---|---|---|---|
| **Fury** (furyteamtop) | Yes: 41 patches in `core/patches/`, BSD-3 by choice; CI in repo; build script fetches pinned Chromium | Patches BSD-3. Launcher, agent, server AGPL-3.0. Windows core `.tar.xz`. Widevine not shipped | README says CDP/Playwright; pipe unverified. README says `Runtime.enable` timing cannot be hidden | **157** (v0.5.6, 1 day) | Persona (OS, GPU, seed), per-profile proxy | Created Aug 2026, 6 contributors, 173 stars. Windows build unsigned (README). Pre-release, fast churn |
| **Apostate** (heretic-tech) | Yes: 153 patches in repo, GH Actions builds, per-artifact manifest with sha256 and patch-series hash | GPL-3.0 (engine). Zip download is fine. Bundling a GPL binary needs source-offer compliance: ask counsel | Playwright/Puppeteer docs; flags `--fingerprint=<seed> --fingerprint-platform`. Pipe unverified | 155.0.8059.31 (v0.5.1, 47 days; 2 behind) | Seed, platform persona, `--fingerprint-explain` | Created Sep 2026, 98 stars, 0 listed contributors. Manifest sha256 matched the zip on winpc. Not updated since 2 Oct |
| **Clearcote** (clearcotelabs) | Yes: `patches/`, `build.sh`, `patch-integrity.yml` | BSD-3. A newer 157 build is "licensed" (README); the open build is 151 | Any CDP client via `--fingerprint=<seed>` switches | 151.0.7922.108 (19 days; 6 behind) | `--fingerprint` seed, platform, timezone, brand, geoip. Fonts not per seed | Pre-release (pre.25). 1 contributor. `.sha256` + GPG `.asc` + `SHA256SUMS`; zip hash matched on winpc. GPG not checked |
| **ovobrowser** | **No.** Repo holds docs and examples only; binaries only | Repo MIT covers docs. Binary terms: "Chromium / ungoogled-chromium" only. Redistribution unclear | Plain `chrome.exe --user-data-dir ...` flags | 138/144/150/**155** (20 days) | Many `--fingerprint-*` flags, optional noise seeds for canvas/WebGL/audio/rects/fonts | Closed binaries, 2 contributors, created Aug 2026. SHA256SUMS exists only for v1.0.0, none for 155 |
| **BotBrowser** (botswin) | Mostly no: `patches/` has 4 small diffs | MIT file, but 150+ profiles are subscription or support-gated (README) | `--remote-debugging-port` documented; CDP leak blocking | **157** (v157.0.8103.38, 10 days) | `--bot-profile`, `--bot-noise-seed`, per-context fingerprint | Created 2024. 800 MB Windows 7z. Profiles gated: poor fit |
| **CloakBrowser** | **No.** Only the wrapper is MIT | `BINARY-LICENSE.md`: proprietary. Latest major needs a paid subscription (licence) vs "free key, 1 session" (README): they disagree | Playwright wrapper. Pipe unverified | 156 (13 days). Releases carry only `SHA256SUMS` + `.sig`, no binary | `--fingerprint=<seed>`, 87 patches claimed | 32.9k stars in 9 months, forks 8%, 27 contributors, 167 issues. Inflation **unverified** (stargazer sampling failed). Treat as commercial. Cannot ship or auto-download |
| **ShardBrowser** (ProxyShard) | **No** | Launcher MIT. Engine closed. Licence **forbids** use in a commercial anti-detect product | Local API, CDP | 157 | Engine-level | Excluded by licence |
| **Donut / Wayfern** | Launcher AGPL; Wayfern engine is a separate product | App-level only. Wayfern terms unverified | Local API on 127.0.0.1:10108 then CDP | Unverified | From Wayfern | Not a shippable engine |
| **aliasmode** | Apache-2.0 app; own Firefox engine; Chromium 155 build unverified | Apache-2.0 | `cdp.ts` in repo | beta.74 | Unverified | Beta, 4 contributors |
| **clark-browser** | Yes: ungoogled-chromium fork, MIT patches | MIT patches (repo licence field says NOASSERTION) | Via Playwright/CDP | 148 (Jun 2026, stale) | Unverified | 142 stars; stale |
| Camoufox | Yes | MPL-2.0 | **None**: Firefox, no CDP | Firefox 158 | Good | Fails chromedp design |
Not engines: veilbrowser (a control layer over stock Chrome), obscura (headless Rust), nodriver, patchright, Brave farbling (no per-profile seed).

## 2. What ChatGPT and Gemini said
- **ChatGPT** (answer complete): pick (b) own patchset in CI, use prebuilt forks only to benchmark. Ranked Fury, Clearcote, veilbrowser, "pppi21", ungoogled-chromium. Matches the evidence on Fury and Clearcote. Wrong or weak: veilbrowser is a stock-Chrome runtime, not a patched engine (it ranked it 3rd for fingerprint quality). `pppi21` exists only as a 0-star empty repo: **invented/unusable**. "Fortress" (Chromium 153): no repo found: **unverifiable**. Correct on ShardBrowser's licence ban. Its note that Clearcote 157 is licensed matches the README. Release gate (100+ cold launches) is sound.
- **Gemini** (account hoangvanlong2207): pick (b). It lists "Nstbrowserless": **invented**. `kaliiiiiiiiii/brotector` is real but is a detection tool (last push Dec 2024), not an engine. It calls CloakBrowser "closed binaries from small maintainers": right on closed, wrong on size. It says VirtualBrowser lags by 10+ majors: unverified. It treats Brave and Electron/CEF as candidates: they fail the per-profile seed need. Its claim that the canvas hang comes from a GPU init race is a guess, not evidence.
- Both missed Apostate, ovobrowser's closed status, BotBrowser, and Fury's GPL/AGPL split. Both rely on one fact we have: the hang is ours to reproduce on another engine. No follow-up question was needed.

## 3. Recommendation
1. **Primary: Fury core (157)** if the smoke test passes: newest, patches open under BSD-3, no copyleft on a browser built from them. Check pipe mode, Windows hang rate and stability (v0.5, unsigned, 1-month-old repo).
2. **Fallback: Apostate 155** (open patches, CI builds, hash-verified manifest) or **Clearcote 151** (BSD-3, GPG-signed, but 6 majors behind). Apostate is GPL: bundling needs counsel; downloading at runtime and invoking it is the lower-risk path (flag, not advice).
3. **Avoid**: CloakBrowser, ShardBrowser, BotBrowser (paid or licence-barred for a commercial manager); ovobrowser (closed, no checksums for 155, unclear terms); Camoufox (no CDP). Keep fingerprint-chromium 150 only until a replacement passes.

## 4. Build ourselves (option b)
- Base: ungoogled-chromium-windows CI (GitHub Actions, large Windows runner, 6+ h build, sccache); pin one Chromium tag per release.
- Patch sets to start from: Fury `core/patches/` (BSD-3) or Apostate's series (GPL-3.0, would make our build GPL). Clearcote's patches are BSD-3 and fit the 151 base.
- Needs: patch rebase every major (about 4 weeks), a Windows canvas/CDP regression gate, signed artifacts and a manifest we verify at download.

## 5. Smoke test status
**Not run.** `.orca/signals/canvasspike.jsonl` has no "done" event, so no browser was launched on winpc. Downloaded into `C:\Users\8BU\engines` and hashed (sha256): clearcote zip (matches SHA256SUMS), apostate zip (matches manifest), ovobrowser 155 zip (no published sum), fury core tar.xz (SHA256SUMS present, comparison not done). Still to do: 20 launches each, `Browser.getVersion`, canvas hang rate, seed difference.

## 6. Open questions for the user
- Is AGPL (Fury app) acceptable for our code, or do we only touch Fury's BSD-3 patches? Can we ship or auto-download a GPL-3.0 Apostate build? Needs counsel.
- Will we pay for a licensed engine (CloakBrowser, Clearcote 157, BotBrowser) if open builds fall short?
- Is the Fury 157 persona model coherent with our per-profile fingerprint model, or do we want our own patches?
