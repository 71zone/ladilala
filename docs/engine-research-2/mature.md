Engine research round 2, slice A: established browsers and licensable engines
Date 2026-11-16. Upstream Chromium stable 157. Sources: GitHub API (gh), release assets, source files, vendor pricing/terms pages, ChatGPT consult (page 83, own tab).
Legend: V = verified by me against primary source, C = ChatGPT claim only (unverified), I = my inference (not tested).

1. OPEN-SOURCE BROWSERS (stock Chromium behaviour for CDP, --user-data-dir, --remote-debugging-pipe: I for all)
Name            | Licence   | Contributors | Age  | Latest (V)                 | Chromium vs 157 | Win build
Brave           | MPL-2.0   | ~530         | 2017 | v1.102.8 (2026-11-13)      | not read, tracks stable | Setup.exe, Standalone(Silent)Setup.exe, brave-v*-win32-x64.zip, each with .sha256/.asc
Cromite         | GPL-3.0   | ~32          | 2020 | v153 (2026-09-15)          | 4 majors behind, 2 months stale | chrome-win.zip
Thorium         | BSD-3     | ~15          | 2021 | M156.0.8078.80 (2026-11-09)| 1 behind | thorium-win repo, CPU-tuned (AVX2) builds, x64 only
Helium          | GPL-3.0   | ~132         | 2025 | 0.21.3 (2026-11-12)        | not read | helium-windows repo, auto-update
ungoogled-chromium | BSD-3  | ~113 (win ~21)| 2015| 157.0.8103.78-1 (2026-11-13), win 157.0.8103.50-1.1 | current | zip/installer, community built, unsigned (I)
Iridium         | BSD-3     | n/a          | 2019 | last push 2024-01          | dead | none
Vivaldi, Opera: closed, no fingerprint control, skip.

