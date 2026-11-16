# Stock engine (tier 1): stock Chrome/Edge + own CDP layer + injector

Measured on winpc (Windows, Chrome 157.0.8103.52, Edge 157.0.4341.60, CfT 157.0.8103.78) and the Mac, 16 Nov 2026. Harness: `cmd/winspike` (`stockdetect`, `stocklaunches`, `cft`); E2E: `LADILALA_E2E=1 go test ./core/driver -run TestE2EStockChrome`.

## Built
- `core/inject`: `injector.js` (go:embed, one script for page, iframe and worker scope), `NewConfig(fp)`/`Script`/`ScriptFor` (deterministic per Fingerprint), `NOTICE` (apify/fingerprint-suite ideas, Apache-2.0). Surfaces: navigator hardwareConcurrency, deviceMemory, platform, language(s) (workers also userAgent/appVersion), screen incl. taskbar delta, WebGL UNMASKED vendor/renderer plus a per-OS parameter table, seeded canvas/OffscreenCanvas noise, audio and client-rect noise, readPixels noise.
- `core/driver/rawcdp.go`: own CDP client over the NUL-framed pipe (`rawConn`, implements `cdp.Session`, so cdproto commands and `EmulationSteps` run on it). `stockpipe_*.go`: pipe handoff (fd 3/4 on Unix, `--remote-debugging-io-pipes` handles on Windows).
- `core/driver/stock.go`, `stock_attach.go`: `StockChrome` engine (SystemChrome detection, Chrome then Edge), `Launcher.LaunchStock` (launch, verify, retry up to 4 launches; Job object kept), `LaunchStockUnverified`, `LaunchStockBare` (control), `StockSession` (`WaitPage`, `NewPage`, `Page.Navigate/Eval/Screenshot`, `SentMethods`, `Close`). `EmulationPlan` now wraps `EmulationSteps` (same behaviour, tests unchanged).
- `core/driver/cft.go`: `ParseCfTStable`, `FetchCfTStable`, `CfTPlatform`, `CfTInstall`; fixture `testdata/cft-last-known-good.json`.
- Unit tests (no browser): config determinism and coherence, injector run in Node vm (native-looking getters, cross-realm toString), pipe framing with a 3 MB message, CDP errors and events, CfT parsing.

## Runtime.enable (answer)
chromedp v0.23.1 sends it: `chromedp.go:590`, `attachTarget` calls `cdp.Call(ctx, c.Target, runtime.Enable, ...)` for every attached target, so chromedp cannot be used on this path. `rawConn` sends only what the caller sends. The E2E test asserts `Runtime.enable`, `Runtime.addBinding`, `Console.enable`, `Debugger.enable` were never sent; rebrowser `runtimeEnableLeak` is green in all four runs below. Needed instead: `Page.enable` (before addScript works), `Page.addScriptToEvaluateOnNewDocument` (runImmediately), `Target.setAutoAttach` (waitForDebuggerOnStart, flatten, recursive), `Runtime.evaluate` on workers, `Runtime.runIfWaitingForDebugger` (none needs Runtime.enable).

## Detector results (Windows host, Windows profile, seed 7; injected vs bare control)
| Detector | Chrome injected | Chrome bare | Edge injected | Edge bare |
|---|---|---|---|---|
| sannysoft | 0 failed | 0 failed | 0 failed | 0 failed |
| rebrowser runtimeEnableLeak / workerConsistency / nativeTampering / platformMismatch / navigatorWebdriver / cdpBindingGlobals | all green | all green | all green | all green |
| rebrowser headless | yellow | yellow | yellow | yellow |
| rebrowser useragent | green | green | red (not Google Chrome) | red |
| CreepJS headless / stealth | 0% / 0% | 0% / 0% | 0% / 0% | 0% / 0% |
| CreepJS lies | getClientRects "unknown rotate dimensions", AudioBuffer "sample noise detected" | none | same two | none |
| BrowserScan bot detection | Normal (Webdriver, UA, CDP, Navigator) | Normal | Normal | Normal |
WebGL renderer seen by sannysoft: injected = profile GPU (AMD), bare = real GTX 1650. Edge showed "no webgl context" in both modes on this SSH session. CreepJS shows our real srflx IP (WebRTC is not injected; needs prefs/flags). Raw texts: not kept in the repo; regenerate with `stockdetect`. Screenshot budget: 1 used.

