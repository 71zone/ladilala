# Test strategy

Status date: 2026-11-16. This file owns the repo side: commands, gates and targets. The vault note `engineering/test-strategy.md` owns the reasons.

## Pyramid

| Layer | What it checks | Where it runs | Speed |
|---|---|---|---|
| 1. Unit tests | Pure Go logic. No browser, no network, no Windows API | Mac and CI | Seconds |
| 2. Component tests with fakes | Relay with fake upstreams. CDP framing with a fake pipe. Data sources with temp files | Mac and CI | Seconds |
| 3. Browser E2E | Real Chrome or Edge. Launcher, Job Object, stock launch, recorder, player | winpc, later a Windows CI runner | Minutes |
| 4. Detector gate (manual) | Detector pages against injected and bare browsers | winpc, by a person | 10 minutes |
| 5. UI checks | `vue-tsc`, `vite build`, `i18n:check`, later component tests | Mac and CI | Seconds |
| 6. Release gate | Signed installer on a clean Windows PC. Update and rollback | Clean VM or PC | Manual |

Most tests sit in layers 1 and 2. Layer 3 stays small. Each E2E test must catch a bug that layers 1 and 2 cannot.

## Mac and winpc

| Task | Mac | winpc |
|---|---|---|
| `gofmt -l .`, `go build ./...`, `go vet ./...` | yes | yes |
| `go test ./core/...` (no E2E) | yes | yes |
| `GOOS=windows go vet ./...` and `go build ./...` | yes (cross check) | not needed |
| `LADILALA_E2E=1` tests | Mac Chrome only, for recorder and player | yes, all |
| Job Object, registry policy, named pipes, lockfile | no (build tags skip them) | yes |
| Detector pages | no | yes |
| `-race` | yes | no (no cgo) |
| UI build and i18n check | yes | no |

Files with `_windows` in the name build only on Windows. A Mac run does not test them. The cross compile only proves they build. `core/driver/job_e2e_windows_test.go` is the real test of the Job Object. `app/platform_windows_test.go` (WebView2 detection and start messages) runs only on winpc: `go test -run 'TestVersionAt|TestEnsureWebView2' ./app`.

## E2E gates

Gates a change must pass before the orchestrator accepts it:

1. `TestE2EStockChrome` and `TestE2EEdge` pass on winpc. `TestE2EStockChrome` asserts the client never sends `Runtime.enable`, `Runtime.addBinding`, `Console.enable` or `Debugger.enable`. For recorder and player tests, `LADILALA_E2E_BROWSER=edge` selects Edge.
2. `TestE2ECanvasVerified` passes. It runs the canvas read 20 times over fresh launches.
3. `TestE2EJobKillsBrowserOnAppDeath` passes. No orphan browser stays after the app is killed.
4. `TestE2ERecord*` and `TestE2EPlayer*` (including `TestE2EPlayerDisabledStep`) pass for recorder and player changes.
5. 20 fresh launches per engine with `winspike stocklaunches`: 0 failures. Needed when launch flags or the injector change.
6. Run engine: a 1000-row run is killed at a random step. Resume in under 15 seconds. No row submits twice. Not written yet, because the engine is in progress.

Run E2E tests by name and by package. `go test ./core/...` with `LADILALA_E2E=1` also runs the old fingerprint-chromium tests. They time out and the engine is rejected.

## Fixtures and local test sites

- `core/recorder/testdata/*.html` (forms, iframes, canvas, dates, double click, delayed fields), served by `core/recorder/site.go` on two origins so cross-origin iframes work. `core/recorder/rectest` holds shared scenarios.
- `core/macro/testdata`: one valid macro and 15 invalid ones, one file per failure. `core/driver/testdata/cft-last-known-good.json`: frozen Chrome for Testing JSON.
- E2E tests never touch the internet. Detector pages are the only exception, and they are manual.
- Never use the default Chrome profile. `validateDataDir` blocks it. Use a temp folder per test.

## Detector pages (manual gate)

