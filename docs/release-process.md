# Release process

Status date: 2026-11-17. The release pipeline exists and an update from 1.0.0 to 1.0.1 was tested on winpc against a local feed. Nothing is uploaded yet. The feed is `https://ladilala.71.zone/manifest.json` (Cloudflare R2 bucket `ladilala-releases`, custom domain). Unverified statements are marked [INFERENCE].

## Decided

| Item | State | Source |
|---|---|---|
| App update channel with Stable, Beta, Nightly | Decided as a feature | Vault ADR 0011 |
| Wails v3.1.0 updater, `endpoint` provider, Ed25519 manifest signature, SHA-512 digest | Decided and tested (`docs/wails-gate.md`) | Vault ADR 0001 |
| Update artifact is the plain `ladilala.exe`. The updater swaps it in place in the per-user install folder | Tested | This file, "Verified" |
| NSIS installer, per user, for the first install only | Built and tested in the gate | `docs/wails-gate.md` |
| No Authenticode signing for now (no budget) | Decided by the user. Windows SmartScreen will warn on the first downloads. The updater checks only Ed25519 and SHA-512 | `docs/wails-gate.md` |
| Feed host: Cloudflare R2 over HTTPS | Decided. The orchestrator uploads. No script here uploads | User |

## Versioning

- Semantic versioning: `MAJOR.MINOR.PATCH`, `0.x` until the first stable public release. Pre-releases use `-beta.N` and `-nightly.YYYYMMDD`. The UI prototype uses `0.4.2`, `0.5.0` and `0.5.0-beta.2`, plus a build number. Keep both: version for people, build number for ordering.
- The macro schema has its own version (`schemaVersion`, now 1). A release that changes it needs a migration in `macro.Migrate` and a note in the release text.
- The database has migration numbers (`core/store/migrations`). A release never opens a database from a newer app version.
- Each app release declares `minEngine`, the lowest stock engine major it supports. The updater refuses a release the installed engine cannot run, and the Engines screen says so (`ui/src/appupdate.ts`).

## Channels

- Stable: the default channel. Passes every gate. Promoted from Beta after 7 days without a blocker [INFERENCE: the period is my proposal].
- Beta: opt-in. Passes automated gates. May have known issues. Settings shows a caution.
- Nightly: opt-in. Built from the main branch each day. No manual gate. Settings shows a stronger caution.
- A user can skip one version and can roll back one version.

## Release signing key

- One Ed25519 key pair. The private key is `~/.ladilala-release/updater.key` on the release Mac (directory mode 700, file mode 600). It is outside the repository. `.gitignore` excludes `*.key` and `dist-release/`.
- **The user must copy the private key to an offline place now** (password manager or an encrypted drive). If the key is lost, no installed copy can ever accept an update again: every user would have to install by hand a build with a new public key. If the key leaks, anyone can sign an update.
- The public key is `app/updater.pub`, embedded in the exe. SHA-256 of that file: `9f7688c6a5c34aa3d3d41be5286d8b7a6353886237b744eb0c29a68f2e3800a0`.
- Never copy the private key to winpc, to the repository or to CI. The Wails CLI is installed outside the repo: `cd /tmp && GOBIN=$HOME/.ladilala-release/bin go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0` (inside the repo module it fails on missing go.sum entries).
- Key rotation: ship one release signed with the old key that carries the new public key, then switch. [INFERENCE] The updater has one pinned key, so this needs a build that holds the new key and is signed with the old one. Plan it before the first public release (threat model T-14).

## Build steps (Windows, winpc)