Brave (brave-core) fingerprint control, read from source:
- Farbling covers canvas, WebGL, WebGPU, audio, fonts, hardwareConcurrency, deviceMemory, languages, plugins, screen, UA, speech synthesis, enumerateDevices, USB (browser/farbling/*_browsertest.cc names).
- It is NOISE, not a coherent identity. It does not let us set UA, GPU vendor/renderer, screen, timezone per profile; those stay with our CDP EmulationPlan.
- Seed: BraveShieldsSettingsService::GetFarblingToken stores a random base::Token per site in Shields metadata ("farbling_token" in host content settings, i.e. in the profile Preferences). Created with Token::CreateRandom(); no CLI switch or pref sets it. Only test hooks (ScopedStableFarblingTokensForTesting) are deterministic. Feature kBraveFarblingTokenReset XORs a per-session token so values change on restart.
- Consequence: per-profile persistence is natural (each user-data-dir keeps its own tokens), but the seed is not choosable from our generator. Pre-seeding the Preferences file is possible (I, untested). Level (off/standard/strict) is settable per site via shields prefs/policy (I).
- Licence: MPL-2.0 source; binaries are free to download from github.com/brave/brave-browser/releases with checksums and PGP .asc. Redistribution of the signed binaries: no explicit grant found; Brave trademark applies (I, ask or auto-download only).
- Risks: Brave Shields/ads/rewards/sync/VPN services phone home; need policy/flags to quiet them; fingerprint is recognisably Brave (UA hides it, but navigator.brave exists unless disabled).
- Cromite: Android-first; anti-fingerprint patches "not comprehensive" per README; GPL-3; lag 4 majors. Not a fit.
- Thorium/Helium: no fingerprint features (privacy/perf focus). Helium GPL-3, young. Thorium solo maintainer (Alex313031).
- ungoogled-chromium: cleanest base (no Google calls, BSD-3), Windows 157 builds published within days. No fingerprint control; we would supply all via CDP + our own patch set.

2. EMBEDDED ENGINES
- CEF (BSD-style; GitHub says NOASSERTION): ~143 contributors, builds at cef-builds.spotifycdn.com (V): stable 156.0.30 / chromium 156.0.8078.96, beta 158. One major behind 157. Official Windows x64 binaries, redistributable, we sign our own bundle.
  Fingerprint control: none built in. CEF exposes --remote-debugging-port and CDP, request contexts per profile (cache_path = per-profile dir). All spoofing must be CDP Emulation + injected JS, same limits as stock Chrome. Pipe mode: not documented for CEF (unverified).
  Go bindings (V, GitHub search): energye/energy (Apache-2.0, 622 stars, pushed 2026-11-13, CEF framework), richardwilkes/cef (MPL, last push 2021, dead), cef2go (2020, dead). CGO + C++ glue is the cost. Needs our own render/UI host, i.e. replaces chromedp-on-stock-Chrome with a custom browser shell. Large scope.
- Electron: MIT, 124k stars, v45.3.3 (2026-11-14) (V); Chromium major tracks stable closely (version not read). Node/Go split: spawn Electron per profile with --user-data-dir + --remote-debugging-port; fingerprint = same CDP-only story. Heavy (150 MB per bundle), but fully redistributable. Chrome flags like --remote-debugging-pipe supported (I).
- WebView2: evergreen runtime is Microsoft-updated (Chromium follows Edge); fixed-version runtime is redistributable with the app. Per-profile via userDataFolder; CDP via WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS or DevTools protocol API (I). No fingerprint features, Windows-only, Edge-branded UA. wailsapp/wails (MIT, 37k stars) wraps WebView2 for Go. Evergreen version cannot be pinned per profile.

3. COMMERCIAL ENGINES (prices as of 2026-11-16, C unless marked V)
Product    | Local API / CDP | Price | Terms on embedding/competing | Binary redistribution
Kameleo    | REST localhost:5050, CDP, MIT SDKs; kameleo-io/kameleo repo (V, 223 stars, desc "engine-level fingerprint masking, Docker") | V: Free EUR0 (2 concurrent), 59, 299, 1499 per month | usage for contracting party own business, no agency use (C; ToS page JS-rendered, my fetch empty) | downloaded via Kameleo, proprietary, no grant
GoLogin    | SDK downloads+launches Orbita, returns WS endpoint | V: Pro from $9 (page), Business $99+, Free 3 | V: ToS forbids reverse engineer, download/distribute Software to third parties | not redistributable. gologinapp/gologin repo has NO licence file (V); C said GPL-3, conflicts
AdsPower   | local HTTP API, CDP | free 2 profiles, paid amounts unverified | no resale/sublicense without consent; partner program for integrators (C) | proprietary, partner agreement needed
Multilogin | xcli, API, Mimic (Chrome), Stealthfox frozen at Firefox 146 | C: Free 5, Pro $11-29, Business $40-89 | V: ToS bars copy, resell, commercial exploitation, may refuse resellers | no
Dolphin Anty| localhost:3001 returns CDP port | C: Free 5, $10..$299 | EULA bars redistribute/sublicense (C) | no
BitBrowser | local REST returns CDP ws | C: $10..$25 tiers | personal non-transferable licence | no
Incogniton | REST 127.0.0.1:35000 | C: $19.99..$79.99 | website ToS bars using it to compete (C) | no
Octo       | localhost:58888, CDP | C: EUR10..329 | EULA bars combining into other software (C) | no
Wayfern    | hosted wss://api.wayfern.com (C) | C: $19..$499 credits | bars redistribution; running outside Donut Browser needs written permission (C); Donut (zhom/donutbrowser) is AGPL-3.0, 4301 stars (V) | no
Bablosoft FingerprintSwitcher | browser-with-fingerprints (MIT, 245 stars, V) spawns patched Chromium with CDP, Windows only | V: Premium $30/month, $60/3 months | BAS allows selling compiled apps; fingerprint dataset export restricted (C) | patched Chromium rights unclear
Verdict: every one is a closed client with a user-facing licence. Using any as OUR engine makes the app a reseller/wrapper and ties updates to a competitor. Kameleo and Bablosoft are the only ones built for third-party programmatic use; neither grants redistribution.

4. FINGERPRINT GENERATORS (work on stock Chrome, can feed core/profile)
- apify/fingerprint-suite: Apache-2.0, ~26 contributors, 2671 stars, v2.1.90 (2026-11-09) (V). Packages: fingerprint-generator (Bayesian network from real traffic data), fingerprint-injector (JS overrides via CDP/Playwright/Puppeteer), header-generator. JS-only; port the data model or run through a Node sidecar.
- daijro/browserforge: Apache-2.0, 1296 stars, ~3 contributors, last commit 2026-10-29 (V). Python port of the same data.
- Limits: JS injection overrides are detectable by toString/prototype checks; fine for the "moderate" goal.

5. RANKED TOP 3 FOR LADILALA
1. ungoogled-chromium (Windows) as base engine + apify/fingerprint-suite data model for generation, applied through CDP. Risk: community Windows builds are unsigned, per-profile native fingerprints (canvas/audio noise) must come from our own injection. Trust in shipped product: yes for engine if we mirror and verify hashes (BSD-3, 157 within days, 113 contributors); injection layer is ours.
2. Brave release builds, auto-downloaded. Risk: farbling seed not controllable per profile, Brave services/branding, no redistribution grant for binaries (download-only). Trust: moderately, signed vendor binaries with .asc and a large team, as a "stronger default noise" option rather than the only engine.
3. CEF embedded (energye/energy or own CGO glue). Risk: huge engineering cost, one major behind, still no native fingerprint control. Trust: yes technically (redistributable, official builds), but it buys nothing for fingerprinting.
Not recommended: any commercial engine (licence blocks shipping), Cromite (stale, GPL), Thorium/Helium (no fingerprint features, small maintainer base).
Unverified: Chromium major in Brave/Helium/Electron releases, CEF pipe support, WebView2 CDP details, all items marked C.
