# Wails v3 gate (R1)

Date: 2026-11-16. Machine: winpc (Windows 11 Pro, 2560x1440 at 125 percent, WebView2 157.0.4341.46).

## Verdict

**R1 is closed. Wails v3 passes every gate. The fallback is not needed.**
Pinned version: `github.com/wailsapp/wails/v3 v3.1.0` (stable tag, not an alpha) and `@wailsio/runtime 3.1.0`.

| Gate | Result | Number or proof |
| --- | --- | --- |
| Build on winpc | PASS | 33.5 s cold (module download), 0.5 s with no change, 1.8 s for a small change. No cgo, no gcc, no Wails CLI, no Node. |
| Run | PASS | Process runs in the user session. Idle: app 34 MB working set; app plus 6 WebView2 processes 368 MB (245 MB private). |
| UI loads from embed | PASS | The real prototype UI (Vietnamese) shows. Cold start to first paint: 715 to 727 ms on runs 2 to 4, 1383 ms on the first run after a data wipe, 1579 ms on the very first run. One outlier of 6004 ms was seen once (light theme run). |
| Go call from UI | PASS | `Ping` gave `pong`. `Version` gave app 1.0.0, go1.27.3, WebView2 157.0.4341.46. |
| Event stream | PASS | `gate:tick` once per second; 5 ticks seen in 3.5 s, 30 ticks after 30 s. |
| Single instance | PASS | Second launch: still 1 process. The first window came back from minimised. |
| Window state | PASS | Position and size saved to `window.json` and restored (set 100,120; reopened at 100,120). Minimum size 900x600 DIP is enforced (1125x750 px at 125 percent). |
| Installer built | PASS | NSIS 3.11. 7.9 MB. Built in 5.4 to 8.5 s. |
| Installer signed (self-signed) and verified | PASS (signature intact, root untrusted) | See "Signing". |
| Updater minimum | PASS | v1 found v2, rejected a changed file, installed the good file in 8 s, restarted as 2.0.0. |
| Launch stock Chrome from the app | PASS | `LaunchStock` from the Wails process: Chrome 157.0.8103.52, title `Ladilala gate page` read over CDP, profile hash verified. |
| Job Object cleanup | PASS | 11 Chrome processes before; 0 after a close (WM_CLOSE) and 0 within 3 s after `taskkill /f` on the app. |

Not exercised: the WebView2-missing path (the runtime is installed on winpc; see "Decisions").

## What was built

- `app/`: Wails v3 shell. `main.go` (window, single instance, updater, SPA asset handler), `shell.go` (the `Shell` service), `windowstate.go`, `platform_windows.go` (WebView2 check, process start time), `wails_windows_amd64.syso` (icon, version info, DPI manifest), `build/windows/` (manifest, NSIS script), `updater.pub`.
- `ui/src/bridge/index.ts`: Wails bridge and a mock for the browser prototype. `ui/src/bridge/gate.ts`: a small overlay (ping, version, chrome, close, update, install buttons, tick counter). It shows only in the Wails window or with `?gate=1`. Edits outside the bridge: one line, `import './bridge/gate'`, at the end of `ui/src/main.ts`. `ui/package.json` gets `@wailsio/runtime`.
- `scripts/build-windows.ps1`: UI build (skip with `-SkipUi`), exe, optional installer (`-Installer`), optional signing (`-Thumbprint`).
- Service methods are bound as `main.Shell.<Name>`: `Ping`, `Version`, `Ready`, `LaunchChrome`, `CloseBrowsers`, `CheckUpdate`, `InstallUpdate`, plus the test hooks `Autorun`, `AutoInstall`, `WriteReport`.
- Self-test: `LADILALA_GATE_AUTORUN=1` runs ping, version, ticks, Chrome and the update check, then writes `%LOCALAPPDATA%\Ladilala\gate-report.json`. `=update` also installs a found update. `LADILALA_GATE_METRICS=1` logs the first paint time. `LADILALA_UPDATE_URL` replaces the feed URL.

## Toolchain (winpc)

