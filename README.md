# Ladilala

Ladilala is a Windows-first desktop app. It has two parts.

- A browser profile manager. Each profile is isolated. Each has its own proxy and device identity (fingerprint).
- A web macro recorder and player. Record a task once. The player repeats it for each row of your data (CSV, Excel, JSON, REST, Google Sheets).

The old internal name was WinMacro. The Go module is `github.com/8bu/ladilala`. Legal clearance of the name is open.

## Status

Early work. Read `docs/` before you trust any claim here.

| Part | State |
|---|---|
| Macro schema, loader, validator (`core/macro`) | Done, unit-tested |
| Profile and fingerprint model (`core/profile`) | Done. Geo from proxy IP is not built |
| Proxy relay (`core/proxy`) | Done, tested with fake upstreams only |
| Browser driver, raw CDP client, Job Object (`core/driver`), injector (`core/inject`) | Done, measured on Windows |
| Recorder and player (`core/recorder`, `core/player`) | Spike-proven on Chrome and Edge |
| Data sources, storage, run engine (`core/data`, `core/store`, `core/engine`) | In progress |
| Wails shell, update manager (`app`) | Not started |
| UI (`ui`) | Throwaway prototype with mock data |

## Layout

| Path | Holds |
|---|---|
| `core/macro`, `core/profile`, `core/proxy` | Macro schema v1. Profile and fingerprint types. Loopback relay per profile that fails closed |
| `core/driver`, `core/inject` | Launcher, Job Object, raw CDP client, stock Chrome and Edge, Chrome for Testing lookup. Injector script and its NOTICE |
| `core/recorder`, `core/player` | Record and replay |
| `core/data`, `core/store`, `core/engine` | Data sources, SQLite storage, run engine |
| `cmd/winspike`, `cmd/recspike` | Spike tools for Windows checks |
| `ui` | Vue 3 and shadcn-vue prototype. Vietnamese default, English second |
| `docs`, `scripts`, `app` | Build-dependent documents. Helper scripts. The Wails shell goes in `app` (empty) |

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

UI prototype:

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
- `ui/PROTOTYPE.md`, `ui/STYLING.md`: prototype and design notes.
