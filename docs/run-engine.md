# Run engine

Code: `core/engine` (run orchestration), `core/store` (SQLite, ledger).
Rules come from ADR 0014. Tables are in `data-model.md`.

## What a run is

A run is one macro, a copy of the data rows, a pool of profiles and options.
`CreateRun` copies the macro body and every row of the range into the ledger.
Later edits of the macro or the source never change a run.
Each row has the key `<runId>:<rowIndex>` (idempotency key).
`rowIndex` is the 0-based row number in the source.

Options: `concurrency` (1), `delayMs` (0), `stopAfterErrors` (5),
`maxRestarts` (3), `dryRun`, `testOne`.
Dry run skips every commit point step. Test one runs the first row only.

## Run states

| State | Meaning |
|---|---|
| created | Rows are in the ledger. No browser started. |
| running | Workers claim rows and play the macro. |
| paused | No new step starts. Browsers stay open. |
| takeover | The user controls the browsers. Same gate as paused. |
| stopping | Stop requested. Workers settle their rows and leave. |
| finished | No pending row is left, or the user stopped the run. |
| failed | No browser can start and rows are still pending. |
| crashed_recovered | The process died. Recover settled the rows. Nothing runs. |

| From | To | Trigger |
|---|---|---|
| created | running | StartRun |
| running | paused | PauseRun, stopAfterErrors reached, guard needsLogin or pause |
| running, paused | takeover | Takeover, or a manual step |
| paused | running | ResumeRun |
| takeover | running | ReleaseTakeover (guards must pass) |
| running, paused, takeover | stopping | StopRun |
| stopping | finished | last worker left |
| running | finished | all rows settled |
| running | failed | no browser can start, rows pending |
| running | paused | the app closed (Service.Close) |
| failed, crashed_recovered, paused (no workers) | running | ResumeRun |
| running, paused, takeover, stopping (after restart) | crashed_recovered | Recover |
| finished, failed, crashed_recovered | paused | RetryRows reset at least one row |
| created, paused, failed, crashed_recovered | finished | StopRun without workers |

Other calls fail with `*StateError` (matches `ErrBadState`).

## Row states

| State | Meaning |
|---|---|
| pending | Waits for a worker. |
| running | A worker holds it. It has a profile and an attempt number. |
| done | The macro ended without error. |
| failed | An error before the commit point. Retry is safe. |
| skipped | The row never reached a browser: missing column, bad value, URL outside allowedOrigins, or the source could not read it. |
| uncertain | The row passed the commit point and did not end clean. Never retried. |

Commit phase (separate column): `none`, `submitting`, `submitted`.
The engine writes `submitting` before the input of a commit point step
reaches the page. The write is durable when it returns (`synchronous=FULL`).
It writes `submitted` when that step ended without error.
A step that has started its commit is never retried by the player.

| Event | Row result |
|---|---|
| Macro ends clean | done |
| Error, commit phase `none` | failed (kind: locator, origin, data, guard, timeout, step, ledger) |
| Error, commit phase `submitting` or `submitted` | uncertain |
| Stop or browser death, phase `none` | back to pending |
| Stop or browser death, phase not `none` | uncertain |
| Guard needsLogin or pause, phase `none` | back to pending, run paused |
| Process crash, phase `none` | pending (at Recover) |
| Process crash, phase not `none` | uncertain (kind `crash`, at Recover) |

Rules:
- `RetryRows` resets failed and skipped rows only. It never touches uncertain rows.
- The user decides an uncertain row with `MarkUncertainRow`: done (the submit happened) or failed (it did not, the row can be retried).
- `stopAfterErrors` counts failed rows in a row. A done row resets it. Skipped and uncertain rows do not count.

## Workers

One worker per profile, `concurrency` workers at a time.
Spare profiles (pool larger than concurrency) replace a profile whose browser
cannot be kept alive.
A worker loops: wait at the gate, claim the lowest pending row in one SQL
statement, run the row preflight, start the browser if needed, play the
macro on a new tab, close the tab, write the result.
The ledger records the profile of each row, so a row runs on one profile only.
A dead browser is restarted up to `maxRestarts` times per profile.
`allowedOrigins` is enforced by the player (preflight and every action).

Pause and takeover work at step boundaries (`player.Hooks.BeforeStep`).
`PauseRun` and `Takeover` return when every worker waits, or after 15 seconds.
Resume is immediate. A browser that is mid-step finishes that step first.
`ReleaseTakeover` runs the macro guards on each page in progress
(`player.CheckGuards`). If a guard fires, the run stays in takeover and the
error is `*GuardFiredError`.

Manual steps (`action: manual`) put the run in takeover with the instruction
as reason. `ReleaseTakeover` continues the row.

## Crash recovery

`Service.Recover(ctx)` runs once at app start.
For each run left in running, paused, takeover or stopping:
1. Rows in state running with commit phase `none` go to pending.
2. Rows in state running with another commit phase become uncertain.
3. The run becomes `crashed_recovered`.

Nothing resumes by itself. The user calls `ResumeRun`.
Pending rows continue. Uncertain rows wait for `MarkUncertainRow`.

## Events

