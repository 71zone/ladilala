# Dev setup

## Toolchains

| Tool | Mac (dev PC) | winpc (Windows test PC) |
|---|---|---|
| Go | 1.26.2 (`go.mod` says `go 1.26.2`) | 1.27.3 at `C:\tools\go\bin` |
| bun | 1.3.14 | not used |
| Node | 26.3.0. Optional. One injector test runs in Node and skips if Node is missing | not needed |
| Chrome | `/Applications/Google Chrome.app` | `C:\Program Files\Google\Chrome\Application\chrome.exe` |
| Edge | not used | `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe` |

Go toolchain policy is open (vault ADR 0018). On winpc, put `C:\tools\go\bin` first in `PATH`. An old Go 1.17 in Program Files must not be used.

The UI needs bun only. Wails is not in `go.mod`. No Wails toolchain is needed yet.

## First build

```
go build ./... && go vet ./... && go test ./core/...
cd ui && bun install && bun run build
```

## UI prototype

```
cd ui
bun run proto
```

- It serves on port 5190 with `--host 0.0.0.0`. Open `http://localhost:5190/`.
- All data is mock data in memory. There is no backend.
- Use the Simulate menu (sidebar footer) to trigger engine and app update failures.
- `bun run i18n:check` must pass after any text change. Terms are in `docs/i18n-glossary.md`.

## Tests

| Goal | Command |
|---|---|
| Unit tests, Mac | `go test ./core/...` |
| Windows compile check from Mac | `GOOS=windows go vet ./...` |
| Windows E2E (on winpc) | `LADILALA_E2E=1 go test ./core/driver -run TestE2EStockChrome` |
| Recorder and player E2E | `LADILALA_E2E=1 go test -count=1 -v -run 'TestE2ERecord\|TestE2EPlayer' ./core/recorder/ ./core/player/` |
| Use Edge in E2E | add `LADILALA_E2E_BROWSER=edge` |
| Detector pages | `go run ./cmd/winspike stockdetect` (winpc) |

`LADILALA_E2E=1` with `./core/...` also runs old fingerprint-chromium tests. They time out. Always name the package and the test. See `docs/test-strategy.md`.

Environment variables seen in code: `LADILALA_E2E`, `LADILALA_E2E_BROWSER`, `LADILALA_FPC_DIR` (old engine folder), `LADILALA_DATA` (data folder override, `core/store/store.go`).

## winpc

winpc is a Windows PC that sleeps. It runs all Windows checks.

- The full runbook is the vault note `engineering/winpc-runbook.md`. Read it before first use. This file holds no secrets and no keys.
- Short form: wake it with `wake-winbox`, wait for SSH on port 22, use key auth, copy the repo as a tarball, run tests, kill leftover browsers.
- One worker uses winpc at a time.
- Use `ssh` and `scp` inside one `eval` cell. Print only result lines.

## Worker and orca workflow

1. The orchestrator `orca-main` writes one brief per work package in `.orca/briefs/`.
2. The first line of a brief is an `export` line. The worker runs it first. It sets the signal file.
3. A worker uses no `task` tool and no subagent.
4. A worker uses `eval` for multi-step work to save quota.
5. A worker edits only the files its brief names.
6. A worker runs only the checks the brief lists.
7. A worker sends `orca-signal progress` at milestones and `orca-signal done` at the end.
8. The last action is one ping: `herdr agent prompt orca-main "orca-wake: <id> <done|blocked> <result>"`.
9. Nobody commits unless the brief says so. The repo has no commits yet.
10. The orchestrator verifies the result itself. Vault note: `engineering/worker-process.md`.

## Checked against

`go.mod`, `ui/package.json`, `ui/PROTOTYPE.md`, `core/inject/inject_test.go` (Node skip), `core/driver/*_test.go` (env names), `docs/recorder-spike.md`, `.orca/playbooks/ladilala-task.md`, vault `engineering/winpc-runbook.md`. Tool versions came from `go version`, `bun --version` and `node --version` on the Mac. winpc versions come from the vault runbook. [INFERENCE] The winpc versions are not re-checked today.