## 20 fresh launches (new data dir and seed each, Verify = warm-up read + 3 stable canvas reads)
| Engine | failed | spawn to CDP answering (ms) min / median / max | verify (ms) | first-script leaks | worker mismatch | pages with no WebGL |
|---|---|---|---|---|---|---|
| Chrome | 0/20 | 239 / 258 / 294 | 0.7 to 1.1 s | 0 | 0 | 7 |
| Edge | 0/20 | 320 / 359 / 441 | 0.7 to 1.2 s | 0 | 0 | 10 |
"First-script leak" = values read by the first inline script of an http page (hardware, memory, GPU, platform, canvas pixels) differ from the profile or from a later read. Worker = dedicated worker (UA, platform, hardware, GPU, pixels) equals the profile. 5 CfT launches: 0 failed, 272 / 293 / 409 ms (run before the warm-up read was added).

## Worker coverage
Fixed. Browser-level `Target.setAutoAttach` pauses every page, worker, shared worker, service worker; the injector runs via `Runtime.evaluate` on the worker session before `runIfWaitingForDebugger`. E2E (Chrome and Edge): page, srcdoc iframe, about:blank iframe and dedicated worker all agree with the profile (hardware, memory, platform, language, UA, GPU, canvas pixels). CreepJS shared-worker scope matched after workers also override userAgent/appVersion. Not covered: worker `navigator.userAgentData`; sandboxed iframes without script injection.

## Chrome for Testing on Windows
- Download via the official JSON: 209,854,321 bytes in 11.5 s, unzip 5 s, `chrome.exe` is NOT Authenticode-signed; no Zone.Identifier stream, so no SmartScreen prompt; Defender real-time was on, 0 detections, first launch 1.3 s (second 0.3 s). Nothing system-wide touched.
- The JSON has no checksum: verify HTTPS + Content-Length (HEAD) + SHA-256 recorded on first download (stable 157.0.8103.78 zip: 5F6EC402...93F9) and re-check the unzipped tree against our own manifest.
- `--load-extension` works on CfT (MV3 content script, world MAIN, ran in the page, injector also active). `Extensions.loadUnpacked` over the pipe worked in 0.2 to 0.3 s: the Mac hang is NOT reproduced on Windows (2 of 2 runs).

## Gotchas
- A canvas read within ~300 ms of process start returns garbage even without the injector (a large image that differs per launch, then a stable one). Verify discards the first read. Real pages reading that early see the same in bare Chrome.
- `navigator.deviceMemory` is undefined on about:blank (insecure context); Verify skips it there.
- Wrappers must not depend on a per-realm map: CreepJS asks an iframe realm's `Function.prototype.toString` about our getters. Marker-in-source fixes this (stealth 20% and headless 33% before, 0% after).
- Do not zero-skip: audio noise on exact-zero samples made CreepJS report "audio is fake".
- `go test ./core/... ` with LADILALA_E2E=1 also runs older fingerprint-chromium E2E tests, which timed out on winpc ("snapshot a: context deadline exceeded", engine not part of this work).
- Windows kill/Job object path unchanged; no `-race` on Windows.

## Not solved
- CreepJS still flags audio noise and client-rect noise as lies (no way found to hide JS noise there). Use `ModeReal` for Audio and ClientRects until an engine patch, or accept the flag.
- Fonts (enumeration, metrics), rendering-layer canvas/WebGL noise, deep GPU parameters (table is approximate, Linux row unverified), WebRTC IP, `userAgentData` in workers: unfixable from JS or untested.
- Not run: proxy, a managed machine with Chrome policy, long sessions, popups and tabs opened by pages beyond `NewPage`, user-visible side effects of the injector on real sites.