Go 1.27.3 (`C:\tools\go\bin`). `go.mod` says `go 1.26.2`. Build flags: `CGO_ENABLED=0 GOOS=windows -tags production -ldflags "-s -w -H windowsgui -X main.version=<v>"`. Bun is not installed on winpc: the UI was built on the Mac and `-SkipUi` was used. NSIS 3.11 was unpacked per user in `%USERPROFILE%\tools\nsis` (no admin). `signtool.exe` comes from the Windows 10 SDK (10.0.22000.0). The Wails CLI (`wails3`) was used on the Mac only: `updater genkey`, `updater manifest`, `generate syso`. Exe size: 14.9 MB unsigned, 15.6 MB signed.

## Window, DPI, theme, WebView2

- The window has the native title bar. The manifest sets per-monitor DPI v2. Text was sharp at 125 percent.
- The title bar follows the system theme (`Theme: SystemDefault`; checked with Windows set to light). **The Vue UI does not follow the system.** `ui/src/theme.ts` starts in dark (`initialValue: 'dark'`). See "Decisions".
- WebView2 data is in `%LOCALAPPDATA%\Ladilala\webview2`. Profiles and window state share `%LOCALAPPDATA%\Ladilala`.
- Missing runtime: `ensureWebView2` reads the registry. If the runtime is absent it asks the user, downloads Microsoft's bootstrapper (`go.microsoft.com/fwlink/p/?LinkId=2124703`), runs it with `/silent /install`, and checks again. On refusal or failure it shows the install link and exits. The NSIS installer runs the same bootstrapper first when the runtime is missing (file `MicrosoftEdgeWebview2Setup.exe` next to `project.nsi`; the build script downloads it).

## Installer

NSIS, per user, no admin (the task ran with a non-elevated token). Result of a silent install and uninstall (`/S`):

- Installs to `%LOCALAPPDATA%\Programs\Ladilala` (`ladilala.exe`, `uninstall.exe`). Start menu shortcut. No desktop shortcut. Uninstall entry in HKCU. Install took 1.2 s.
- The installed app ran: version 1.0.0, 5 ticks, Chrome launched.
- Uninstall removed the folder, the shortcut and the registry key. A test file in `%LOCALAPPDATA%\Ladilala` stayed, with `window.json`.
- Gap: `uninstall.exe` is not signed. Add `!uninstfinalize` with a signing command when a real certificate exists.
- MSIX was not chosen: it needs a trusted certificate to install outside the Store, and the in-app exe swap does not fit its model.

## Signing (self-signed test)

Certificate: `New-SelfSignedCertificate -Type CodeSigningCert`, RSA 3072, SHA-256. Signed with `Set-AuthenticodeSignature` (no admin, no SDK needed) and a DigiCert timestamp.

- `Get-AuthenticodeSignature` on the exe and the installer: signer and timestamp present. Status `UnknownError`: "terminated in a root certificate which is not trusted". That is the expected state for a self-signed root.
- `signtool verify /pa`: 1 error, the same untrusted root. I could not add the root to the user store from a non-interactive session (`certutil -addstore` gave ERROR_NOT_SUPPORTED, a UI prompt is needed). So "Valid" was not observed here.
- Defender (`MpCmdRun -Scan`, real-time protection on): no threats in the exe and the installer.
- SmartScreen: I added Mark of the Web (zone 3) to a copy of the installer and started it. It opened with no SmartScreen prompt (`docs/img/wails-gate-installer.png`). A synthetic Zone.Identifier is not a real browser download, so this does not prove a user will see no warning. Microsoft says a self-signed file gets a strong block for users who did not trust the root.

## Options for a trusted certificate

Sources: Microsoft Learn "Code signing options for Windows app developers" (updated 2026-11-10), Artifact Signing quickstart and pricing, CA/Browser Forum Code Signing Baseline Requirements, My-SSL price page (2026-10-18).

