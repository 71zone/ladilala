# M0 engine matrix: tier 1 against fingerprint-chromium 148 and 150

Date: 2026-11-17. Machine: winpc (Windows 11, GTX 1650 SUPER, 2048x1152 at 125 percent, console session, headed).
Tags: [MEASURED] run on winpc today. [SOURCE] page or API read today. [INFERENCE] estimate, not tested.
Related: `docs/tier3-engine-plan.md` (M0), `docs/stock-engine.md`, `docs/canvas-hang.md`, ADR 0006.
Harness: `cmd/winspike` modes `m0detect`, `m0reliab`, `m0fit` (new), `canvasmatrix`. Raw logs are on winpc (section 11).

## 1. Short answer

- fingerprint-chromium (FPC) 148 launches and drives well: 0 hangs and 0 crashes in 290 launches. It fixes what tier 1 cannot fix: engine-level canvas, audio and rect noise, GPU strings, WebRTC policy.
- FPC 148 also has bugs that a page can see: `measureText` returns 0.00015 instead of 246.98, `deviceMemory` is 16, `colorDepth` is 32.
- FPC 150, the newest build, hangs or crashes on the first canvas read in 200 of 200 launches. Do not use it.
- Upstream Chrome stable is 158. FPC 148 is 10 majors behind. The gap grows about 1 major every 2 weeks.

## 2. Inventory [SOURCE: GitHub API and Chromium Dash, read 2026-11-17]

| Tag | FPC release | Chromium stable (first build) | Lag | Windows zip (bytes) | SHA-256 of zip |
|---|---|---|---|---|---|
| 150.0.7871.186 | 2026-10-02 | 2026-06-17 | 15 weeks | 193,743,241 | 4d549c32...77e03 |
| 148.0.7778.215 | 2026-06-21 | 2026-04-23 | 8.5 weeks | 189,767,686 | 9ef3f471...32579 |
| 144.0.7559.132 | 2026-02-27 | 2026-01-08 | 7 weeks | 176,796,270 | ab409667...0996 |

- No release is newer than 150.0.7871.186. The last commit is the 150 update (2026-09-28). Open issues: 43.
- Each release has an installer (`.exe`, 112 to 123 MB) and a zip. The GitHub API gives a SHA-256 `digest` for each asset since 2026. The release text has no hash.
- The Windows 148 and 150 zips were downloaded and hashed on winpc. The hashes match the API values [MEASURED]. Unpacked size: 425 MB (148), 433 MB (150).
- Licence: BSD-3-Clause, `LICENSE` file copies the ungoogled-chromium text [SOURCE]. One author (14 commits).
- Source: public only for tags up to 148. Source of a release is published when the next one ships [SOURCE, README].
- Stable Chrome now: 158 (2026-11-05). Chrome ships a major every 13 to 26 days in 2026. FPC ships every 2 to 4 months [SOURCE].

## 3. Method [MEASURED]

- Four engines, same machine, headed, new profile per engine, seed 7, Windows profile, timezone America/New_York (the FPC flag and the tier 1 emulation):
  - A: stock Chrome 157.0.8103.52, no flags, our pipe launcher only (control).
  - B: tier 1 (stock Chrome, `core/inject`, CDP emulation), same Chrome.
  - C: FPC 148.0.7778.215 with its own flags (`FingerprintChromium.Args`: seed, platform 13.0.0, brand Chrome 148, 8 threads, timezone, no WebRTC UDP). No injector.
  - D: FPC 150.0.7871.186, same flags.
- Driver for A to D: own CDP over `--remote-debugging-pipe`, no `Runtime.enable` (`LaunchStockBare` or `LaunchStockUnverified`). Reliability also ran on the old chromedp path (`Launcher.Launch`).
- Page text, key values and screenshots (`docs/img/m0-<engine>-<page>.png`, each under 300 KB, not committed) were taken after the page was stable.
- Page, srcdoc iframe and dedicated worker were compared by a probe served from 127.0.0.1.
- Second pass with timezone Asia/Saigon (the real one) for B and C on CreepJS, pixelscan and BrowserScan: same verdicts.

## 4. Detector results

Verdict: ok, flag (yellow or red) and the exact item. A is the control.

