# UI-to-core API contract

Status date: 2026-11-16. Nothing in this file is built as a Wails service. The shell (`app/`) is empty and Wails is not in `go.mod`. The contract comes from three sources: what the UI prototype does (`ui/src`), what the Go packages already offer, and ADR 0014. The RunService section follows `docs/run-engine.md` and `core/engine`, which are built and tested.

## Rules

1. Each group below is one Wails service: a Go struct in `app/` with exported methods. Methods call `core/*`. They hold no business logic.
2. Every method takes `context.Context` first. Wails cancels it when the window closes.
3. IDs are opaque strings. The prototype uses numbers. `profile.Profile.ID` is already a string.
4. JSON is camelCase. Time is RFC 3339 UTC. Durations are integer milliseconds. Sizes are bytes.
5. Secrets never go to the UI. A DTO has `hasPassword bool`. The UI sends a password only to a `Set...Secret` method. Nothing reads it back.
6. A method that takes longer than 1 second returns a `jobId` at once. Progress arrives as events (see Events). `CancelJob(jobId)` stops it.
7. The Go core validates all input again. The UI check is a convenience.
8. The UI text is Vietnamese or English. The core returns codes, not text. The UI maps a code to a message (`ui/src/locales`).

## Error shape

Every method returns `(T, error)`. Wails delivers the error as this JSON in the message:

```json
{ "code": "proxy.auth", "message": "upstream refused credentials", "details": { "field": "password" }, "retryable": false }
```

| Code | Meaning | Source |
|---|---|---|
| `invalid` | Input fails validation. `details.problems` lists `{path, msg}` | `macro.ValidationError`, `profile.Validate` |
| `notFound` | ID unknown | `store.ErrNotFound` |
| `conflict` | Name taken, profile running, macro in use by a run | |
| `profile.locked` | Profile data folder is in use | `driver` lock probe |
| `profile.dataDir` | Data folder is invalid or is the default Chrome folder | `driver.ErrInvalidDataDir` |
| `engine.notFound` | Browser binary missing | `driver.ErrBinaryNotFound` |
| `engine.policyBlocked` | `RemoteDebuggingAllowed=0` | `driver.ErrDebuggingBlocked` |
| `engine.hang` | Canvas read hung or spoof not verified | `driver.ErrCanvasHang`, `ErrCanvasLeak` |
| `proxy.down` | Upstream not reachable. Start is refused | `proxy` relay |
| `proxy.auth` | Upstream answered 407 | `proxy.ErrUpstreamAuth` |
| `macro.origin` | Step leaves `allowedOrigins` | `player.OriginError` |
| `macro.missingColumn` | Row has no value for a variable | `player.MissingColumnError` |
| `data.format` | Bad file, encoding or ragged row | `core/data` |
| `data.auth` | REST or Sheets credentials refused | |
| `run.state` | Method not allowed in this run state. `params`: `{state, op}` | `engine.StateError` (`engine.ErrBadState`) |
| `run.notFound` | Unknown run id | `engine.ErrRunNotFound` |
| `run.noRows` | The row range has no rows | `engine.ErrNoRows` |
| `run.noProfiles`, `run.tooFewProfiles` | No profile given, or concurrency above the profile count | `engine.ErrNoProfiles`, `ErrNotEnoughProfiles` |
| `run.notUncertain` | `MarkUncertainRow` on a row that is not uncertain | `engine.ErrNotUncertain` |
| `run.guard` | Release refused: the page still fails a macro guard. The run stays in takeover | `engine.GuardFiredError` (wraps `player.ErrGuard`) |
| `run.browserDied` | The browser process ended | `engine.ErrBrowserDied` |
| `update.signature` | Signature or checksum check failed | planned |
| `engine.tooOld` | App release needs a newer engine than installed | planned |
| `disk.full`, `network` | Environment problems. `retryable` is true | |
| `internal` | Bug. Details go to the log, not the UI | |

## Events

Wails events, one-way, core to UI. Payload types are in the DTO section. The core drops an event rather than block when the UI is slow. State is always readable by a `Get` method.

| Event | Payload | Fired when |
|---|---|---|
| `profile.state` | `{profileId, state, err?, code?}` (`code` is the API error code of a failed start) | Start, stop, window closed by the user, failed start |
| `proxy.status` | `{proxyId, status, latencyMs?, ip?, country?, timezone?, language?}` | A test starts (`checking`) or ends, from `TestProxy` or `HealthCheck` |
| `job.progress` | `{jobId, kind, stage, pct, speedBps?, etaSec?, detail?}` | Download, engine install, app update, bulk work |
| `job.done` | `{jobId, ok, error?}` | A job ends |
| `record.step` | `{recordId, stepIndex, step}` | The recorder captured a step. The UI sets `steps[stepIndex]`: a double click replaces the click before it |
| `record.warning` | `{recordId, kind, stepIndex, locator?, message?}` | Canvas or file input seen. No step exists for it. `stepIndex` is where the missing step belongs |
| `record.state` | `{recordId, state}` | `recording`, `paused`, `stopped` (the user pressed Stop in the page overlay) or `closed` (the browser ended). The UI then calls `StopRecording` |
| `record.steps` | `{recordId, steps[]}` | A data column was renamed or deleted in the page overlay: the whole step list replaces the UI copy (a delete also drops the steps that used the column) |
| `record.hint` | `{recordId, closed, never}` | The user closed the hint card in the page; `never` after "never show again". The UI stores that choice |
| `run.progress` | `{runId, state, total, counts, resync?}` | Row events happened. At most 5 per second per run. `resync` is true when events were lost: reload the visible rows |
| `run.state` | `{runId, state, reason, at}` | The run state changed |
| `run.session` | `{runId, profileId, message, at}` | A browser started, died, or did not start |
| `run.log` | `{runId, lines[{at, kind, rowIndex, profileId, message}], skipped?}` | Batched log lines, same pace as `run.progress` |
| `run.uncertain` | `{runId, rowIndex, profileId, message, at}` | A row became uncertain |
| `run.recovered` | `[{runId, backToQueue, uncertain}]` | `Recover` settled runs at start. The window may not listen yet: call `Recovered()` on load too |
| `engine.changed` | `{engineId}` | Install, update, roll back, remove |
| `app.update` | `{phase, release?, stage?, pct?}` | App update state moves |
| `notify` | `{level, code, params}` | A toast: done, uncertain, proxy down, update ready |