1. On the Mac: `cd ui && bun run build`, then copy `ui/dist` to `app/dist` (the build script copies `ui\dist` to `app\dist` itself, so `ui\dist` must exist on the Windows machine).
2. Sync the repo to winpc without `.git`, `node_modules` and `ui/src`. On the Mac set `COPYFILE_DISABLE=1` for `tar`, or macOS adds `._*` files and the build embeds a `._0001_init.down.sql` migration: the app then fails to start with "bad migration file name".
3. On winpc: `powershell -File scripts\build-windows.ps1 -Version <v> -Installer -SkipUi -Makensis <path to makensis.exe>` (Go 1.27.3 at `C:\tools\go\bin`, NSIS 3.11 per user). Outputs: `app\bin\ladilala.exe` and `ladilala-amd64-installer.exe`. Build 1.0.0 and 1.0.1 from the same source for an update test.
4. Copy both files to the Mac.
5. `go test ./core/... ./app/...`, the E2E gates in `docs/test-strategy.md`, the detector pages by a person.
6. Generate `THIRD_PARTY_NOTICES.txt` from a licence scan (not done yet).
7. Install on a clean Windows 10 and a clean Windows 11 machine. The silent install and uninstall passed on winpc (Windows 11, WebView2 present, see "Verified" 10). The WebView2-missing path was checked by tests and by reading only (item 12). Windows 10 is untested.

Do not commit binaries. `.gitignore` excludes `*.exe`, `/bin/` and `dist-release/`.

## `scripts/release.sh`

```
scripts/release.sh <version> <ladilala.exe> <setup.exe> [notes-en] [notes-vi]
```

- `<ladilala.exe>` is the plain exe from the build. `<setup.exe>` is the NSIS installer. The notes are text or a file path.
- Environment: `CHANNEL` (default `stable`), `MIN_ENGINE` (lowest stock Chrome major, default `0` = none), `NEWS` and `FIXED` (items split by `|`), `URL_BASE` (default `https://ladilala.71.zone`), `KEY`, `WAILS3`.
- It refuses to overwrite `dist-release/<version>/`. A version number is never reused.
- It runs `wails3 updater manifest` with the private key, adds the `metadata` block, runs `wails3 updater verify` against `app/updater.pub`, and prints the upload order. It does not upload.

Manifest format (Wails Update Manifest, `schemaVersion` 1, read by `app/appupdate_service.go`):

```
{ "schemaVersion": 1, "version": "1.0.1", "channel": "stable", "name": "Ladilala 1.0.1",
  "notes": "<English>\n\n---\n\n<Vietnamese>", "publishedAt": "<RFC 3339>",
  "artifacts": [ { "url": "https://ladilala.71.zone/1.0.1/ladilala-1.0.1-windows-amd64.exe",
      "platform": "windows", "arch": "amd64", "size": 26843648,
      "digestAlgo": "sha512", "digest": "<base64>",
      "signatureAlgo": "ed25519ph", "signature": "<base64>" } ],
  "metadata": { "minEngine": 0, "news": ["..."], "fixed": ["..."] } }
```

The signature covers the artifact digest, not the manifest. The Wails CLI writes no `metadata`, so the script adds it. The filename must contain `windows` and `amd64`: the updater picks the artifact from them. The manifest has one `channel`. [INFERENCE] Beta and Nightly need their own manifest object, for example `beta/manifest.json`, and the user sets the feed URL to it; this is not built yet.

## Publish the feed

`dist-release/` layout after `scripts/release.sh 1.0.1 ...`:

```
dist-release/1.0.1/ladilala-1.0.1-windows-amd64.exe         upload as 1.0.1/ladilala-1.0.1-windows-amd64.exe
dist-release/1.0.1/ladilala-1.0.1-windows-amd64-setup.exe   upload as 1.0.1/ladilala-1.0.1-windows-amd64-setup.exe
dist-release/1.0.1/manifest.json                            upload LAST as manifest.json at the bucket root
dist-release/1.0.1/SHA256SUMS.txt                           keep, do not upload
```

1. Upload the exe. 2. Upload the setup. 3. Upload `manifest.json`, last, in one put (an R2 object put is atomic). Do not rename the exe: the manifest URL points at it.
4. Check: `curl -s https://ladilala.71.zone/manifest.json`, then download the exe by the manifest URL and compare its SHA-512 (base64) with `digest`.
Set a short cache time on `manifest.json` and a long one on the versioned files. [INFERENCE] Not set up yet.

## Rollback

