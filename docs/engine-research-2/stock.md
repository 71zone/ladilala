# Slice B: stock Chrome / Edge / Chrome for Testing + CDP injection (engstock, 16 Nov 2026)
Evidence tags: [E] measured on this Mac (Chrome 157.0.8103.51 stable, CfT 157.0.8103.78, mac-arm64, raw CDP over Bun/Node, no Runtime.enable, scripts in /tmp/stock);
[S] primary source or repo metadata read today; [G] Gemini only (chat tab 84), not trusted unless noted; [U] unverified.

## 1. Surfaces: CDP/flags vs injection
| Surface | Clean via CDP / flag | Notes |
|---|---|---|
| UA string, UA-CH (brands, platform, arch, bitness) | Emulation.setUserAgentOverride + userAgentMetadata | [E] applied to page, iframes AND workers (worker UA matched) |
| Timezone, locale | setTimezoneOverride, setLocaleOverride | [E] timezone also correct inside worker; locale only partly (navigator.language stayed en-US in my run: set it via setUserAgentOverride acceptLanguage too) |
| hardwareConcurrency | setHardwareConcurrencyOverride | [E] page+iframe OK, WORKER STILL 12 (leak) |
| screen / viewport | setDeviceMetricsOverride (screenWidth/Height) | [E] screen.width ok; availWidth mirrors it (no taskbar delta: tell for Windows) |
| geolocation, media features | setGeolocationOverride, setEmulatedMedia | [S] standard; not run |
| proxy | --proxy-server (per profile) | [S]; auth needs Fetch.authRequired or our local relay (already planned) |
| WebRTC leak | prefs webrtc.ip_handling_policy=disable_non_proxied_udp + --force-webrtc-ip-handling-policy | [S]/[U] not run; CreepJS showed our real srflx IP with default prefs |
| Injection needed | canvas, WebGL vendor/renderer/params, audio, fonts, client rects, deviceMemory, plugins, speech voices, battery, mediaDevices, navigator.platform on pages that read it before override | no CDP knob exists |
Gotcha [E]: Page.addScriptToEvaluateOnNewDocument did NOT run until Page.enable was sent first on the session. Page.enable does not trigger the Runtime.enable leak.

## 2. Injection quality
Own 40-line injector (toString map, getter descriptors with names, WebGL getParameter wrap, canvas 1-bit noise) [E]:
- passes: Function.prototype.toString on wrapped getters, own-property list clean, iframes (also srcdoc) covered, stack trace of a plain TypeError clean.
- FAILS: Web Workers. Worker saw deviceMemory 16 and hardwareConcurrency 12 (real) while page said 4/6. Same for WebGL/canvas in OffscreenCanvas workers. Fix: Target.setAutoAttach(waitForDebuggerOnStart=true, flatten) and inject into each worker target before Runtime.runIfWaitingForDebugger [U, not built].
- rebrowser-bot-detector flagged our injector: workerConsistency RED (platform differs in worker), nativeTampering YELLOW (Navigator.webdriver wrapped), platformMismatch YELLOW (claims Windows, CSS system colours say macOS; on a real Windows host with Windows profiles this vanishes).
Tooling state [S] (gh api, today):
| Tool | State | License | Verdict |
|---|---|---|---|
| puppeteer-extra-plugin-stealth | repo not archived but last default-branch commit 2023-03-01 | MIT | stale, do not depend |
| apify/fingerprint-suite (fingerprint-injector, generator) | v2.1.90, 2026-11-09, active | Apache-2.0 | best reference; JS injector is Node-only but the script is plain JS we can embed; its fingerprint DB idea fits our coherence need |
| rebrowser-patches | v1.1.3, 2026-11-02 | MIT | patches puppeteer/playwright libs (Runtime.enable via addBinding, sourceURL removal); irrelevant to chromedp, ideas reusable |
| patchright | v1.64.1, 2026-10-15 | Apache-2.0 | Playwright fork, avoids Runtime.enable via isolated contexts; Node/Python/.NET only |
| nodriver / zendriver | active 2026-10 | AGPL-3.0 | Python, CDP without Runtime.enable; AGPL, do not embed |
| undetected-chromedriver | last push 2025-07, GPL-3.0 | | stale, chromedriver-based, skip |
| camoufox | active, MPL-2.0 | | Firefox, off-scope |
chromedp (MIT, v0.23.1 2026-11-14) calls Runtime.enable in its default target setup [U: verify in code]; we can drive raw cdproto commands and Runtime.evaluate with explicit contextId/isolated worlds instead.
CDP detection [E]/[S]: on stock Chrome 157, without Runtime.enable, rebrowser's runtimeEnableLeak = green and our Error.stack getter probe fired 0 times. The Error.stack/console.debug trick still works against any client that calls Runtime.enable [G, matches rebrowser README]; no Chrome version fixed it [U]. Sources of leak otherwise: sourceURL strings on Runtime.evaluate (we send none), main-world exposeFunction/addBinding names (none used).