| Page | A bare Chrome 157 | B tier 1 | C FPC 148 | D FPC 150 |
|---|---|---|---|---|
| sannysoft | flag: WebDriver "present (failed)" | 0 failed | 0 failed | page hangs, no text |
| rebrowser (20 checks) | flag: navigatorWebdriver | ok. runtimeEnableLeak ok | ok. runtimeEnableLeak ok | ok |
| CreepJS headless / stealth | 33% / 0% | 0% / 0% | 0% / 0% | page hangs |
| CreepJS lies | none | flag: Audio "sample noise" (1 lie) | none. textMetrics 6e-9 (should be 0.01) | page hangs |
| CreepJS WebRTC | srflx IP shown | srflx IP shown | "blocked", no candidates | page hangs |
| BrowserScan bot detection | flag: Robot (WebDriver) | Normal | Normal | Normal |
| BrowserScan fingerprint | 85%: time zone -10%, Bot Control -5% | 85%: time zone -10%, Canvas Tampering -5% | 90%: time zone -10% | page hangs |
| pixelscan fingerprint | "No masking detected" | "Masking detected" (inconsistent) | "Masking detected" (inconsistent) | page hangs |
| pixelscan bot check | "Bot Behavior Detected" (Webdriver) | "Definitely a Human" | "Definitely a Human" | not read (no screenshot) |
| browserleaks canvas signature | common (233 users) | unique | unique | page hangs |
| browserleaks rects | hash F8C6... | same as A (no noise seen) | C8AB... (noise) | ok |
| browserleaks WebRTC | public IP shown (no proxy, so no leak) | same as A | no address | no address |
| Cloudflare demo (nopecha, rucaptcha) | interactive checkbox | same | same | widget crashed ("sad face") on nopecha |
| Turnstile demo (nopecha) | checkbox | checkbox | checkbox | checkbox |
| Google sign-in page | loads | loads | loads | loads |

- BrowserScan "time zone -10%" appears for A too. It compares `Asia/Saigon` with `Asia/Ho_Chi_Minh`. Ignore it.
- A fails WebDriver because `--remote-debugging-pipe` sets `navigator.webdriver` without `--disable-blink-features=AutomationControlled`. B adds that flag. C and D hide it in the engine.
- pixelscan flags B and C in the same way. It does not tell them apart [MEASURED, also with matching timezone].
- No CAPTCHA was solved. No engine passed a challenge without a click.

## 5. Key values [MEASURED]

| Value | A | B | C | D |
|---|---|---|---|---|
| UA major | 157 | 157 | 148 | 150 (not read, page hangs) |
| UA-CH brands | Not-A.Brand 99, Chrome 157, Chromium 157 | Chromium, Chrome 157, Not.A/Brand 99 | Chromium 148, Chrome 148, Not/A)Brand 99 | n/a |
| platformVersion | 19.0.0 | 13.0.0 | 13.0.0 | n/a |
| GPU renderer | GTX 1650 SUPER | AMD Radeon (injected) | Intel Iris Xe (engine) | n/a |
| hardwareConcurrency / deviceMemory | 12 / 32 | 8 / 8 | 8 / 16 (value Chrome never reports) | n/a |
| screen / colorDepth | 2048x1152 / 24 | 1920x1080 / 24 | 2048x1152 (real) / 32 | n/a |
| canvas hash (220x60 text) | 3a683465 | 363a5876 | 518d036c | n/a |
| WebGL readPixels hash | c5511555 | 9761a01c | eb34d295 | n/a |
| audio sum | 172.671648761643 | 172.671648910655 | 172.672697595437 | n/a |
| rect (rotated div, x of range) | 11.5625010 | 11.5625010 | 11.5630379 | n/a |
| font probe (50 names) | 41 | 41 | 40 | n/a |
| `measureText` width (18px Arial) | 246.98 | 246.98 | 0.00015 | n/a |
| WebRTC ICE candidates | host(mdns), srflx | host(mdns), srflx | 0 | n/a |
| speech voices (CreepJS) | 3 local, 19 remote | same | 3 local, 0 remote | n/a |
| page vs iframe vs worker (UA, platform, hardware, GPU, canvas, WebGL pixels, language) | equal | equal | equal | n/a |

