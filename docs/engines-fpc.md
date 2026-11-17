# fingerprint-chromium (engine `fpc`)

## What it is
fingerprint-chromium is a build of ungoogled-chromium that changes the browser fingerprint inside the browser. Ladilala offers it as a download, next to Chrome, Edge and Chrome for Testing. You pick it per profile, per group or as the default.

The engine ID is `fpc`. The files go to `<data>/engines/fingerprint-chromium/<tag>/`. There is no installer and no admin step.

## Pinned version
Only one build is offered: tag `148.0.7778.215` (Chromium 148, Windows x64).

| Field | Value |
|---|---|
| Zip | 189,767,686 bytes |
| On disk | about 425 MB |
| SHA-256 | `9ef3f471b7a6641b4224532522b29141ce3746e27d55788d88e2fd951f362579` |

The pin table is `driver.FPCBuilds` in `core/driver/fpc.go`. Ladilala checks the SHA-256 of the zip before it unpacks anything. If the hash is wrong, or the download fails, it deletes the partial file and installs nothing.

Do not offer 150.0.7871.186. It crashes on the first canvas read (200 of 200 launches in M0).

## What it fixes
The engine spoofs these surfaces itself: canvas, audio, client rects, fonts, WebGL (GPU strings) and WebRTC. It reads the profile seed and the fingerprint flags. The Ladilala injector (`core/inject`) is off for this engine. The injector would add noise on top of the engine noise.

## Known bugs
A page can see these. Ladilala logs them at each launch and does not hide them.

- `measureText` width is about 0.00015 (a real browser reports about 247).
- `navigator.deviceMemory` is 16.
- `screen.colorDepth` is 32.
- Screen size stays real. The profile screen field is not applied, so the app does not claim it.

## Age risk
The build is about 10 Chromium versions behind current Chrome. It misses the security fixes of those versions. The Engines screen shows an "Old engine" note. Use it only for profiles that need its fingerprint, and keep them away from untrusted pages.

## Launch self-check
After install, and at each launch of a profile on `fpc`, Ladilala reads the canvas (`getImageData`, `toDataURL`) and WebGL (`readPixels`) with a 3 second limit. If the read hangs or the renderer crashes, the run does not start. The error is `driver.ErrFPCCanvasBroken`: "This engine build crashed on canvas read. Choose another engine."

## How to add a newer tag
1. Find the release on GitHub (`adryfish/fingerprint-chromium`) and the Windows x64 zip.
2. Get the size and the SHA-256 from the release API: `GET https://api.github.com/repos/adryfish/fingerprint-chromium/releases/tags/<tag>`, field `digest` of the asset. Download the zip and check the hash yourself.
3. Test it. Add the entry to a local build, install it from the Engines screen (the install runs the launch self-check), then start many profiles on it. The M0 harness in `cmd/winspike` (`-engine fpc -enginedir <folder>`) can run 50 or more fresh launches. Any hang or crash means the tag is not safe.
4. Add a `FPCBuild{Tag, URL, Size, SHA256, ChromiumMajor}` to `driver.FPCBuilds`. The first entry is the one the app offers.
5. Run `go test ./core/driver ./app`.

## Recorder overlay on a page with a strict CSP
Symptom: on some pages the recorder overlay had no layout. The bar, the Data columns panel and the first-use card sat in the page flow as plain text with default buttons ([before](img/fpc-overlay-before.png)).

Cause: the overlay put its CSS in a `<style>` element inside its shadow root. A page with a Content-Security-Policy `style-src` that has no `'unsafe-inline'` (a nonce or hash only) blocks that element, so no overlay rule applied. This is not specific to fingerprint-chromium: stock Chrome 157 and FPC 148 behave the same on such a page. The light test pages have no CSP, which is why the M0 recorder run (9 of 9) looked right.

Fix: `core/recorder/recorder.js` now builds the CSS as a constructed stylesheet (`adoptedStyleSheets`), which CSP `style-src` does not govern; the `<style>` element stays only as a fallback for a browser without `CSSStyleSheet`. Result on FPC 148 and Chrome 157: [after](img/fpc-overlay-after.png), [Chrome](img/fpc-overlay-chrome-after.png). The test `TestE2EOverlayLayout` (`core/recorder/overlay_e2e_test.go`, page `testdata/dark.html` with a nonce CSP and hostile global styles) fails if the bar, panel or card is not `position: fixed` or its box is outside the viewport. Run it with `LADILALA_E2E=1 LADILALA_E2E_BROWSER=fpc LADILALA_FPC_DIR=<folder>`.

Launch flags: none of the `FingerprintChromium.Args` flags changes the overlay. The CSS features it uses (`:host`, `all`, `position: fixed`, shadow DOM) work on 148.

## Infobar "You are using an unsupported command-line flag"
Not reproduced. The app's exact command line (the flags of `launchFlags` plus `FingerprintChromium.Args`, with the debug pipe, with `--proxy-server`, `--disable-spoofing`, `--fingerprint-platform=macos`, `--fingerprint-brand=Edge`, a `vi-VN` locale) was run on Windows with FPC 148 in a visible window, one flag group removed at a time: no infobar showed, and the viewport had no infobar offset in the E2E run. The text names the flag, but the screenshot of the report cuts it off, so the flag is unknown. Most likely the report came from an older Ladilala build with other flags. `--test-type` was not enabled: it hides this class of infobar but also changes sandbox and security behaviour, and no flag is known to need it. If an infobar is shown it adds about 40 px above the page, so `window.innerHeight` and `outerHeight - innerHeight` differ from a browser without it.