## ProfileService

Built: `app/profile_service.go`, tests in `app/profile_service_test.go` (fake launcher, real SQLite store) and `app/profile_e2e_test.go` (real Chrome, `LADILALA_E2E=1`). UI side: `ui/src/bridge/profiles.ts`.

DTO `ProfileDTO`: `id, name, group, tags[], notes, proxyId?, fingerprint, state, err?, errCode?, cookieCount, sessions[], dataDir, createdAt, updatedAt`. `fingerprint` is `profile.Fingerprint` plus `preset` (`windows-11 | windows-10 | macos-14`). It has `seed` (a number the UI can hold exactly, below 2^53) and the six modes (`canvas, audio, webgl, fonts, clientRects, webrtc`: `noise | real | off`). `state` is `running | stopped | error`. `err` is `proxyDown | engineExit`. `errCode` is the API error code of the last failed start. The DTO never holds the proxy password. `cookieCount` is always 0 and `sessions` is always empty: nothing records them yet.

`ProfileInput`: `name, group, tags[], notes, proxyId, preset, seed, language, timezone, canvas, audio, webgl, fonts, clientRects, webrtc`. The device (UA, screen, GPU, threads, memory) is not an input: it follows from `preset` and `seed` (`profile.NewFingerprint`), so the profile is always coherent. On create an empty field takes the default (preset `windows-11`, new seed, `en-US`, `America/New_York`, modes `noise` and WebRTC `off`). On update an empty field keeps the stored value, except `group`, `tags`, `notes` and `proxyId`, which are always replaced. The device is rebuilt only when `preset` or `seed` differ from the stored ones. `profile.Validate` runs on the result: a failure gives `invalid` with `details.problems`.

| Method | Purpose | Core call |
|---|---|---|
| `Presets()` returns `{presets[], engineMajor}` | OS presets and the Chrome major version new fingerprints use (from the installed Chrome, else `profile.DefaultBrowserVersion`) | `driver.StockChrome.Version` |
| `PreviewFingerprint({preset, seed, language, timezone})` returns the fingerprint | The editor shows the device before saving. Saves nothing. Seed 0 picks a random one | `profile.NewFingerprint` |
| `ListProfiles(filter) []ProfileDTO` | `filter` is `{text, group, tag, state}`, all optional. No paging: the UI holds the whole list | store |
| `GetProfile(id)` | One profile | store |
| `CreateProfile(input)` | New profile, data folder `<data>/profiles/<id>` (made at first start). `conflict` (`details.reason` `name`) if the name is taken, ignoring case | `profile.NewFingerprint` |
| `UpdateProfile(id, input)` | Edit. While the browser runs, only name, group, tags and notes may change. A change to the device, language, timezone, modes or proxy gives `conflict` (`reason` `running`) | `profile.Validate` |
| `CloneProfile(id, name)` | Copy settings under a new name (empty: `<name> (copy)`, then ` 2`, ` 3`), new seed, same OS, language, timezone, modes and proxy, empty data folder | `profile.Shuffle` |
| `DeleteProfiles(ids[], deleteData bool)` | All ids must exist and none may run, or nothing is deleted (`notFound`, `conflict` with `reason` `running` and `ids`). `deleteData` removes the data folder, only inside `<data>/profiles` | store |
| `StartProfiles(ids[]) []StartResult` | Profiles start at the same time. Each has its own relay. A profile with a proxy first needs a TCP connection to the upstream, else `proxy.down`: it never starts without its proxy. The call returns when every start has ended (up to 2 minutes). Fires `profile.state` | `engine.StockLauncher.Launch`, `proxy.New`, `driver.LaunchStock` (verifies the spoof) |
| `StopProfiles(ids[])`, `StopAll()` | Close browser and relay. A profile that does not run is ignored | `Browser.Close` |
| `ShuffleFingerprint(ids[])` | New seed, same OS, language, timezone and modes. `conflict` while running | `profile.Shuffle` |
| `AssignProxy(ids[], proxyId)` | Set, or clear with an empty `proxyId`. `conflict` while running | |
| `AssignProxyRoundRobin(ids[], proxyIds[])` | The "Assign proxies" bulk action | |
| `BulkSetGroup(ids[], group)`, `BulkSetTags(ids[], add[], remove[])` | Group and tag edits. Allowed while running | |
| `ApplyProxyGeo(id)` | Timezone and language from the exit IP of the profile's proxy. One test request through the proxy (see `docs/proxy-lookup.md`). `conflict` while running | `profile.ApplyGeo` |
| `ListGroups()`, `ListTags()` | Distinct values in use, sorted | store |