- C changes the audio, rect and canvas output in the engine, so CreepJS shows no lie there. B changes them in JavaScript and CreepJS flags the audio.
- C does not set the screen size. Screen stays real. B sets it.
- C reports colorDepth 32. Chrome on Windows reports 24 (A and B). A page can test this in one line [INFERENCE: no real Windows Chrome reports 32].
- Issue #96 (zero `measureText`) says Linux builds. It also happens on the Windows build. Issue #100 (deviceMemory 16 or 32) is true for seed 7 [SOURCE, MEASURED].
- Issue #88 claims an infinite Cloudflare loop on 148 with GPU spoofing. It did not appear on two Cloudflare pages in 40 s [MEASURED, one run each].

## 6. Reliability [MEASURED]

Protocol: new data dir and new seed for each launch. Read after navigation (0 s). Then three reads at 1 s or later. Each read does canvas `getImageData`, `toDataURL` and WebGL `readPixels`. A read that does not answer in 3 s is a hang. `canvasmatrix` ran the older protocol (`toDataURL` only).

| Build and path | Launches | Hang | Crash event | First read differs from later | Later reads unstable | Distinct hashes | Launch to CDP answer (median / max) |
|---|---|---|---|---|---|---|---|
| 148 chromedp pipe | 100 | 0 | 0 | 0 | 0 | 100 | 283 / 442 ms |
| 148 own CDP pipe | 100 | 0 | 0 | 0 | 0 | 100 | 257 / 406 ms |
| 148 `canvasmatrix` (baseline, initial tab, wait 1 s) | 30 each | 0 | n/a | 0 | 0 | n/a | n/a |
| 150 chromedp pipe | 100 | 100 (at 0 s) | no event hook | n/a | n/a | 0 | 288 / 336 ms |
| 150 own CDP pipe | 100 | 100 (at 0 s) | 100 (`Inspector.targetCrashed`) | n/a | n/a | 0 | 252 / 349 ms |

