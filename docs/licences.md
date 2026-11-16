# Licences

Status date: 2026-11-16. This is an engineering list, not legal advice. Counsel has not reviewed it. Items I could not check are marked [INFERENCE] or listed under "Unknown".

## Go dependencies (`go.mod`)

I read the `LICENSE` file of each module in the Go module cache. `go list -m all` prints a very large module graph. Only the modules in `go list -deps ./core/... ./cmd/...` end up in a binary. Wails is in `go.mod` but no package imports it yet.

| Module | Version | Licence | Use |
|---|---|---|---|
| github.com/chromedp/chromedp | v0.23.1 | MIT | Old fingerprint-chromium path only. Open question in ADR 0018 |
| github.com/chromedp/cdproto | v0.160.3 | MIT | CDP types. Used by the raw client |
| github.com/wailsapp/wails/v3 | v3.1.0 | MIT | Shell. Not yet imported by code |
| github.com/xuri/excelize/v2 | v2.11.1 | BSD-3-Clause | Excel data source |
| modernc.org/sqlite | v1.62.0 | BSD-3-Clause | Storage. Bundles SQLite (public domain [INFERENCE]) |
| golang.org/x/net, x/sys, x/text | v0.62.0, v0.49.0, v0.44.0 | BSD-3-Clause | |
| golang.org/x/crypto, x/mod, x/term | v0.58.0, v0.42.0, v0.47.0 | BSD-3-Clause | Indirect |
| github.com/go-json-experiment/json | 2026-02-13 | BSD-3-Clause | Indirect. cdproto uses it |
| github.com/google/uuid | v1.6.0 | BSD-3-Clause | Indirect |
| github.com/xuri/efp, xuri/nfp | pseudo | BSD-3-Clause | Indirect. excelize |
| modernc.org/libc, mathutil, memory | v1.80.1, v1.7.1, v1.12.1 | BSD-3-Clause | Indirect. sqlite |
| github.com/remyoudompheng/bigfft | 2023-01-29 | BSD-3-Clause | Indirect. sqlite |
| github.com/richardlehane/mscfb, msoleps | v1.0.9, v1.0.6 | Apache-2.0 | Indirect. excelize. Needs NOTICE check |
| github.com/ebitengine/purego | v0.11.1 | Apache-2.0 | Indirect. Wails |
| github.com/coder/websocket | v1.8.14 | ISC | Indirect. Wails |
| github.com/go-ole/go-ole, mattn/go-isatty, ncruces/go-strftime, tiendc/go-deepcopy, dustin/go-humanize | various | MIT | Indirect |
| github.com/godbus/dbus/v5 | v5.2.2 | BSD-2-Clause [INFERENCE] | Indirect. Linux only |

No GPL, LGPL or AGPL module is in the module list. I read licence text headers only. Run a tool such as `go-licenses` before release to confirm. It is not installed here.

## UI dependencies (`ui/package.json`)

Licences come from `node_modules/<pkg>/package.json`.

| Package | Version | Licence |
|---|---|---|
| vue, vue-router, vue-i18n, @vueuse/core, vue-sonner | 3.5.45, 4.6.5, 11.4.20, 15.0.0, 2.0.9 | MIT |
| reka-ui | 2.11.0 | MIT |
| class-variance-authority | 0.7.1 | Apache-2.0 |
| cn | 0.5.2 | MIT |
| @lucide/vue | 1.73.0 | ISC |
| tailwindcss, @tailwindcss/vite | 4.3.5 | MIT |
| tw-animate-css | 1.4.0 | MIT |
| vite, @vitejs/plugin-vue, vue-tsc, @types/node | 5.4.21, 5.2.4, 2.2.12, 26.6.6 | MIT |
| typescript | 5.6.3 | Apache-2.0 |

shadcn-vue is not a dependency. It copies component source into `ui/src/components/ui/` (`components.json`, style `reka-vega`). [INFERENCE] The shadcn-vue project licence is MIT. I did not read it. Lucide icons inside `@lucide/vue` are ISC with some parts under MIT [INFERENCE]. Transitive npm packages are not listed. Run a licence scan on `bun.lock` before release.