| Option | Cost | Needs | Fit for a Vietnamese individual or company |
| --- | --- | --- | --- |
| Azure Artifact Signing (was Trusted Signing) | About $9.99 per month, 5,000 signatures | Azure paid subscription. Identity check of a few business days. No token. | **No.** Organizations: US, Canada, EU, UK (the quickstart also lists Australia, New Zealand, Japan, South Korea, Singapore, Switzerland, Norway, Israel). Individuals: US and Canada. Vietnam is on neither list. |
| OV from a CA (DigiCert, Sectigo, SSL.com, GlobalSign, Certum) | $129 to $530 or more per year; Certum resellers from about $99 | Key on a token or cloud HSM (mandatory since 2023-06). Validity at most 460 days since 2026-03-01. Legal-entity check, several days. | Yes, worldwide. A company needs its business registration and a verifiable phone or address. [INFERENCE] Some CAs sell individual validation; ask the CA about Vietnam before paying. |
| EV from a CA | $349 to $669 per year | Same hardware rule, stricter checks. | Not worth it for SmartScreen: EV lost the instant bypass in 2024. Needed only for driver signing or a customer rule. |
| SignPath Foundation | Free | Open-source project, application process. | Only if the project is open source. |
| Microsoft Store (MSIX) | Free, Store re-signs | Store account, certification. | Possible later. It changes the installer and the updater model. |
| Self-signed | Free | Root trusted by hand or by Intune or Group Policy. | Dev and test only. |

All trusted options build SmartScreen reputation over time. Keep the same publisher identity for every release. Plan for warnings on the first weeks of downloads.

## Updater

Wails v3.1.0 has a built-in updater (`app.Updater`). Providers: GitHub, appcast, Keygen, and `endpoint` (one JSON manifest on any static host). I used `endpoint`.

- Feed: `wails3 updater manifest -version 2.0.0 -key <private key> ladilala-windows-amd64.exe` writes `manifest.json` with SHA-512 and an Ed25519ph signature. Served from the Mac with `python3 -m http.server` over plain HTTP on the LAN.
- The app pins the public key (`app/updater.pub`, embedded). With a key set, an unsigned release is refused (fail closed).
- Test 1, changed bytes under a valid manifest: check found 2.0.0, install failed with `updater: digest mismatch`, the exe stayed as v1.
- Test 2, good feed: the helper process swapped the exe while the app quit and restarted it. 8 s from start to the swapped file. Report after restart: 2.0.0, no newer version.
- Works because the install folder is per user and writable. A Program Files install would need admin for the swap.
- Missing or to watch: the updater does not check Authenticode on Windows (only the Ed25519 signature and the digest). Log and `.ready` files stay in `%TEMP%` (`wails-update-*`). The uninstall entry still shows `DisplayVersion` 1.0.0 after an update. The wrong-signature case with a right digest was not run here (upstream has a fail-closed test). A feed host with real HTTPS was not tested.
- The gate key in `app/updater.pub` is for tests. Generate a new pair for release and keep the private key in a secret store.

## Stock Chrome and Job Object

`Shell.LaunchChrome` creates a `driver.Launcher` (it owns the Job Object with kill on close), calls `LaunchStock` with a fresh profile and reads the page title over CDP. `ServiceShutdown` closes the sessions and the launcher. The hard-kill case relies on Windows closing the job handle, so it works even when the app crashes.

## Decisions for the orchestrator

1. **Certificate path.** Azure Artifact Signing is closed to Vietnam. Pick an OV certificate from a CA that accepts a Vietnamese individual or company, or form a legal entity in a supported country. Budget about $130 to $530 per year plus the token or cloud signing fee.
2. **UI theme.** Make the UI follow the system theme: a one-line change in `ui/src/theme.ts` (use auto mode). I did not edit it (scope).
3. **Update host and keys.** GitHub Releases (the default feed URL in `main.go` is a placeholder) or Cloudflare R2 with HTTPS. Create the release Ed25519 key and decide who holds it. Done: see `docs/release-process.md`.
4. **Gate overlay.** Keep `import './bridge/gate'` for development, or remove the line before release. The overlay is hidden outside Wails.
5. **Missing WebView2 path.** Test it once on a clean Windows 10 VM before release.
6. **Bun on winpc.** Install it if the UI must be built there; today the script needs `-SkipUi`.

## Files

`docs/img/wails-gate-app-running.png` (app with overlay), `docs/img/wails-gate-light-theme.png` (system light, UI dark), `docs/img/wails-gate-installer.png` (installer started with Mark of the Web).