- Bad release found: upload the manifest of the last good version again. **Installed copies do not go back**: the updater offers only a newer version, so users who already took the bad version stay on it. To fix them, ship a new version number (for example 1.0.2) that has the fix or the old code.
- Pull a release: re-upload the previous manifest. Never reuse a version number. Do not delete the files of a released version while any manifest points at them.
- The client keeps the previous exe as `ladilala.exe.old.<number>` in the install folder after a swap. [INFERENCE] A rollback button using it is not built. Settings, Roll back is not wired to it.
- The database is not downgraded. Keep each release able to read the database of the previous one for one version. This rule is not designed yet.
- Engines roll back separately. The Engines screen keeps one previous version.

## Known limits

- No Authenticode: the updater verifies Ed25519 and SHA-512 only, and SmartScreen warns on the first download of the setup.
- The version in the exe file properties and in the NSIS uninstall entry comes from the `.syso` and the installer define, not from `-ldflags`. The update tests below read the version from the app (Settings). Both builds show `1.0.0` in file properties.
- Chrome for Testing downloads are not signed and have no published checksum. The app records a SHA-256 on first download and checks its own manifest (`docs/stock-engine.md`).
- Install on quit needed a code fix (see "Verified").
- A rejected update (changed bytes or a bad signature) shows the message "invalid signature" in both cases. The log of a digest failure says `digest mismatch`.

## Open decisions
Authenticode certificate type, issuer and cost (see `docs/wails-gate.md`). Beta and Nightly feed layout. Stable promotion rule. Telemetry and crash reports (vault stance: default off, still open). Changelog format and language ([INFERENCE] Vietnamese and English).

## Checked against

`go.mod` (Wails v3.1.0), the Wails updater source (`pkg/updater`, `internal/commands/updater_tool.go`), `app/appupdate_service.go`, `app/main.go`, `scripts/build-windows.ps1`, `docs/wails-gate.md`, vault ADR 0001, 0011 and 0020.

## Verified

Date 2026-11-17. Machine winpc, Windows 11, per-user folder `%USERPROFILE%\ladilala-release\install`, instance `rel`, own data folder. The feed was served from the Mac over HTTP (the manifest URLs were rewritten to the LAN address; the signature covers the artifact, not the manifest). The user's own install and data were not touched.

1. Both installers (1.0.0, 1.0.1) built on winpc with the new public key and the default feed. Exe 26.8 MB, setup 12.2 MB.
2. `scripts/release.sh 1.0.1` wrote a signed manifest. `wails3 updater verify` passed: digest and Ed25519 signature OK. SHA-512 was also checked with Python.
3. Update 1.0.0 to 1.0.1 with "Update now" then "Restart to update": passed. The exe hash changed, the app restarted and Settings shows 1.0.1.
4. Install on quit (close the window with an update ready): **failed on the first build**. The hand-off ran inside the Wails shutdown, `Updater.Restart` called `App.Quit` there, the process never exited and the helper gave up after 30 s and relaunched 1.0.0. Fixed: `AppUpdateService.BeforeQuit` is now a window-close hook and the `ShouldQuit` hook. After the fix: swap in about 1 s, the app restarted as 1.0.1.
5. Changed installer bytes under a valid manifest: rejected, 1.0.0 kept (exe hash unchanged).
6. Valid digest with a changed signature: rejected, 1.0.0 kept.
7. A manifest from the feed override variable `LADILALA_UPDATE_URL` works over plain HTTP. The Settings field accepts only https or loopback.
8. Unit test `TestBeforeQuitHandsOffReadyUpdate` covers the hand-off hook.
9. Real feed, 2026-11-17: the same 1.0.0 build, started with no `LADILALA_UPDATE_URL` (built-in default feed `https://ladilala.71.zone/manifest.json` over HTTPS from R2). The UI found 1.0.1, "Update now" downloaded and verified it, "Restart to update" swapped it. Settings shows 1.0.1 and the exe SHA-256 is `bf262d42c4c692c37ea2f07bb0315e686dee700a26e84c8795c07cac29f5c2a0`, equal to `dist-release/1.0.1/ladilala-1.0.1-windows-amd64.exe` and to a direct download of the published file. Install on quit against the real feed: passed too (swap in about 1 s, the app restarted as 1.0.1, same hash).
   Not verified: Windows 10, a Beta or Nightly feed, a run active during install on quit on Windows (unit test only), the installer and the app with WebView2 truly absent (see item 12).
