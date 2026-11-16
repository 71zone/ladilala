CREATE TABLE proxies (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    scheme      TEXT NOT NULL,
    host        TEXT NOT NULL,
    port        INTEGER NOT NULL,
    username    TEXT NOT NULL DEFAULT '',
    has_secret  INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

CREATE TABLE profiles (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    grp         TEXT NOT NULL DEFAULT '',
    tags        TEXT NOT NULL DEFAULT '[]',
    notes       TEXT NOT NULL DEFAULT '',
    proxy_id    TEXT REFERENCES proxies(id) ON DELETE SET NULL,
    fingerprint TEXT NOT NULL,
    data_dir    TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);
CREATE INDEX profiles_proxy ON profiles(proxy_id);
CREATE INDEX profiles_group ON profiles(grp);

CREATE TABLE macros (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    version     INTEGER NOT NULL,
    hash        TEXT NOT NULL,
    body        TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

CREATE TABLE data_sources (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    kind        TEXT NOT NULL,
    config      TEXT NOT NULL DEFAULT '{}',
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

CREATE TABLE runs (
    id            TEXT PRIMARY KEY,
    macro_id      TEXT NOT NULL,
    macro_name    TEXT NOT NULL,
    macro_version INTEGER NOT NULL,
    macro_hash    TEXT NOT NULL,
    macro_body    TEXT NOT NULL,
    source_id     TEXT NOT NULL DEFAULT '',
    state         TEXT NOT NULL,
    reason        TEXT NOT NULL DEFAULT '',
    options       TEXT NOT NULL,
    profile_ids   TEXT NOT NULL,
    row_from      INTEGER NOT NULL,
    row_to        INTEGER NOT NULL,
    created_at    INTEGER NOT NULL,
    started_at    INTEGER,
    finished_at   INTEGER,
    updated_at    INTEGER NOT NULL
);
CREATE INDEX runs_state ON runs(state);
CREATE INDEX runs_created ON runs(created_at);

CREATE TABLE run_rows (
    run_id          TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    row_index       INTEGER NOT NULL,
    idempotency_key TEXT NOT NULL,
    state           TEXT NOT NULL,
    commit_state    TEXT NOT NULL DEFAULT 'none',
    profile_id      TEXT NOT NULL DEFAULT '',
    attempts        INTEGER NOT NULL DEFAULT 0,
    data            TEXT NOT NULL,
    error_kind      TEXT NOT NULL DEFAULT '',
    error_msg       TEXT NOT NULL DEFAULT '',
    step_id         TEXT NOT NULL DEFAULT '',
    started_at      INTEGER,
    finished_at     INTEGER,
    updated_at      INTEGER NOT NULL,
    PRIMARY KEY (run_id, row_index)
);
CREATE UNIQUE INDEX run_rows_key ON run_rows(idempotency_key);
CREATE INDEX run_rows_state ON run_rows(run_id, state, row_index);

CREATE TABLE run_events (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id    TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    row_index INTEGER,
    ts        INTEGER NOT NULL,
    kind      TEXT NOT NULL,
    data      TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX run_events_run ON run_events(run_id, id);
CREATE TRIGGER run_events_no_update BEFORE UPDATE ON run_events
BEGIN
    SELECT RAISE(ABORT, 'run_events is append-only');
END;
