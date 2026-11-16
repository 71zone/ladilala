package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrConflict is returned when a ledger update finds the row in another state
// than the caller expected. The update did not happen.
var ErrConflict = errors.New("store: row is not in the expected state")

// RunRecord is the stored form of a run. State and Reason are plain strings;
// core/engine owns their meaning.
type RunRecord struct {
	ID           string
	MacroID      string
	MacroName    string
	MacroVersion int
	MacroHash    string
	MacroBody    []byte // copy of the macro at creation: a later edit never changes a run
	SourceID     string
	State        string
	Reason       string
	Options      json.RawMessage
	ProfileIDs   []string
	RowFrom      int
	RowTo        int // exclusive; -1 means to the end
	CreatedAt    time.Time
	StartedAt    *time.Time
	FinishedAt   *time.Time
	UpdatedAt    time.Time
}

// RowSeed is one ledger row made when the run is created.
type RowSeed struct {
	Index     int
	Data      map[string]string
	State     string // "pending", or "skipped" for a row the source could not read
	ErrorKind string
	ErrorMsg  string
}

// RunFilter selects runs.
type RunFilter struct {
	States []string
	Offset int
	Limit  int // 0 = 100
}

// RunRepo stores runs.
type RunRepo struct{ s *Store }

const runCols = `id,macro_id,macro_name,macro_version,macro_hash,macro_body,source_id,state,reason,options,profile_ids,row_from,row_to,created_at,started_at,finished_at,updated_at`