- 148: 0 of 290 launches had a hang. Rule of three: the true rate is under 1.1 percent at 95 percent confidence [INFERENCE]. Same seed gave the same hash on both paths.
- The first-read leak in `docs/canvas-hang.md` (2 to 7 of 20) did not appear: 0 of 290 launches (200 read after page load, 90 in `canvasmatrix` with initial-tab and no-wait reads).
- 150 fails on every launch in this protocol: renderer crash on pixel read-back (issues #94, #95, #104). The Cloudflare widget also crashed on 150 [MEASURED].
- Peak memory of one 148 instance on a blank page: 350 MB working set, 228 MB private, 6 processes (166 samples, 150 ms apart). Stock Chrome was not measured the same way.
- CDP pipe: works. The 148 process command line has `--remote-debugging-pipe` and `--remote-debugging-io-pipes=...` and no `--remote-debugging-port`. The driver does not need a port. No local port means no port scan can find the browser [INFERENCE: not tested against a scanner].

## 7. Coherence

- The claimed UA equals the real engine (148). UA, UA-CH and the features are consistent. GREASE brand order follows Chromium 148 code [INFERENCE].
- Chrome 148 is 10 majors and about 7 months old on the date of this test (stable 2026-04-23, stable now 158) [SOURCE]. Tier 1 reports the real current Chrome (157, one major behind 158).
- rebrowser `useragent` check passes for 148 and 150. No page in the matrix showed "update your browser". Google sign-in page loaded.
- A site can use the age. A rule such as "block Chrome older than N majors" would hit FPC today. No tested site did [INFERENCE for the rule].
- The age grows. At 1 major per 2 to 3 weeks and 1 FPC release per 2 to 4 months, a new FPC build is 8 or more majors old on its release day [INFERENCE from the dates in section 2].

## 8. Fit with Ladilala [MEASURED, FPC 148 unless noted]

| Need | Result |
|---|---|
| `--load-extension` (per-profile extensions) | Works. MAIN-world MV3 content script ran in the page. |
| `Extensions.loadUnpacked` over the pipe | Answers in 0.3 s with no error. It returns an empty id. Effect not checked. |
| Proxy relay (`core/proxy` in front of an HTTP upstream with Basic auth) | Works. Page loaded through the relay, upstream saw 1 authenticated request. Without a relay address the launcher refuses (`ErrProxyRequired`). |
| Profile data dir | Standard Chromium layout. Cookie and `localStorage` survive a relaunch. Second launch on an open dir is refused. Same seed gives the same canvas hash after relaunch. |
| Recorder (overlay, CDP input) | 9 of 9 `TestE2ERecord*` pass. Same 9 pass on stock Chrome 157. The 3 "SEEN" items of the detectability test are the same on both. |
| Player | 7 of 7 `TestE2EPlayer*` pass. Same 7 pass on stock Chrome 157. |
| Run engine (`core/engine` E2E) | Not run. It builds its own stock launcher. |
| Injector | Not needed. Turn it off: it would add noise on top of the engine noise [INFERENCE]. |
| CDP mouse `screenX - clientX` (issue #110) | 0 on A, B and C. The window sat at screen x 0, so the test cannot show the tell [MEASURED, weak]. |

- Test seam: `rectest.Launch(..., "fpc", ...)` starts the build in `LADILALA_FPC_DIR` with `LaunchStockBare`. It exists for this run only.
- Auto-attach with `waitForDebuggerOnStart` works on 148. Page, iframe and worker pause and resume without a problem.

## 9. Update path

- Asset URL pattern: `https://github.com/adryfish/fingerprint-chromium/releases/download/<tag>/ungoogled-chromium_<tag>-1.1_windows_x64.zip` [SOURCE]. List: `GET /repos/adryfish/fingerprint-chromium/releases`. Each asset has a `digest` field (`sha256:...`).
- Our app would read the API, check the digest, unpack to its own folder (no installer, no admin), and pin the tag in its own signed manifest (as for Chrome for Testing in `docs/stock-engine.md`). A mirror on our storage removes the GitHub dependency [INFERENCE].
- Cadence: 5 releases in 13 months, 2 to 4 months apart. One maintainer. "No technical support" is in the README. 43 open issues, at least 8 of them open canvas or WebGL bugs (#87, #94, #95, #96, #98, #99, #102, #104).
- Risk, in plain words:
  - Age: high and growing (section 7).
  - Single maintainer: high. A stop of releases freezes the engine at 148 or 150.
  - Quality: medium. 148 is stable but has the visible bugs in section 5. 150 is broken. The next release may break again; the 150 patch source is not public.
  - Licence: low. BSD-3-Clause. Patch source for 148 is public, so a fork is possible [SOURCE].
  - Security patches: the 148 build lacks the fixes of 10 Chromium majors [INFERENCE]. This is a risk for users who browse the open web with it.
- Size: 190 MB download, 425 MB on disk, per engine version.

## 10. Recommendation

1. FPC 148 is usable as tier 2 now for per-profile canvas, audio, rect, GPU and WebRTC noise, but only pinned at 148, with the injector off and with our own launch check (canvas, `measureText`, `deviceMemory`, `colorDepth`); the newest build, 150, is not usable.
2. Tier 1 beats FPC 148 on version (current Chrome with real security fixes), on a coherent real screen and colour depth, and on having no `measureText` bug or single maintainer; FPC 148 beats tier 1 on engine-level noise (no CreepJS audio lie, no BrowserScan "Canvas Tampering"), GPU strings, WebRTC policy and hidden `webdriver` without a launch flag.
3. Building our own patched Chromium is still needed for a tier that stays current, because FPC lags 8 to 10 majors and ships every 2 to 4 months; FPC 148 is a bridge, and its public 148 patches are the design reference for M1.

## 11. Not measured

- Windows `chrome://gpu` log of FPC 148 (issue #88). Cloudflare behaviour past the first challenge page.
- FPC 150 key values, CreepJS and fingerprint pages (they hang), and the extension, proxy and recorder fit of 150.
- Stock Chrome peak RAM. Many parallel instances. Long sessions. Audio, font and rect spoofing under a real proxy IP.
- Tests with the engine run loop (`core/engine`). Whether a real site rejects Chrome 148 by age.
- Machine state left on winpc: `%USERPROFILE%\ladilala-release\m0` holds `fpc148`, `fpc150` (unpacked), the harness `.exe` files, logs (`det*.log`, `rel*.jsonl`, `out*`). Zips and temp data dirs are deleted.
