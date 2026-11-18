# Development

For contributors. The product overview is in the [README](../README.en.md).

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

- `dev-setup.md`: toolchains, the Windows test PC, the worker workflow.
- `test-strategy.md`, `threat-model.md`, `api-contract.md`.
- `macro-schema.md`, `stock-engine.md`, `recorder-spike.md`, `windows-spikes.md`: format and measured results.
- `licences.md`, `release-process.md`, `release-howto.md`: licences and releases (commits, changelog, release PR).
- `engines-fpc.md`, `m0-engine-matrix.md`: the fingerprint-chromium engine and its test results.
- `acceptable-use.md`: the options for the terms of use. Option A is chosen. The final text is not written.
- `../ui/STYLING.md`: UI and design notes.