// Create stores the run and its ledger rows in one transaction.
func (r *RunRepo) Create(ctx context.Context, run RunRecord, seeds []RowSeed) (RunRecord, error) {
	now := time.Now().UTC()
	if run.ID == "" {
		run.ID = NewID()
	}
	run.CreatedAt, run.UpdatedAt = now, now
	if len(run.Options) == 0 {
		run.Options = json.RawMessage(`{}`)
	}
	pids, _ := json.Marshal(append([]string{}, run.ProfileIDs...))
	err := r.s.tx(ctx, func(t *sql.Tx) error {
		if _, err := t.ExecContext(ctx, `INSERT INTO runs(`+runCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			run.ID, run.MacroID, run.MacroName, run.MacroVersion, run.MacroHash, string(run.MacroBody), run.SourceID, run.State, run.Reason,
			string(run.Options), string(pids), run.RowFrom, run.RowTo, ms(run.CreatedAt), nullMS(run.StartedAt), nullMS(run.FinishedAt), ms(run.UpdatedAt)); err != nil {
			return err
		}
		st, err := t.PrepareContext(ctx, `INSERT INTO run_rows(run_id,row_index,idempotency_key,state,data,error_kind,error_msg,updated_at) VALUES(?,?,?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer st.Close()
		for _, sd := range seeds {
			state := sd.State
			if state == "" {
				state = "pending"
			}
			data, err := json.Marshal(sd.Data)
			if err != nil {
				return err
			}
			if _, err := st.ExecContext(ctx, run.ID, sd.Index, IdempotencyKey(run.ID, sd.Index), state, string(data), sd.ErrorKind, sd.ErrorMsg, ms(now)); err != nil {
				return err
			}
		}
		return nil
	})
	return run, err
}

// IdempotencyKey is the key of one row of one run.
func IdempotencyKey(runID string, index int) string { return fmt.Sprintf("%s:%d", runID, index) }

func scanRun(sc interface{ Scan(...any) error }) (RunRecord, error) {
	var r RunRecord
	var body, opts, pids string
	var c, u int64
	var st, fin sql.NullInt64
	if err := sc.Scan(&r.ID, &r.MacroID, &r.MacroName, &r.MacroVersion, &r.MacroHash, &body, &r.SourceID, &r.State, &r.Reason,
		&opts, &pids, &r.RowFrom, &r.RowTo, &c, &st, &fin, &u); err != nil {
		return r, err
	}
	r.MacroBody, r.Options = []byte(body), json.RawMessage(opts)
	if err := json.Unmarshal([]byte(pids), &r.ProfileIDs); err != nil {
		return r, err
	}
	r.CreatedAt, r.StartedAt, r.FinishedAt, r.UpdatedAt = fromMS(c), scanMS(st), scanMS(fin), fromMS(u)
	return r, nil
}

// Get returns one run.
func (r *RunRepo) Get(ctx context.Context, id string) (RunRecord, error) {
	run, err := scanRun(r.s.db.QueryRowContext(ctx, `SELECT `+runCols+` FROM runs WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return run, ErrNotFound
	}
	return run, err
}

// List returns runs, newest first.
func (r *RunRepo) List(ctx context.Context, f RunFilter) ([]RunRecord, error) {
	q := `SELECT ` + runCols + ` FROM runs`
	var args []any
	if len(f.States) > 0 {
		q += ` WHERE state IN (` + placeholders(len(f.States)) + `)`
		for _, s := range f.States {
			args = append(args, s)
		}
	}
	if f.Limit <= 0 {
		f.Limit = 100
	}
	q += ` ORDER BY created_at DESC, id LIMIT ? OFFSET ?`
	args = append(args, f.Limit, f.Offset)
	rows, err := r.s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunRecord
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// SetState writes the run state and reason. started_at is set the first time
// the state is "running"; finished_at is set in "finished" and "failed" and
// cleared in any other state.
func (r *RunRepo) SetState(ctx context.Context, id, state, reason string) error {
	now := ms(time.Now())
	res, err := r.s.db.ExecContext(ctx, `UPDATE runs SET state=?, reason=?, updated_at=?,
started_at=CASE WHEN ?='running' AND started_at IS NULL THEN ? ELSE started_at END,
finished_at=CASE WHEN ? IN ('finished','failed') THEN ? ELSE NULL END WHERE id=?`,
		state, reason, now, state, now, state, now, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a run with its ledger rows and events.
func (r *RunRepo) Delete(ctx context.Context, id string) error {
	res, err := r.s.db.ExecContext(ctx, `DELETE FROM runs WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// ---- ledger rows -----------------------------------------------------------------

// RowRecord is one ledger row.
type RowRecord struct {
	RunID          string
	Index          int
	IdempotencyKey string
	State          string // pending, running, done, failed, skipped, uncertain
	CommitState    string // none, submitting, submitted
	ProfileID      string
	Attempts       int
	Data           map[string]string
	ErrorKind      string
	ErrorMsg       string
	StepID         string
	StartedAt      *time.Time
	FinishedAt     *time.Time
	UpdatedAt      time.Time
}

// RowQuery selects ledger rows of one run in index order.
type RowQuery struct {
	RunID   string
	States  []string
	Indexes []int
	Offset  int
	Limit   int // 0 = 100
}

// RowRepo is the ledger.
type RowRepo struct{ s *Store }

const rowCols = `run_id,row_index,idempotency_key,state,commit_state,profile_id,attempts,data,error_kind,error_msg,step_id,started_at,finished_at,updated_at`

func scanRow(sc interface{ Scan(...any) error }) (RowRecord, error) {
	var r RowRecord
	var data string
	var st, fin sql.NullInt64
	var u int64
	if err := sc.Scan(&r.RunID, &r.Index, &r.IdempotencyKey, &r.State, &r.CommitState, &r.ProfileID, &r.Attempts, &data, &r.ErrorKind, &r.ErrorMsg, &r.StepID, &st, &fin, &u); err != nil {
		return r, err
	}
	if err := json.Unmarshal([]byte(data), &r.Data); err != nil {
		return r, err
	}
	r.StartedAt, r.FinishedAt, r.UpdatedAt = scanMS(st), scanMS(fin), fromMS(u)
	return r, nil
}

// Claim takes the lowest pending row of the run for profileID and marks it
// running in one statement, so two workers never get the same row.
func (r *RowRepo) Claim(ctx context.Context, runID, profileID string) (RowRecord, bool, error) {
	now := ms(time.Now())
	row, err := scanRow(r.s.db.QueryRowContext(ctx, `UPDATE run_rows SET state='running', profile_id=?, attempts=attempts+1, started_at=?, finished_at=NULL, updated_at=?, error_kind='', error_msg='', step_id=''
WHERE run_id=? AND row_index=(SELECT MIN(row_index) FROM run_rows WHERE run_id=? AND state='pending') RETURNING `+rowCols,
		profileID, now, now, runID, runID))
	if errors.Is(err, sql.ErrNoRows) {
		return row, false, nil
	}
	return row, err == nil, err
}

// SetCommit records the commit phase of a running row ("submitting" before the
// click, "submitted" after it succeeded). The write is durable on return.
func (r *RowRepo) SetCommit(ctx context.Context, runID string, index int, commit, stepID string) error {
	res, err := r.s.db.ExecContext(ctx, `UPDATE run_rows SET commit_state=?, step_id=?, updated_at=? WHERE run_id=? AND row_index=? AND state='running'`,
		commit, stepID, ms(time.Now()), runID, index)
	return conflictIfNone(res, err)
}

// Finish moves a running row to a final state.
func (r *RowRepo) Finish(ctx context.Context, runID string, index int, state, errKind, errMsg, stepID string) error {
	now := ms(time.Now())
	res, err := r.s.db.ExecContext(ctx, `UPDATE run_rows SET state=?, error_kind=?, error_msg=?, step_id=CASE WHEN ?='' THEN step_id ELSE ? END, finished_at=?, updated_at=?
WHERE run_id=? AND row_index=? AND state='running'`, state, errKind, errMsg, stepID, stepID, now, now, runID, index)
	return conflictIfNone(res, err)
}

// Release puts a running row back to pending. It refuses a row that passed
// its commit point.
func (r *RowRepo) Release(ctx context.Context, runID string, index int) error {
	res, err := r.s.db.ExecContext(ctx, `UPDATE run_rows SET state='pending', profile_id='', started_at=NULL, updated_at=?
WHERE run_id=? AND row_index=? AND state='running' AND commit_state='none'`, ms(time.Now()), runID, index)
	return conflictIfNone(res, err)
}

// Resolve sets the user's decision on an uncertain row: "done" or "failed".
func (r *RowRepo) Resolve(ctx context.Context, runID string, index int, state string) error {
	if state != "done" && state != "failed" {
		return fmt.Errorf("store: cannot resolve a row to %q", state)
	}
	msg := "marked failed by the user"
	if state == "done" {
		msg = "marked done by the user"
	}
	res, err := r.s.db.ExecContext(ctx, `UPDATE run_rows SET state=?, error_kind='resolved', error_msg=?, finished_at=?, updated_at=?
WHERE run_id=? AND row_index=? AND state='uncertain'`, state, msg, ms(time.Now()), ms(time.Now()), runID, index)
	return conflictIfNone(res, err)
}

func conflictIfNone(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrConflict
	}
	return nil
}

// Get returns one ledger row.
func (r *RowRepo) Get(ctx context.Context, runID string, index int) (RowRecord, error) {
	row, err := scanRow(r.s.db.QueryRowContext(ctx, `SELECT `+rowCols+` FROM run_rows WHERE run_id=? AND row_index=?`, runID, index))
	if errors.Is(err, sql.ErrNoRows) {
		return row, ErrNotFound
	}
	return row, err
}

func (q RowQuery) where() (string, []any) {
	w, args := ` WHERE run_id=?`, []any{q.RunID}
	if len(q.States) > 0 {
		w += ` AND state IN (` + placeholders(len(q.States)) + `)`
		for _, s := range q.States {
			args = append(args, s)
		}
	}
	if len(q.Indexes) > 0 {
		w += ` AND row_index IN (` + placeholders(len(q.Indexes)) + `)`
		for _, i := range q.Indexes {
			args = append(args, i)
		}
	}
	return w, args
}

// List returns one page of rows and the number of rows the filter matches.
func (r *RowRepo) List(ctx context.Context, q RowQuery) ([]RowRecord, int, error) {
	w, args := q.where()
	var total int
	if err := r.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_rows`+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if q.Limit <= 0 {
		q.Limit = 100
	}
	rows, err := r.s.db.QueryContext(ctx, `SELECT `+rowCols+` FROM run_rows`+w+` ORDER BY row_index LIMIT ? OFFSET ?`, append(args, q.Limit, q.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []RowRecord
	for rows.Next() {
		row, err := scanRow(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, row)
	}
	return out, total, rows.Err()
}

// Counts returns the number of rows per state.
func (r *RowRepo) Counts(ctx context.Context, runID string) (map[string]int, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT state, COUNT(*) FROM run_rows WHERE run_id=? GROUP BY state`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return nil, err
		}
		out[s] = n
	}
	return out, rows.Err()
}

// Recover settles the rows a dead process left running: a row that passed its
// commit point becomes uncertain, any other goes back to pending.
func (r *RowRepo) Recover(ctx context.Context, runID string) (toPending, toUncertain int, err error) {
	now := ms(time.Now())
	err = r.s.tx(ctx, func(t *sql.Tx) error {
		res, err := t.ExecContext(ctx, `UPDATE run_rows SET state='uncertain', error_kind='crash', error_msg='the process stopped after the commit point; the submit may have happened', finished_at=?, updated_at=?
WHERE run_id=? AND state='running' AND commit_state<>'none'`, now, now, runID)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		toUncertain = int(n)
		res, err = t.ExecContext(ctx, `UPDATE run_rows SET state='pending', profile_id='', started_at=NULL, updated_at=? WHERE run_id=? AND state='running' AND commit_state='none'`, now, runID)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		toPending = int(n)
		return nil
	})
	return
}

// Reset puts the rows that match q (restricted to failed and skipped rows)
// back to pending. Uncertain rows are never reset.
func (r *RowRepo) Reset(ctx context.Context, q RowQuery) (int, error) {
	allowed := map[string]bool{"failed": true, "skipped": true}
	var states []string
	for _, s := range q.States {
		if allowed[s] {
			states = append(states, s)
		}
	}
	if len(q.States) == 0 {
		states = []string{"failed", "skipped"}
	}
	if len(states) == 0 {
		return 0, nil
	}
	q.States = states
	w, args := q.where()
	res, err := r.s.db.ExecContext(ctx, `UPDATE run_rows SET state='pending', commit_state='none', profile_id='', error_kind='', error_msg='', step_id='', started_at=NULL, finished_at=NULL, updated_at=?`+w,
		append([]any{ms(time.Now())}, args...)...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ---- events -----------------------------------------------------------------------

// EventRecord is one line of the append-only run log.
type EventRecord struct {
	ID       int64
	RunID    string
	RowIndex *int
	Time     time.Time
	Kind     string
	Data     json.RawMessage
}

// EventRepo is the append-only log.
type EventRepo struct{ s *Store }

// Append adds one event. Events are never updated.
func (r *EventRepo) Append(ctx context.Context, runID string, rowIndex *int, kind string, data any) (int64, error) {
	b := []byte(`{}`)
	if data != nil {
		var err error
		if b, err = json.Marshal(data); err != nil {
			return 0, err
		}
	}
	var ri any
	if rowIndex != nil {
		ri = *rowIndex
	}
	res, err := r.s.db.ExecContext(ctx, `INSERT INTO run_events(run_id,row_index,ts,kind,data) VALUES(?,?,?,?,?)`, runID, ri, ms(time.Now()), kind, string(b))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// List returns events with id > after, oldest first.
func (r *EventRepo) List(ctx context.Context, runID string, after int64, limit int) ([]EventRecord, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := r.s.db.QueryContext(ctx, `SELECT id,run_id,row_index,ts,kind,data FROM run_events WHERE run_id=? AND id>? ORDER BY id LIMIT ?`, runID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventRecord
	for rows.Next() {
		var e EventRecord
		var ri sql.NullInt64
		var ts int64
		var data string
		if err := rows.Scan(&e.ID, &e.RunID, &ri, &ts, &e.Kind, &data); err != nil {
			return nil, err
		}
		if ri.Valid {
			i := int(ri.Int64)
			e.RowIndex = &i
		}
		e.Time, e.Data = fromMS(ts), json.RawMessage(data)
		out = append(out, e)
	}
	return out, rows.Err()
}