Not built: `CheckConsistency` (the editor and the detail view compute the nine checks in `ui/src/store.ts` from the draft, so a profile that is not saved yet can be checked), `ExportCookies`, `ImportCookies`, `ClearCookies` (the cookie screens are hidden in the real app).

`StartResult`: `{profileId, ok, error?}` where `error` is the API error (`code`, `retryable`). The UI shows one toast per profile and one per bulk call. An unknown id gives `ok` false with `notFound`. A running profile gives `ok` true and nothing changes.

State: kept in memory, one entry per running browser. A browser that exits by itself (the user closed the window) gives `stopped`: `core/driver` does not report an exit code, so a crash is not told apart. A failed start gives `error`: `err` is `proxyDown` for `proxy.*` codes and `engineExit` for the rest, and `errCode` holds the exact code. `StopProfiles` and a later good start clear the error. Closing the app kills all browsers (`Backend.Close`, Windows Job object).

## ProxyService

Built: `app/proxy_service.go`, `app/proxy_parse.go`. Tests in `app/profile_service_test.go`. UI side: `ui/src/bridge/proxies.ts`.

DTO `ProxyDTO`: `id, scheme (http|https|socks5), host, port, username, hasPassword, country, timezone, language, status (ok|down|checking|untested), latencyMs?, ip?, usedBy[]` (profile ids). The password is in `core/store` Secrets under `proxy/<id>`. No DTO, event or error holds it. `country`, `timezone`, `language`, `status`, `latencyMs` and `ip` are the last test result. They are kept in `<data>/proxy-status.json` (no table column exists) and cleared when the endpoint or password changes.

`ProxyInput`: `scheme, host, port, username, password`. `password` is write-only: null keeps the stored one, `""` removes it. Two proxies may not share scheme, host, port and username: `conflict` (`reason` `duplicate`).

| Method | Purpose | Core call |
|---|---|---|
| `ListProxies()` | All proxies with usage | store |
| `CreateProxy(input)`, `UpdateProxy(id, input)` | Add and edit | store, Secrets |
| `SetProxyPassword(id, password)` | Replace the secret. Empty removes it | Secrets |
| `DeleteProxy(id)` | `conflict` (`reason` `running`) if a running profile uses it. Otherwise the profiles lose the link | store |
| `TestProxy({id, scheme, host, port, username, password})` returns `ProxyCheckResult` | `id` names a saved proxy. Given fields override it, and an empty password uses the saved one. Result: `{ok, status, ip, latencyMs, country, timezone, language, code, message}`. A failed test is a normal result (`ok` false, `code` `proxy.down` or `proxy.auth`), not an error. A saved proxy keeps the result and fires `proxy.status` | `Relay.Check`, geo lookup |
| `HealthCheck(ids[])` returns `jobId` | Empty `ids` means all. At most 6 at a time. Fires `proxy.status` (`checking`, then the result) per proxy, `job.progress` and, at the end, `job.done`. There is no `CancelJob` yet | same |
| `ImportProxies(text, defaultScheme)` returns `{added, skipped[]}` | One proxy per line. Forms: `host:port`, `host:port:user:pass`, `[ipv6]:port[:user:pass]`, `scheme://host:port`, `[scheme://]user:pass@host:port` (also with `[ipv6]`; user and password may be percent-encoded). Empty lines and `#` lines are ignored. `skipped` has `{line, reason}`: `line` counts from 1, `reason` is `format`, `scheme`, `host`, `port` or `duplicate`. It never repeats the line, because a line can hold a password | |
| `GetProxyLookup()`, `SetProxyLookup({ipUrl, geoUrl})` | The two public lookup services of a test. `docs/proxy-lookup.md` | |

A test sends the IP request through a relay to the upstream (never directly). The location request goes through the same relay. Country comes from the geo answer, timezone too, and language from a country table (`countryLanguage`). If the geo lookup fails or is off, the test still passes and those three fields stay empty.

## MacroService

Code: `app/macro_service.go`, `app/recording_service.go`. The macro document is strict JSON (`docs/macro-schema.md`). What the UI keeps beside it (description, the linked data source, the recording warnings, the variable to column overrides) is in `macro-meta.json` next to the database, because the schema has no field for it.

DTOs (all camelCase):
- `MacroSummary`: `id, name, description, stepCount, version, hash, variables[], sourceId, warningCount, updatedAt`. `variables` is the list of declared names. Run setup reads `id, name, variables`.
- `MacroDTO`: `macro` (the `macro.Macro` document), `version, hash, description, sourceId, warnings[], createdAt, updatedAt`.
- `SaveMacroInput`: `macro, description, warnings?`. An empty `macro.id` creates a macro. `warnings` null keeps the stored ones.
- `Problem`: `{path, msg}`. The paths are the ones `macro.Validate` reports (`steps[login].value.expr`).
- `RecordWarning`: `{kind: canvas_unsupported | file_input_unsupported, stepIndex, locator?, message?}`.
- `ImportReview`: `{reviewId, macro, allowedOrigins[], commitPointSteps[], variables[], problems[], hash, stepCount}`. A document with problems has an empty `reviewId` and `macro` null.
- `StepPreview`: `{stepId, action, value, error?, column?}`. `error` is `missingColumn` or `invalid`.