`Subscribe(ctx, runID)` returns a buffered channel (256 events).
Publishing never blocks: a slow reader loses its oldest events and the next
event carries `Dropped`. Every event is also appended to `run_events`.
Kinds: `run.state`, `row.started`, `row.commit`, `row.done`, `row.failed`,
`row.skipped`, `row.uncertain`, `row.released`, `row.resolved`, `browser`.

## Service API (package `engine`)

```go
func NewService(st *store.Store, l Launcher) *Service
func (s *Service) Recover(ctx context.Context) ([]Recovery, error)
func (s *Service) Close(ctx context.Context) error

func (s *Service) CreateRun(ctx context.Context, req CreateRunRequest) (Run, error)
func (s *Service) StartRun(ctx context.Context, id string) error
func (s *Service) PauseRun(ctx context.Context, id string) error
func (s *Service) ResumeRun(ctx context.Context, id string) error
func (s *Service) StopRun(ctx context.Context, id string) error
func (s *Service) Takeover(ctx context.Context, id string) error
func (s *Service) ReleaseTakeover(ctx context.Context, id string) error
func (s *Service) RetryRows(ctx context.Context, id string, f RowFilter) (int, error)
func (s *Service) MarkUncertainRow(ctx context.Context, id string, index int, res Resolution) error
func (s *Service) ListRuns(ctx context.Context, f RunFilter) ([]Run, error)
func (s *Service) GetRun(ctx context.Context, id string) (Run, error)
func (s *Service) ListRows(ctx context.Context, id string, f RowFilter) (RowPage, error)
func (s *Service) Subscribe(ctx context.Context, id string) (<-chan Event, error)

type CreateRunRequest struct {
    MacroID    string      // macro saved in the store
    Source     data.Source // read once; the caller closes it
    SourceID   string      // optional: saved source config
    From, To   int         // row range [From,To), To < 0 means to the end
    ProfileIDs []string    // pool, profiles saved in the store
    Options    RunOptions
}
type RowFilter struct { States []RowState; Indexes []int; Offset, Limit int }
type RunFilter struct { States []RunState; Offset, Limit int }
type Resolution string // ResolveDone, ResolveFailed
```

`Run`, `Row`, `RowPage`, `Event`, `Recovery` and the state constants are in `core/engine/types.go`.
`Run.Counts` has the number of rows per row state.
`Row.Data` is the copy of the source row. It can hold passwords. Show it with care.

Typed errors: `ErrRunNotFound`, `ErrBadState` (`*StateError`), `ErrNoRows`,
`ErrNoProfiles`, `ErrNotEnoughProfiles`, `ErrNotUncertain`, `ErrBrowserDied`,
`*GuardFiredError` (wraps `player.ErrGuard`), `store.ErrNotFound`.

`Launcher` and `Browser` are interfaces. `StockLauncher` is the real one:
core/driver stock path (raw CDP, no `Runtime.enable`), proxy relay for
profiles with a proxy, one kept tab, one new tab per row.

## Changes in core/player

- `Player.Hooks`: `BeforeStep`, `BeforeCommit`, `AfterCommit`, `Manual`, `DryRun`.
- `Player.CheckGuards`: evaluates `macro.Guards`. `Run` now checks them before each step.
- A step with `BeforeCommit` done is not retried and is not skipped by `onFail: skip`.

## Deviations

1. **Pull, not round-robin.** ADR rule 5 says rows go round-robin. Workers claim the next row when free. A slow profile then does not hold rows back. Each row still runs on one profile.
2. **Pause waits for the step.** The engine does not cancel a step in progress. A pause can take up to the step timeout (10 s default, 30 s for navigation). `PauseRun` returns after 15 s either way. Resume is immediate.
3. **Error limit pauses, it does not fail.** After `stopAfterErrors` failed rows the run is paused, so the user can fix the cause and resume. State `failed` is only for runs that cannot start a browser.
4. **Any error after the commit phase starts is uncertain.** This includes errors after `submitted`, for example a later navigation. The engine cannot know whether the user wants a second submit.
5. **Secrets.** ADR 0013 proposes Windows Credential Manager (go-keyring). The build uses DPAPI (`CryptProtectData`, current user) with sealed blobs in `secrets.dpapi.json`. It needs no extra module. On macOS and Linux the dev build writes `secrets.insecure.json` as plain base64. It is not encryption.
6. **Row data is plain text.** The ledger copy of the data (`run_rows.data`) is not encrypted. A password column in a CSV is stored as it is. Mitigation for a later step: per-column masking or encryption, and cleanup after the run.
7. **Rows are read into memory at `CreateRun`.** Very large sources need a streaming insert.
8. **Browsers after a crash.** On Windows the Job object kills them. On macOS a browser can stay open and keep its profile directory locked until closed.
9. **Windows code is not run here.** DPAPI and the Windows build were checked with `GOOS=windows go vet` and `go build` only.
10. **Guards are new in the player.** Before this change the player did not evaluate `macro.Guards`.

## Tests

`go test -count=1 ./core/store ./core/engine` uses a real SQLite file in a temp
folder and a fake browser. The real browser test needs Chrome:
`LADILALA_E2E=1 go test -count=1 -v -run TestE2ERunEngine ./core/engine/`
(records the macro on the local test pages, runs 10 rows on 2 profiles,
checks the site and the ledger).
