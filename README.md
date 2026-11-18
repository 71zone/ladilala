# Ladilala

Ladilala is a Windows-first desktop app. It has two parts.

- A browser profile manager. Each profile is isolated. Each has its own proxy and device identity (fingerprint).
- A web macro recorder and player. Record a task once. The player repeats it for each row of your data (CSV, Excel, JSON, REST).

The old internal name was WinMacro. The Go module is `github.com/8bu/ladilala`.

## Download

Version 1.1.0 for Windows 64-bit. Get the files from the [latest release](https://github.com/71zone/ladilala/releases/latest).

- `ladilala-1.1.0-windows-amd64-setup.exe`: the installer. Use it for a first install.
- `ladilala-1.1.0-windows-amd64.exe`: the portable program. The in-app updater replaces this file.
- `SHA256SUMS.txt`: the checksums of the files.

The files are not code-signed, so Windows SmartScreen can warn. Check the file before you run it.

- SHA-256 of the portable exe: `4e8db0d2e8350dea35cef32c2eade86702f3b783d8e75904936236bb54c23164`
- [VirusTotal scan of the portable exe](https://www.virustotal.com/gui/file/4e8db0d2e8350dea35cef32c2eade86702f3b783d8e75904936236bb54c23164): 2 of 71 engines flagged it (Bkav Pro and Trapmine, both machine-learning rules). 69 engines found nothing. These are likely false positives, but we cannot prove that.

## Status

Version 1.1.0 is released. Windows 10 is untested. Read `docs/` before you trust any claim here.

| Part | State |
|---|---|
| Macro schema, loader, validator (`core/macro`) | Done, unit-tested |
| Profile and fingerprint model (`core/profile`) | Done. Geo from proxy IP is not built |
| Proxy relay (`core/proxy`) | Done, tested on Windows |
| Browser driver, raw CDP client, Job Object (`core/driver`), injector (`core/inject`) | Done, measured on Windows |
| Recorder and player (`core/recorder`, `core/player`) | Done, tested on Chrome, Edge and fingerprint-chromium 148 |
| Data sources, storage, run engine (`core/data`, `core/store`, `core/engine`) | Done. Google Sheets is not built |
| Wails shell, update manager (`app`) | Done. Updates come from `ladilala.71.zone` |
| UI (`ui`) | Done. Vietnamese default, English second |

## Layout

| Path | Holds |
|---|---|
| `core/macro`, `core/profile`, `core/proxy` | Macro schema v1. Profile and fingerprint types. Loopback relay per profile that fails closed |
| `core/driver`, `core/inject` | Launcher, Job Object, raw CDP client, stock Chrome and Edge, Chrome for Testing lookup. Injector script and its NOTICE |
| `core/recorder`, `core/player` | Record and replay |
| `core/data`, `core/store`, `core/engine` | Data sources, SQLite storage, run engine |
| `cmd/winspike`, `cmd/recspike` | Spike tools for Windows checks |
| `ui` | Vue 3 and shadcn-vue interface. Vietnamese default, English second |
| `docs`, `scripts`, `app` | Build-dependent documents. Helper scripts. The Wails shell lives in `app` |

## Build and test

Mac and Windows use the same commands. Go version in `go.mod`: 1.26.2.

```
gofmt -l .
go build ./...
go vet ./...
go test ./core/...
GOOS=windows go vet ./...     # checks the Windows code from a Mac, runs nothing
```

Browser E2E tests need a real Chrome or Edge. They are off by default.

```
LADILALA_E2E=1 go test ./core/driver -run TestE2EStockChrome
```

On Windows, do not use `-race`. It needs cgo.

UI:

```
cd ui
bun install
bun run proto          # http://localhost:5190
bun run build          # type check and bundle
bun run i18n:check     # key parity of vi.json and en.json
```

## Documents

- `docs/dev-setup.md`: toolchains, the Windows test PC, the worker workflow.
- `docs/test-strategy.md`, `docs/threat-model.md`, `docs/api-contract.md`.
- `docs/macro-schema.md`, `docs/stock-engine.md`, `docs/recorder-spike.md`, `docs/windows-spikes.md`: format and measured results.
- `docs/licences.md`, `docs/release-process.md`.
- `docs/acceptable-use.md`: three options. The user has not decided.
- `ui/PROTOTYPE.md`, `ui/STYLING.md`: UI and design notes.