Run `go run ./cmd/winspike stockdetect` on winpc. It opens bot.sannysoft.com, the rebrowser bot detector, CreepJS and BrowserScan. Compare injected and bare runs.
Pass lines today: sannysoft 0 failed. Rebrowser checks (runtimeEnableLeak, workerConsistency, nativeTampering, platformMismatch, navigatorWebdriver, cdpBindingGlobals) all green. CreepJS headless and stealth 0 percent. CreepJS lies: two known (client rects, audio noise). BrowserScan bot detection: Normal.
Run it before every release and after any injector, flag or Chrome major change. A person reads the result. Do not keep raw page text in the repo. Record the table in `docs/stock-engine.md`. These sites change. A red result may be the site, not us. Re-run once before you act.

## Flake policy
- A test that fails once and passes on retry is a flake. Log it in `.orca/log.md` with date and name. Two flakes in 10 runs: fix it in the next work package or delete the test.
- No automatic retries in tests. The launch code already retries up to 4 launches by design (`MaxVerifyAttempts`). No fixed sleeps: wait for a condition with a timeout.
- A canvas read in the first 300 ms after launch returns noise even in plain Chrome. Tests must use `Verify`, not a raw read. Real-site tests are never gates.

## CI plan (not added)

GitHub Actions with two jobs. A Windows runner has no desktop session, so layer 3 on CI covers headless-safe tests only. Desktop-dependent checks (Edge WebGL, real-desktop gate) stay on winpc or a self-hosted runner. [INFERENCE] This matches the vault note, which says the same. It is not tested on a hosted runner.

```yaml
# .github/workflows/ci.yml  (sketch, do not add yet)
name: ci
on: [push, pull_request]
jobs:
  unix:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod }
      - run: test -z "$(gofmt -l .)"
      - run: go vet ./... && GOOS=windows go vet ./...
      - run: go test -race ./core/...
      - uses: oven-sh/setup-bun@v2
      - run: cd ui && bun install --frozen-lockfile && bun run build && bun run i18n:check
  windows:
    runs-on: windows-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod }
      - run: go build ./... ; go vet ./...
      - run: go test ./core/...
      - run: go test ./core/driver -run 'TestE2EJobKillsBrowserOnAppDeath|TestE2EStockChrome'
        env: { LADILALA_E2E: "1" }
```

Open: the hosted runner has Chrome and Edge. Whether E2E passes without a desktop is unknown. Fallback: a self-hosted runner on winpc.

## Coverage targets

These are targets, not measurements. Nobody has run `go test -cover` for this file.

| Package | Target | Note |
|---|---|---|
| `core/macro`, `core/profile` | 90 percent | Pure logic, determinism, coherence |
| `core/data`, `core/engine` | 85 percent | One test per source type and row error. State tables and crash recovery |
| `core/proxy` | 80 percent | Fake upstreams |
| `core/inject`, `app` (Wails services) | 70 percent | Injector rest runs in the Node vm and E2E. Services: test the mapping, not Wails |
| `core/player`, `core/recorder`, `core/driver` | 60 and 50 percent, plus E2E | Browser paths need E2E. Windows files need winpc |
| `ui` | type check and i18n gate | Component tests later. Prototype is throwaway |

## What never gets a unit test

- Generated code, `cdproto` types, Wails glue that only forwards a call, constants, getters, struct shapes.
- Text of UI strings (the i18n check covers key parity). Real third-party sites and detector scores (manual gates).
- Mock-only behaviour. `cmd/winspike` and `cmd/recspike` (tools, run by hand).

## Checked against

`core/driver/*_test.go` (test names), `core/driver/canvasverify.go` (`MaxVerifyAttempts`), `core/recorder/testdata`, `core/recorder/site.go`, `core/macro/testdata`, `core/player/player_e2e_test.go`, `docs/stock-engine.md`, `docs/recorder-spike.md`, `ui/package.json`, vault `engineering/test-strategy.md` and `winpc-runbook.md`. `GOOS=linux go vet ./core/...` ran and passed except for the unfinished `core/store`. No CI file exists in the repo.