| Method | Purpose | Core call |
|---|---|---|
| `ListMacros()` | Macros screen. Newest edit first | store |
| `GetMacro(id)` | Editor. Returns `MacroDTO` | store, `macro.Load` |
| `SaveMacro(input)` | Create or update. Before it validates, it adds a declaration (text, optional) for each `{{name}}` the macro uses and does not declare. The saved macro lists these variables. Version rises when the canonical JSON changes. `invalid` carries `details.problems`. `conflict` (`details.field` = `name`) when another macro has the name (case and outer spaces ignored) | `macro.DeclareUsed`, `macro.Validate`, `MacroRepo.Put` |
| `ValidateMacro(macro) []Problem` | Live check while editing. An empty `id` is accepted. It checks a copy after `macro.DeclareUsed`, so a new `{{name}}` is not a problem. `ImportMacro` stays strict: an undeclared reference in an imported file is a problem | `macro.DeclareUsed`, `macro.Validate` |
| `DuplicateMacro(id, name)` | New ID. A taken name gets ` (2)`, ` (3)`. Description, source link, warnings and overrides are copied | |
| `DeleteMacro(id)` | `conflict` (`details.reason` = `run`) while a run in state created, running, paused, takeover, stopping or crashed_recovered uses it. Ended runs keep their own copy, so they do not block | |
| `ImportMacro(content)` returns `ImportReview` | Strict loader on the file text. Saves nothing | `macro.Load` |
| `ConfirmImport(reviewId)` | Save a reviewed macro once. New ID, numbered name when taken. Reviews expire after 30 minutes (`notFound`) | |
| `ExportMacro(id, path)` | Write indented JSON | `macro.Marshal` |
| `PickMacroSavePath(fileName)` | Native save dialog. Returns `""` when cancelled | Wails dialog |
| `MapVariable(macroId, stepId, column)` | The value of one fill or select step becomes `{{column}}` and the column is declared as a required string variable. Other steps with the same text do not change. Saves and returns `MacroDTO` | `recorder.Parameterize` |
| `SetMacroSource(macroId, sourceId)` | Link a source. Empty clears the link | |
| `PreviewRow(macroId, sourceId, rowIndex) []StepPreview` | The text of each step value for one row, with the saved mapping applied | `player.BuildVars`, `Value.Eval` |

`SetBindings` of the first draft is gone: the mapping screen sets `MapVariable` for the steps and `DataService.SetMapping` for the column overrides. Use `{{name}}` in a value. A literal `{{` is written `\{{` (`macro.EscapeLiteral`).

A step can be switched off with `disabled: true` (macro schema). The player skips it and the run does not read its variables.

### Recording

Events: `record.state`, `record.step`, `record.steps`, `record.hint`, `record.warning` (see Events). Canvas and file input are warnings only: the recorder makes no step for them. The warnings come back from `StopRecording` and are stored with the macro by `SaveMacro`, so the editor shows them at the step where they happened.