## NOTICE obligations

- **apify/fingerprint-suite, Apache-2.0.** `core/inject/NOTICE` says `injector.js` is original code that applies techniques from that project. No source file is copied. I fetched the upstream licence file and it is Apache-2.0. Section 4 of Apache-2.0 needs a copy of the licence and a NOTICE only when you redistribute their work or a derivative. [INFERENCE] The current file is a safe-side attribution. Keep `NOTICE` in the repo and ship it in the installer. If any code is copied later, ship the full licence text.
- **Apache-2.0 dependencies** (class-variance-authority, typescript is a dev tool only, mscfb, msoleps, purego): ship their licence texts and any NOTICE with the installer.
- **MIT, BSD, ISC dependencies:** the licence text and the copyright line must ship with binary distributions. Build one `THIRD_PARTY_NOTICES.txt` from a scan. The About screen links to it.
- The product's own licence is not set. No `LICENSE` file exists in the repo. This is a user decision.

## Chrome for Testing (R5)

What I read:

- `developer.chrome.com/docs/automation-and-testing/download-test-binaries`: it explains downloads by npm tool or JSON endpoints. It says Chrome for Testing is not meant for regular browsing. It names no redistribution right.
- `github.com/GoogleChromeLabs/chrome-for-testing`: the repo and its LICENSE are Apache-2.0. This covers the repo's scripts and data, not the browser binaries. The README states no binary licence.
- The Chrome Terms of Service (`chromium.googlesource.com/chromium/chromium/+/refs/heads/main/chrome/app/resources/terms/terms_en.html`) apply to the executable code of Google Chrome. [INFERENCE] Chrome for Testing is Google-built and Chrome-branded, so these terms may apply. A Google community thread asks this exact question and I could not read an answer (`support.google.com/chrome/thread/383394537`).

Conclusion for the app, as engineering policy until counsel answers:

| Action | Allowed? |
|---|---|
| Download from Google's official URLs to the user's PC at run time, on the user's request | Yes. This is how the doc says to use it. Tested on winpc (`docs/stock-engine.md`) |
| Check versions through the official JSON | Yes |
| Bundle the Chrome for Testing zip inside our installer | No. Not allowed until the terms are confirmed |
| Host a copy of the zip on our servers | No. Same reason |
| Patch or rebuild Chrome for Testing and ship it | No. A rebuilt Chromium (tier 3) is a different case. Chromium source is BSD-licensed, but branding rules apply |
| Remove Google branding or claim it is ours | No |

Also note that `chrome.exe` in Chrome for Testing is not Authenticode-signed (measured on winpc).

## WebView2

- Microsoft documents two ways to get the Evergreen runtime: the Evergreen Bootstrapper (small, downloads the runtime) and the Evergreen Standalone Installer for offline use (`learn.microsoft.com/microsoft-edge/webview2/concepts/distribution`).
- The runtime ships with Windows 11 and is common on Windows 10. [INFERENCE] The installer must still check for it.
- I did not read the WebView2 redistribution licence text. Unknown.

## Wails

Wails v3.1.0 is MIT (`LICENSE` in the module cache). Wails brings its own dependencies. They show in `go.mod` as indirect modules, for example `coder/websocket`, `purego`, `go-ole`. Re-scan after the shell is built. R1 (build, sign and update on Windows) is open.

## Unknown

1. Redistribution terms of the Chrome for Testing binary (R5).
2. WebView2 redistribution licence text.
3. Full transitive npm licence list.
4. Whether `modernc.org/libc` and `modernc.org/sqlite` carry extra notices in third-party files.
5. Trademark clearance of the name "Ladilala" (vault ADR 0015, open). It is a separate matter from licences.
6. Licence of any future tier 3 engine (patched Chromium). Chromium's third-party licences list is large.
7. The product's own licence and EULA.

## Checked against

`go.mod`, module cache `LICENSE` files (`go mod download -json`), `go list -deps ./core/... ./cmd/...`, `ui/package.json`, `ui/node_modules/*/package.json`, `ui/components.json`, `core/inject/NOTICE`, `docs/stock-engine.md`, and the URLs named above, read on 2026-11-16.