10. NSIS silent install of the current build, 2026-11-17, winpc. Built with the `ship-remote.ps1` flow as version 1.9.9-insttest, run as `setup /S /D=%USERPROFILE%\ladilala-release\nsis-test`. Exit code 0. Before/after listings (HKCU Uninstall, HKCU Software and Software\Classes subkey names, Start Menu tree, user and public Desktop, `%LOCALAPPDATA%\Programs`, `%TEMP%\ns*`) differ by exactly two entries:
    - Registry `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\LadilalaLadilala` (Publisher, DisplayName, DisplayVersion 1.9.9, DisplayIcon, UninstallString, QuietUninstallString, EstimatedSize).
    - `%APPDATA%\Microsoft\Windows\Start Menu\Programs\Ladilala.lnk`, target the test folder.
    - Files in the install folder: `ladilala.exe` (SHA-256 equal to the built exe) and `uninstall.exe`. No desktop shortcut, no file association, no protocol key.
    - Silent uninstall (`uninstall.exe /S`): the folder, the key and the shortcut are gone, and the listing equals the "before" listing.
    - **Shared names:** the uninstall key name and the shortcut name are fixed (`LadilalaLadilala`, `Ladilala.lnk`), not derived from `/D`. On this run winpc had neither (the user's app runs from `LadilalaDemo`, not from this installer), so nothing was overwritten. If a user has a real install, a second setup with another `/D` overwrites both and its uninstaller deletes both. This is by design for a per-user install, not changed.
    - The installed exe started in the console session (scheduled task, own `LADILALA_DATA` and instance `insttest`): window title `Ladilala [insttest]`, one process from the test path. Stopped by PID. The user's `ladilala.exe` (LadilalaDemo) and its data were not touched.
11. Player `Step.disabled`. The player skips disabled steps and `Preflight` does not evaluate them (existing unit test). Gaps found and fixed: (a) a variable declared required but read only by disabled steps still failed the run (`BuildVars`); (b) `data.TemplateColumns` put such variables in the Excel template; (c) the step preview in `MacroService` reported `missingColumn` for a disabled step. New tests: `TestBuildVarsIgnoresRequiredVariableOfDisabledStepOnly`, `TestTemplateColumnsSkipDisabledSteps`, `TestE2EPlayerDisabledStep` (Mac Chrome: the disabled step is not played, the page gets an empty City, the other steps run). Validation still checks a disabled step like any other (a bad reference stays an error, so a step cannot be enabled later in a broken state). The run engine has no per-step counter beyond the player hook, which is not called for skipped steps. [INFERENCE] The UI has no control to disable a step yet (`disabled` is only in the TypeScript type), so no UI text was checked.
12. WebView2 missing path. Read, not run end to end. Installer (`wails.webview2runtime`): if no `pv` value in HKLM WOW6432Node or HKCU, it runs the embedded Microsoft bootstrapper `/silent /install`. Gaps fixed: `pv` of `0.0.0.0` (left by an uninstall) counted as installed; a failed bootstrapper (no network) was silent. Now `0.0.0.0` counts as missing and a non-zero exit code writes a line in the install log (not shown with `/S`). App start (`ensureWebView2` in `app/platform_windows.go`): with no runtime it shows "Ladilala needs Microsoft Edge WebView2", offers to download and run Microsoft's bootstrapper, and on "No", on download failure or when the runtime is still absent it shows an error with the Microsoft download page. Tests: `TestVersionAt` (fake keys below `HKCU\Software\LadilalaTest`, never the real EdgeUpdate keys) and `TestEnsureWebView2Messages` (dialog, installer and version are passed in), both passed on winpc. WebView2 was not uninstalled. Not verified: the dialogs on screen, the bootstrapper on a PC without WebView2, the installer built with the new NSIS lines on such a PC (it was built and ran on a PC that has the runtime), Windows 10.
10. Public key SHA-256: `9f7688c6a5c34aa3d3d41be5286d8b7a6353886237b744eb0c29a68f2e3800a0`.