| Method | Purpose | Core call |
|---|---|---|
| `StartRecording({profileId, startUrl, name, lang?, hint?})` returns `recordId` | Launch the profile through the stock path with the recorder attached (own driver launcher and relay, closed at stop), open `startUrl`. `startUrl` must be http or https (`invalid`). No data source is needed. While recording, the user types `{{name}}` (alone or inside text) in a text-like field; the recorder turns it into a data column: it normalizes the name (`Họ tên` becomes `ho_ten`), writes a test token the page accepts into the field (the player's native setter plus `input` and `change`) and records `fill` with `{{name}}`. `lang` is `vi` or `en` (overlay texts, default `en`). `hint` shows the first-use hint card in the page. Errors map to `profile.locked`, `profile.dataDir`, `engine.notFound`, `engine.policyBlocked`, `engine.hang`, `proxy.auth` | `recorder.New`, `Recorder.Hook` |
| `PauseRecording(recordId)`, `ResumeRecording(recordId)` | Events are dropped while paused. The browser stays open | `Recorder.SetPaused` |
| `StopRecording(recordId)` returns `{macro, warnings[], problems[], columns[{name, kind, protected, steps}]}` | Close the browser and return the draft. Nothing is saved. `problems` is not empty when the draft cannot be saved yet (no step). `columns` lists the data columns the steps still read, in macro order. `macro.variables[].kind` holds the field kind (`text email phone number url date password`); `type` stays `string` | `Recorder.Macro`, `Warnings`, `data.TemplateColumns` |
| `DiscardRecording(recordId)` | Close without result | |
| `GetRecordingStatus(recordId)` | `{recordId, state, stepCount, warningCount}`. `notFound` after stop | `Recorder.Steps` |

A recorded password field becomes `{{password}}`. The recorder never stores the text.

## DataService

Code: `app/data_service.go`. A source is stored in `data_sources` with a config that holds no secret. A REST bearer token, a basic password and every header value go to the secret store (`source/<id>/token`, `basicPass`, `headers`). No DTO has them; `SourceDTO` has `hasAuth`, `authKind`, `basicUser` and `headerNames`.

DTOs: `SourceDTO` (`id, name, kind (csv|excel|json|rest), path, encoding, delimiter, sheet, nested, url, rowsPath, authKind, basicUser, hasAuth, headerNames[], paging, pageSize, columns[], rowCount, createdAt, updatedAt`; `columns` and `rowCount` are filled by `GetSource` only, `rowCount` -1 means unknown), `FileSourceInput`, `RESTSourceInput`, `SourcePreview` (`kind, encoding, delimiter, sheet, sheets[], columns[], rows[][], from, total, duplicates[], issues[]`), `RowIssue` (`rowIndex, level, code, params?`; codes `ragged, nested, notObject, emptyRequired, noColumn, format`), `MappingDTO` (`macroId, sourceId, variables[{name, type, required, column, suggestion}], columns[], missing[], extra[], duplicates[], overrides{}, ok`), `ValidationResult` (`checked, issues[], truncated`).

| Method | Purpose | Core call |
|---|---|---|
| `PickDataFile()` | Native open dialog for csv, tsv, txt, json, xlsx. `""` when cancelled | Wails dialog |
| `InspectFile(input)` returns `SourcePreview` | Header and first 8 rows with the chosen encoding, delimiter or sheet. Saves nothing. The wizard calls it again when an option changes | `data.NewCSV`, `NewJSON`, `NewXLSX` |
| `CreateSourceFromFile(input)` | Save a file source. The first sheet is pinned by name | |
| `TestRESTSource(input)` returns `SourcePreview` | First request only. Saves nothing and stores no secret | `data.NewREST` |
| `CreateRESTSource(input)` | GET only, paging, auth. Secrets go to the secret store | `data.NewREST` |
| `CreateSheetsSource(url)` | [PENDING] ADR 0016 is open. Always `data.auth` with `reason` `needsSetup`. The UI shows "needs setup" | none |
| `ListSources()` | Saved sources, not opened | store |
| `GetSource(id)` | Opens the source: columns and row count (REST: first request, count -1) | |
| `DeleteSource(id)` | `conflict` while a run that is not finished uses it. Removes the secrets and the links | |
| `PreviewSource(id, from, limit)` | Rows `[from, from+limit)`, at most 200 | `Source.Open` |
| `ValidateMapping(macroId, sourceId)` returns `MappingDTO` | Variables matched to columns (name, ignoring case and outer spaces) with the saved overrides. `suggestion` is a column whose name matches ignoring case and punctuation | `Mapping.Resolve` |
| `SetMapping(macroId, sourceId, overrides)` | Save variable to column overrides and link the source. Unknown variable or column: `invalid`. Empty column removes an override | |
| `ValidateRows(macroId, sourceId, from, to)` | Required variables need a column and a value, and the file must be well formed. Up to 500 issues | `Mapping.RequiredValidator` |
| `ExportTemplate(macroId, savePath, lang)` returns `{path, columns}` | Write an empty data file for a saved macro. The extension picks the format: `.csv` gives one header row; any other name gives an Excel file (`.xlsx` is added when missing) with the sheet `Data` (row 1 = the data columns in macro order, bold and frozen, row 2 empty, column width fit) and the sheet `Guide` (column, field type, page label, transform, step count). `lang` (`vi` or `en`) sets the Guide text. A macro with no data columns is `invalid`. The headers are the variable names, so a file filled from the template maps to the macro by exact name | `data.WriteTemplate` |
| `ExportDraftTemplate(macro, savePath, lang)` returns `{path, columns}` | The same for a macro that is not saved yet (the review after a recording) | `data.WriteTemplate` |
| `PickTemplateSavePath(fileName, format)` | Native save dialog; `format` is `xlsx` or `csv`. Returns `""` when cancelled | Wails dialog |

For the run shell: `openDataSource(ctx, store, sourceID)` opens a saved source with its secrets. `openMappedSource(ctx, backend, macroID, sourceID)` does the same and adds the variable names to every row, so a user override reaches the player. Both are in `app/data_service.go`. The caller closes the source.

Errors: `data.format` (file, encoding, sheet, extension, `details.reason`), `data.auth` (HTTP 401 or 403), `network` (REST request failed), `conflict`, `notFound`, `invalid`.

## RunService

`app/run_service.go`. A thin service over `engine.Service` (`core/engine/service.go`). Every method takes a `context.Context` first. Types in this section are the JSON DTOs of the service (camelCase, time as RFC 3339). They mirror `core/engine/types.go`. The UI bridge is `ui/src/bridge/runs.ts`.

| Method | Purpose |
|---|---|
| `CreateRun(ctx, req CreateRunRequest) (Run, error)` | Reads the saved macro and source from the store. Opens the source, copies the macro and the rows of the range into the ledger. The run is `created`. Error `invalid` for a missing id, a negative `from`, or `to <= from` |
| `StartRun(ctx, id string) error` | Only from `created`. Starts the event forwarder first, so no early event is lost |
| `PauseRun(ctx, id string) error` | Pause at the next step. Returns when all browsers wait, or after 15 s |
| `ResumeRun(ctx, id string) error` | Also restarts a run that is `failed`, `crashed_recovered`, or `paused` with no workers |
| `StopRun(ctx, id string) error` | Rows in flight go back to pending, or uncertain past the commit point. The run becomes `finished` |
| `Takeover(ctx, id string) error` | The user drives the browsers. One call per run, not per profile |
| `ReleaseTakeover(ctx, id string) error` | Checks the macro guards on each page first. Error `run.guard` keeps takeover |
| `RetryRows(ctx, id string, f RowFilter) (int, error)` | Resets Failed and Skipped rows that match `f`. Never Uncertain rows. Returns the row count |
| `MarkUncertainRow(ctx, id string, index int, res string) error` | `res`: `"done"` or `"failed"`. Any other value is `invalid` |
| `ListRuns(ctx, f RunFilter) ([]Run, error)` | Newest first |
| `GetRun(ctx, id string) (Run, error)` | Includes `counts` per row state |
| `ListRows(ctx, id string, f RowFilter) (RowPage, error)` | The ledger, paged |
| `Watch(ctx, id string) error` | Starts the event forwarder for a run that is already active (the UI opens it after a reload). Does nothing for a finished run |
| `Recovered(ctx) ([]Recovery, error)` | What `Recover` settled at app start. The service calls `engine.Recover` once when it is built |
| `DismissRecovered(ctx) error` | Clears that list (the user chose "leave as it is") |
| `ExportLedger(ctx, id, path string) (string, error)` | Writes the ledger as CSV and returns the path. An empty `path` opens a save dialog; the result is `""` when the user cancels. Columns: `row` (from 1), `state`, `commit`, `profile`, `attempts`, `step`, `errorKind`, `errorMessage`, `startedAt`, `finishedAt`. Source data is not exported (it can hold passwords). Cells that start with `=`, `+`, `-`, `@` get a leading `'` |

`Subscribe` and `Close` of the engine are not UI methods. The service subscribes by itself and forwards. There is no `StopSession`: a profile is not stopped alone. The UI labels for uncertain rows: Mark submitted (`done`), Retry (`failed`, then `RetryRows` with that index), Not submitted (`failed`).

Request types:
- `CreateRunRequest`: `macroId, sourceId string`, `from, to int` (0-based, `to < 0` means to the end), `profileIds []string`, `options RunOptions`. The UI shows rows from 1 and sends `from - 1`.
- `RunOptions`: `concurrency` (1), `delayMs` (0), `stopAfterErrors` (5), `maxRestarts` (3), `dryRun`, `testOne` (first row only).
- `RowFilter`: `states []RowState`, `indexes []int`, `offset, limit int` (`limit` 0 means 100). `RunFilter`: `states []RunState`, `offset, limit int`.

Response types:
- `Run`: `id, macroId, macroName, macroVersion, macroHash, sourceId, state, reason, options, profileIds, rowFrom, rowTo, total, counts map[RowState]int, createdAt, startedAt?, finishedAt?`.
- `Row`: `runId, index, idempotencyKey, state, commitState, profileId, attempts, data map[string]string, errorKind, errorMsg, stepId, startedAt?, finishedAt?`. `data` can hold passwords. The UI masks it.
- `RowPage`: `rows, total` (rows that match the filter). `Recovery`: `runId, backToQueue, uncertain`.

Run states: `created, running, paused, takeover, stopping, finished, failed, crashed_recovered`. Row states: `pending, running, done, failed, skipped, uncertain`. Commit state: `none, submitting, submitted`. A commit point step writes `submitting` before the click (ADR 0014). `Row.errorKind`: `data, origin, locator, guard, browser, timeout, step, crash, stopped, ledger, resolved`. State tables: `docs/run-engine.md`.

Events. The service reads the engine events of each active run in one goroutine. It never blocks the engine: the engine drops the oldest event of a slow reader. The forwarder maps them to the events in the Events table. `run.state`, `run.session` and `run.uncertain` go out at once. Every row event only marks the run dirty. Every 200 ms the forwarder sends one `run.progress` (counts from `GetRun`) and one `run.log` (at most 200 lines; older lines of a burst are counted in `skipped`). When a run ends (`finished` or `failed`) it sends a last flush and stops. `ResumeRun` and `Watch` start it again. The log lines carry the engine event kinds: `run.state`, `row.started`, `row.commit`, `row.done`, `row.failed`, `row.skipped`, `row.uncertain`, `row.released`, `row.resolved`, `browser`. Their `message` is English text for the log; the UI translates the kind.

Source and mapping. `CreateRun` opens the saved source with `openMappedSource` (`app/data_service.go`): the source config, its secrets and the saved column mapping of the macro all apply. The engine copies the mapped rows into the ledger.

Errors map in one place (`toAPIError`): `run.notFound`, `run.state` (`details.state`, `details.op`), `run.noRows`, `run.noProfiles`, `run.tooFewProfiles`, `run.notUncertain`, `run.guard`, `run.browserDied` (retryable), `notFound` (macro, source, profile), `invalid`, `macro.missingColumn`, `data.format`, `disk.full` (export), `internal`.

Prototype gaps closed in the UI: dry run, test of one row, whole-run pause and resume, row range, run summary with warnings before Start, the per-row ledger with filters, paging and CSV export, crash recovery banner (resume or leave). Still open: CAPTCHA or 2FA pause (`docs/ui-gap-review.md` part 1), duplicate-key warning, screenshots on failure.

## EngineService

Built in `app/engine_service.go`, `engine_install.go`, `engine_doctor.go`. Three tiers (ADR 0003, 0004, 0005). Engine ids: `chrome`, `edge` (tier 1, stock, from this PC), `cft` (tier 2, Chrome for Testing, downloaded by Ladilala), `patched` (tier 3, own Chromium, not built: shown as `unavailable`, no download code). fingerprint-chromium is not an engine (ADR 0006).

Files: `<data>/engines/chrome-for-testing/<version>/` holds one unpacked version and `ladilala-engine.json` (our own SHA-256, URL, time). `state.json` holds `active` and `previous`. `checksums.json` records the SHA-256 of each zip on its first download (Google publishes none). `downloads/` holds `.zip.part` files.

`EngineDTO`: `id, tier (1|2|3), name, status (ready|missing|blocked|unavailable), system, path, installed[{version, sizeBytes, path, sha256, active}], active, previous, latest, downloadBytes, default, policyBlocked, notesUrl, jobId`. `latest` and `downloadBytes` are known after `CheckEngineUpdates`. `jobId` is set while an install job of that engine runs.

| Method | Purpose |
|---|---|
| `ListEngines()` returns `[]EngineDTO` | The three tiers. Stock Chrome first, then Edge (`driver.StockChrome.Detect`, version by `--version`, policy by `driver.DebuggingBlockedByPolicy`) |
| `CheckEngineUpdates()` returns `[]CfTBuildDTO{version, channel, revision, url, installed}` | Reads the official JSON (`driver.FetchCfTVersions`), all channels for this platform. Error `engine.network` |
| `InstallEngine({engineId, version})`, `UpdateEngine(id)`, `RedownloadEngine(id, version)` return `JobDTO` at once | Only `cft`. Empty version means the newest of the chosen channel. Redownload deletes the version and installs it again, and fails with `engine.checksum` when the file differs from the recorded SHA-256 |
| `GetJob(id)`, `ListJobs()`, `PauseJob(id)`, `ResumeJob(id)`, `CancelJob(id)` | Pause works in stage `download` only. Cancel keeps the `.part` file. One job runs at a time (`conflict`, reason `jobRunning`) |
| `ApplyJobFix(jobId, fixId)` returns `JobDTO` | `retry` (continue the partial download), `redownload` (delete the zip and the partial file), `freeSpace` (delete old versions and leftovers). Starts a new job |
| `RollbackEngine(id)`, `RemoveEngine(id, version)` | `cft` only. Empty version removes all. Both refuse while a run is active (`conflict`, reason `runActive`) |
| `SetDefaultEngine(id)` | The engine must be installed and not blocked. Applies at once to `StockLauncher` (next browser launch) |
| `GetPins()`, `SetGroupPin(group, id)`, `SetProfileEngine(profileId, id)` | Pins win over the default: profile, then group, then default. Empty id removes a pin |
| `GetUpdateSettings()`, `SetUpdateSettings(s)` | `channel (Stable|Beta), autoCheck (daily|weekly|off), keepOld (0 to 3), bandwidthLimit (bytes per second, 0 = unlimited), lastChecked`. Auto check runs at app start when due |
| `RunDiagnostics()` returns `[]Diag{id, level, code, params, fixId}` | Six checks: `browser`, `policy`, `pipe` (2 second launch probe over the CDP pipe), `folder`, `job` (Job Object), `relay` (local port). `fixId` is `installCft` |
| `TestLaunch(engineId)` returns `[]TestStep{id, state, code, params}` | `launch`, `cdp`, `fingerprint` (canvas and hardware readback), `proxy` (always `skipped`, code `noProfile`: ProxyService tests proxies). A failed step makes the later ones `skipped` |
| `DiagnosticsReport()` | Plain text. Home folder and user name are replaced |
| `OpenEnginesFolder()` | Shows the engines folder |

`JobDTO`: `id, engineId, kind (install|update|redownload), version, state (running|paused|done|failed|cancelled), stage, stages[{key, state, code, params}], bytesDone, bytesTotal, bytesPerSec, attempt, partialKept, error{code, details{fixId}}, startedAt`. Stages: `check`, `download`, `verify`, `unpack`, `selftest`, `switch`. The download tries 3 times with a growing pause and continues with an HTTP Range request. `verify` compares the SHA-256 with the first download. `unpack` goes to `<version>.staging`, refuses zip slip, then renames. `selftest` starts the new browser and asks it for its version; on failure the new version is deleted and the old one stays active. `switch` sets `active`, keeps `keepOld` old versions and sets `previous`.

Event `engine:job`: a full `JobDTO` after every change, at most 4 per second while bytes flow.

Error codes: `engine.network`, `engine.download`, `engine.checksum`, `engine.disk`, `engine.policy`, `engine.selftest`, `engine.installed`, `engine.versionGone`, `engine.binary`, `engine.timeout`, `engine.unpack`, `engine.missing`, `engine.unavailable`, `engine.system`, `engine.noPrevious`, `engine.inUse`, `engine.platform`, `engine.notRollback`, `engine.internal`, plus `invalid`, `notFound`, `conflict`.

Not built: `SetUpdateSettings.autoUpdate`, `ListDependencies`, `FixDependency`, `ApplyFix` for orphans and locks, `CreateSupportBundle`. The UI has no Dependencies tab and no support bundle.

## AppUpdateService

Built in `app/appupdate_service.go` over the Wails v3.1.0 updater (`app.Updater`, checked against its source). The updater has one provider, `feedProvider`: it reads the feed URL and the channel from settings on every call. The feed URL default is `https://ladilala.71.zone/manifest.json` (an empty value in settings still means "updates are not configured"). `LADILALA_UPDATE_URL` overrides the setting.

`AppUpdateState`: `current, channel (stable|beta|nightly), autoCheck (daily|weekly|off), installOnQuit, skipped, lastChecked, configured, feedUrl, feedFromEnv, phase (idle|checking|available|downloading|ready|failed), release?, stages[], bytesDone, bytesTotal, bytesPerSec, failure?, whenIdle, activeRuns, engineMajor, engineCompatible`. `release`: `version, channel, name, notes, news[], fixed[], publishedAt, sizeBytes, minEngine`. The manifest `metadata` carries `minEngine`, `news` and `fixed`. The engine need is shown, not enforced (`engine.tooOld` is planned).

| Method | Purpose |
|---|---|
| `GetAppUpdateState()` | Settings, sidebar footer, update sheet |
| `SetAppUpdateSettings({channel, autoCheck, installOnQuit, feedUrl})` | Feed must be https (or loopback http). A new channel or feed drops an offered release |
| `CheckAppUpdate(force)` | `force=true` (the Check Now button) shows a skipped release again. Error `update.notConfigured` without a feed. A feed error sets phase `failed` |
| `DownloadAppUpdate()` | Starts download, signature check and staging in the background. Progress arrives as event `appupdate:state` |
| `CancelAppUpdate()` | Stops the download. The release stays offered. The Wails updater cannot pause |
| `ApplyUpdateFix(fixId)` | `retry` or `redownload` |
| `RestartNow()` | Refused with `conflict` (reason `runActive`) while a run is active |
| `RestartWhenIdle()`, `CancelRestartWhenIdle()` | Waits until `Engine.ActiveRuns()` is 0, then restarts |
| `SkipVersion(version)` | Hides a release until Check Now |
| `GetReleaseNotes(version)` | The offered release |

When the user closed the app, an update is ready and `installOnQuit` is on, `ServiceShutdown` hands the update to the updater helper. [INFERENCE] Not tested on Windows. `RollbackApp` and `PauseJob` for app updates do not exist: the Wails updater has no rollback and no pause.

Error codes: `update.notConfigured`, `update.network`, `update.signature`, `update.disk`, `update.noRelease`, `update.restart`, `invalid`, `conflict`.

## SettingsService

Built in `app/settings_service.go` and `settings_store.go`. Values are JSON in `<data>/settings.json` (not SQLite). A broken file fails the start instead of being replaced. Out-of-range values fall back to the default. The file also holds the update and engine settings of the other two services.

`GeneralSettings`: `language (vi|en), theme (system|light|dark), defaultDelayMs, concurrency (1 to 64), proxyTestUrl, logLevel (error|warn|info|debug), defaultEngine`. Defaults: `vi`, `system`, 0, 1, `https://api.ipify.org`, `info`, `chrome`.

| Method | Purpose |
|---|---|
| `GetSettings()`, `SaveSettings(s)` | `SaveSettings` validates (`invalid` with `problems`) and ignores `defaultEngine`: `EngineService.SetDefaultEngine` owns it. The log level applies at once |
| `SetLocale(vi\|en)`, `SetTheme(t)` | The UI also keeps both in `localStorage` for the first paint |
| `GetDataFolder()` | `{path, pending, default, overridden}`. `path` has the separators of the OS |
| `PickDataFolder()`, `ChangeDataFolder(path)` | Writes `datafolder.json` in the default folder. The change applies at the next start. Data is **not moved**: the new folder starts empty and the old one stays. Empty path goes back to the default. `LADILALA_DATA` overrides everything |
| `DataFolderUsage()` | Bytes in the data folder |
| `OpenDataFolder()`, `OpenLogsFolder()` | Show the folder in Explorer |

Not built here: `GetStorageUsage` per profile, `SecretsStatus`, `Backup`, `Restore`.

Dark mode and the sidebar state stay in the UI (the theme choice is saved through `SetTheme`).

## Open points

1. Run events and states: done. See RunService.
2. Sheets sign-in and token storage wait for ADR 0016.
3. Whether the UI may call `ConfirmImport` only after a review token is a security rule. Keep it.
4. Geo lookup source is undecided.
5. Wails binding generation and event typing are not tested. [INFERENCE] Wails v3 generates TypeScript bindings. The shell gate (R1) decides.

## Checked against

`ui/src/store.ts`, `engines.ts`, `appupdate.ts`, `macros.ts`, `views/*.vue`, `components/MappingView.vue`, `ProfileEditor.vue`, `core/profile/types.go`, `fingerprint.go`, `core/proxy/relay.go`, `check.go`, `core/macro/macro.go`, `validate.go`, `core/recorder/recorder.go`, `core/player/player.go`, `errors.go`, `vars.go`, `hooks.go`, `core/driver/engine.go`, `cft.go`, `stock.go`, `core/data/*.go`, `core/store/store.go`, `runs.go`, `core/engine/types.go`, `service.go`, `runner.go`, `hub.go`, `docs/run-engine.md`, `docs/data-model.md`, `.orca/briefs/runcore.md`, vault ADRs 0011 to 0016, `docs/ui-gap-review.md`.