## 3. CfT, pipe, extensions
- --remote-debugging-pipe + own --user-data-dir on branded Chrome 157 [E]: works (Extensions.loadUnpacked via pipe also worked, content script ran). Official blog restricts only the default dir [S developer.chrome.com/blog/remote-debugging-port].
- --load-extension: branded Chrome 157 IGNORES it [E] (also with --disable-features=DisableLoadExtensionCommandLineSwitch). CfT 157 HONOURS it [E: extension service worker appeared in /json]. Mosaic blog and chromium-extensions PSA confirm branded-only removal [S].
- Extensions.loadUnpacked over --remote-debugging-pipe + --enable-unsafe-extension-debugging works on branded Chrome [E]; on CfT the same script hung after loadUnpacked (cause unknown) [U].
- MV3 content script world "MAIN" at document_start: ran in main world on branded Chrome via loadUnpacked [E]. It also injects into iframes (all_frames). It does NOT cover dedicated workers; no real advantage over CDP injection except it avoids Page.enable and survives without a CDP session. Needs the unsafe-debugging flag, which is a detectable switch only via chrome://version, not via JS [U].
- CfT detection vs stock [E]: not tested head to head; CfT identifies as Chrome/157 with the same UA brands. Differences are in branding, no auto-update, no Widevine/proprietary codecs? [U].

## 4. Ceiling at "moderate hiding" (Mac experiment, injected set)
| Detector | Result |
|---|---|
| bot.sannysoft.com | 31 passed, 0 failed, 0 warn (injected and bare) |
| rebrowser-bot-detector | green: runtimeEnableLeak, webdriver, viewport, headless, cdpBindingGlobals; red: workerConsistency; yellow: nativeTampering, platformMismatch (cross-OS spoof only) |
| CreepJS | still reports lie/headless hash rows; real srflx IP visible; per-surface hashes differ from bare run so uniqueness changes work [E, no score extracted] |
User outcome: passes typical bot-check pages and shuffles identity across sessions. Does not survive detectors that compare worker vs page, GPU/font consistency, or real hardware (WebGL params beyond vendor/renderer, MAX_TEXTURE sizes, extension lists, font set from a real OS).

## 5. Windows specifics [S/U, not tested: winpc busy]
- Channel pick: read HKLM/HKCU Software\Google\Chrome\BLBeacon version and App Paths; Edge via same pattern (Microsoft\Edge\BLBeacon). Prefer stable, fall back to Edge, then our own CfT.
- CfT updates: poll https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json (Stable 157.0.8103.78, ts 2026-11-15 11:10Z) and known-good-versions-with-downloads.json; download chrome-win64.zip into %LOCALAPPDATA%\Ladilala\engines\<ver>, keep N-1. Verify no checksum field exists in the JSON, so pin by HTTPS + size, or hash on first fetch [U].
- CfT never self-updates and does not touch the Google Update service, no UAC, runs from a user-writable dir; Defender may scan the 250 MB unzip once (exclusion needs admin, do not ask).
- Chrome policy (HKLM\Software\Policies\Google\Chrome) can override flags (ExtensionInstallBlocklist, proxy); a managed machine will break stock mode silently, so detect and warn.

## Answer
Stock browser + our injector good enough for moderate hiding: YES for UA/UA-CH, timezone, locale, screen, hardware threads, memory, WebGL vendor/renderer strings, canvas/audio noise, WebRTC policy, proxy; NO for worker consistency unless we add worker auto-attach injection (feasible, not built), and NO for deep GPU/font/real-hardware coherence.
Three surfaces needing an engine patch: (1) fonts enumeration/metrics per profile; (2) WebGL/GPU parameters and shader precision (beyond the two strings) incl. in workers; (3) audio/canvas/client-rect noise at the rendering layer (so toString and worker paths cannot leak). Also nice: navigator fields inside workers.
Recommended architecture: hybrid. Default = stock Chrome (or Edge) with --remote-debugging-pipe, custom user-data-dir, raw CDP (no Runtime.enable, Page.enable + addScript, worker auto-attach), injector derived from apify/fingerprint-suite (Apache-2.0) with a per-profile seed. Second tier = CfT pinned and auto-updated (only channel where --load-extension works; use it for MV3 helper extension features). Optional third tier = patched engine for the three surfaces, same BrowserEngine interface, user opt-in download.
Ranked: 1 stock+injector hybrid (ship first), 2 CfT as managed fallback, 3 patched engine optional. Gemini notes: its URLs for the 137 blog were Google search links (invented); its CfT-load-extension claim matched my test, its Runtime.enable remarks matched rebrowser README.
