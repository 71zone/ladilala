# Data model

Code: `core/store`. One SQLite file, `ladilala.db`, pure Go driver
(`modernc.org/sqlite`, no cgo). Migrations are embedded SQL in
`core/store/migrations` (`NNNN_name.up.sql`, `.down.sql`). Table `schema_migrations`
holds the applied versions. `Migrate` is safe to repeat. `MigrateDown(target)` reverts.

## Where the data lives

| Item | Location |
|---|---|
| Data folder | `%APPDATA%\Ladilala` on Windows, `os.UserConfigDir()/Ladilala` elsewhere; env `LADILALA_DATA` overrides. `LADILALA_INSTANCE` names an instance: its own single-instance lock and the window title `Ladilala [name]`, so several can run together, each with its own `LADILALA_DATA` |
| Database | `<data folder>/ladilala.db` (WAL files next to it) |
| Secrets, Windows | `<data folder>/secrets.dpapi.json`, each value sealed with DPAPI (current user) |
| Secrets, macOS and Linux dev | `<data folder>/secrets.insecure.json`, plain base64, NOT encryption |

Pragmas: `journal_mode=WAL`, `foreign_keys=1`, `synchronous=FULL` (the commit point
write must survive a crash), `busy_timeout=10000`.
Times are Unix milliseconds. IDs are random 128-bit hex strings.

## What is secret

| Data | Where it is kept |
|---|---|
| Proxy password | `Secrets` store, key `proxy/<proxy id>`. Never in SQLite. |
| Tokens of data sources (for example Google) | `Secrets` store. The `config` column holds a reference only. |
| Data rows of a run | `run_rows.data`, plain text. Sensitive: it can hold passwords from the source. |
| Macro body | `macros.body`, `runs.macro_body`. The recorder never stores typed passwords. |

## Tables

### proxies
| Column | Type | Note |
|---|---|---|
| id | TEXT | primary key |
| name | TEXT | |
| scheme | TEXT | http, https, socks5 |
| host, port, username | TEXT, INTEGER, TEXT | |
| has_secret | INTEGER | 1 when a password is in `Secrets` |
| created_at, updated_at | INTEGER | |

### profiles
| Column | Type | Note |
|---|---|---|
| id | TEXT | primary key |
| name, grp, notes | TEXT | `grp` is the group |
| tags | TEXT | JSON array of strings |
| proxy_id | TEXT | foreign key to proxies, SET NULL on delete |
| fingerprint | TEXT | JSON of `profile.Fingerprint` |
| data_dir | TEXT | Chrome user-data-dir |
| created_at, updated_at | INTEGER | |

Indexes: `profiles_proxy(proxy_id)`, `profiles_group(grp)`.
`profile.Profile.Proxy` is not a column. It is built from `proxy_id` and `Secrets` on read.

### macros
| Column | Type | Note |
|---|---|---|
| id | TEXT | primary key, the macro id |
| name | TEXT | |
| version | INTEGER | starts at 1, +1 when the body changes |
| hash | TEXT | SHA-256 hex of the canonical JSON body |
| body | TEXT | canonical macro JSON (schema v1) |
| created_at, updated_at | INTEGER | |

### data_sources
| Column | Type | Note |
|---|---|---|
| id | TEXT | primary key |
| name | TEXT | |
| kind | TEXT | csv, excel, json, rest, gsheet |
| config | TEXT | JSON, no secrets |
| created_at, updated_at | INTEGER | |

### runs
| Column | Type | Note |
|---|---|---|
| id | TEXT | primary key |
| macro_id, macro_name, macro_version, macro_hash | | copied from the macro at creation |
| macro_body | TEXT | copy of the macro body: a later edit never changes the run |
| source_id | TEXT | saved source config, may be empty |
| state | TEXT | see `run-engine.md` |
| reason | TEXT | why the run is in this state |
| options | TEXT | JSON of `engine.RunOptions` |
| profile_ids | TEXT | JSON array: the pool |
| row_from, row_to | INTEGER | range asked for, `row_to` -1 = to the end |
| created_at, started_at, finished_at, updated_at | INTEGER | start and finish can be NULL |

Indexes: `runs_state(state)`, `runs_created(created_at)`.

### run_rows (the ledger)
| Column | Type | Note |
|---|---|---|
| run_id | TEXT | foreign key to runs, CASCADE |
| row_index | INTEGER | 0-based row number in the source |
| idempotency_key | TEXT | `<run_id>:<row_index>`, unique |
| state | TEXT | pending, running, done, failed, skipped, uncertain |
| commit_state | TEXT | none, submitting, submitted |
| profile_id | TEXT | profile that holds or ran the row |
| attempts | INTEGER | +1 each claim |
| data | TEXT | JSON object, the copy of the source row |
| error_kind, error_msg, step_id | TEXT | last failure, step of the commit or error |
| started_at, finished_at, updated_at | INTEGER | |

Primary key `(run_id, row_index)`. Indexes: unique `run_rows_key(idempotency_key)`,
`run_rows_state(run_id, state, row_index)` (claim and counts).
Claim is one `UPDATE ... RETURNING`, so two workers never get the same row.

### run_events (append-only log)
| Column | Type | Note |
|---|---|---|
| id | INTEGER | autoincrement primary key |
| run_id | TEXT | foreign key to runs, CASCADE |
| row_index, ts | INTEGER | `row_index` is NULL for run events |
| kind, data | TEXT | event kind, JSON |

Index: `run_events_run(run_id, id)`. A trigger rejects every UPDATE. Rows leave only
with their run. Ledger updates check the row state in the WHERE clause and return
`store.ErrConflict` when the row moved.
