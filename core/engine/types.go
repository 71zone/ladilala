// Package engine runs a macro over the rows of a data source on a pool of
// browser profiles. It keeps a ledger in core/store so a crash never repeats
// a submit. Design: docs/run-engine.md.
package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/8bu/ladilala/core/macro"
	"github.com/8bu/ladilala/core/player"
	"github.com/8bu/ladilala/core/profile"
)

// RunState is the state of a run.
type RunState string

const (
	StateCreated          RunState = "created"
	StateRunning          RunState = "running"
	StatePaused           RunState = "paused"
	StateTakeover         RunState = "takeover"
	StateStopping         RunState = "stopping"
	StateFinished         RunState = "finished"
	StateFailed           RunState = "failed"
	StateCrashedRecovered RunState = "crashed_recovered"
)

// RowState is the state of one ledger row.
type RowState string

const (
	RowPending   RowState = "pending"
	RowRunning   RowState = "running"
	RowDone      RowState = "done"
	RowFailed    RowState = "failed"
	RowSkipped   RowState = "skipped"
	RowUncertain RowState = "uncertain"
)

// CommitState tells how far a row got through its commit point step.
type CommitState string

const (
	CommitNone       CommitState = "none"
	CommitSubmitting CommitState = "submitting" // written before the click
	CommitSubmitted  CommitState = "submitted"  // the commit step ended without error
)

// Error kinds stored in the ledger.
const (
	KindData     = "data"     // missing column, value the field refuses
	KindOrigin   = "origin"   // outside allowedOrigins
	KindLocator  = "locator"  // element not found, ambiguous or covered
	KindGuard    = "guard"    // a macro guard fired
	KindBrowser  = "browser"  // the browser died
	KindTimeout  = "timeout"  // a step ran out of time
	KindStep     = "step"     // any other step error
	KindCrash    = "crash"    // found in flight after a crash
	KindStopped  = "stopped"  // the run was stopped at the commit point
	KindLedger   = "ledger"   // the ledger write failed
	KindResolved = "resolved" // decided by the user
)

// RunOptions tune one run. Zero values take the defaults in brackets.
type RunOptions struct {
	Concurrency     int  `json:"concurrency"`     // browsers at the same time [1]
	DelayMs         int  `json:"delayMs"`         // pause after each row, per browser [0]
	StopAfterErrors int  `json:"stopAfterErrors"` // consecutive failed rows that pause the run [5]
	MaxRestarts     int  `json:"maxRestarts"`     // browser relaunches per profile [3]
	DryRun          bool `json:"dryRun"`          // skip commit point steps
	TestOne         bool `json:"testOne"`         // run only the first row of the range
}

const (
	defaultStopAfterErrors = 5
	defaultMaxRestarts     = 3
)

func (o *RunOptions) fill() {
	if o.Concurrency <= 0 {
		o.Concurrency = 1
	}
	if o.StopAfterErrors <= 0 {
		o.StopAfterErrors = defaultStopAfterErrors
	}
	if o.MaxRestarts <= 0 {
		o.MaxRestarts = defaultMaxRestarts
	}
}

// Run is a run as the UI sees it.
type Run struct {
	ID           string
	MacroID      string
	MacroName    string
	MacroVersion int
	MacroHash    string
	SourceID     string
	State        RunState
	Reason       string
	Options      RunOptions
	ProfileIDs   []string
	RowFrom      int
	RowTo        int
	Total        int
	Counts       map[RowState]int
	CreatedAt    time.Time
	StartedAt    *time.Time
	FinishedAt   *time.Time
}

// Row is one ledger row as the UI sees it. Data is the copy of the source row;
// it can hold passwords.
type Row struct {
	RunID          string
	Index          int
	IdempotencyKey string
	State          RowState
	CommitState    CommitState
	ProfileID      string
	Attempts       int
	Data           map[string]string
	ErrorKind      string
	ErrorMsg       string
	StepID         string
	StartedAt      *time.Time
	FinishedAt     *time.Time
}

// RowFilter selects ledger rows of one run.
type RowFilter struct {
	States  []RowState
	Indexes []int
	Offset  int
	Limit   int // 0 = 100
}

// RowPage is one page of rows.
type RowPage struct {
	Rows  []Row
	Total int // rows that match the filter
}

// RunFilter selects runs, newest first.
type RunFilter struct {
	States []RunState
	Offset int
	Limit  int // 0 = 100
}

// Resolution is the user's decision on an uncertain row.
type Resolution string

const (
	ResolveDone   Resolution = "done"   // the submit happened
	ResolveFailed Resolution = "failed" // it did not; the row may be retried
)

// EventKind names an event.
type EventKind string

const (
	EventRunState     EventKind = "run.state"
	EventRowStarted   EventKind = "row.started"
	EventRowCommit    EventKind = "row.commit"
	EventRowDone      EventKind = "row.done"
	EventRowFailed    EventKind = "row.failed"
	EventRowSkipped   EventKind = "row.skipped"
	EventRowUncertain EventKind = "row.uncertain"
	EventRowReleased  EventKind = "row.released"
	EventRowResolved  EventKind = "row.resolved"
	EventBrowser      EventKind = "browser"
)

// Event is one thing that happened in a run. RowIndex is -1 for run events.
type Event struct {
	RunID     string
	Kind      EventKind
	Time      time.Time
	RowIndex  int
	ProfileID string
	RunState  RunState // run.state
	RowState  RowState // row.*
	Message   string
	// Dropped counts the events this subscriber lost since the last one it got
	// because it read too slowly.
	Dropped int
}

// Recovery reports what Recover did to one run.
type Recovery struct {
	RunID       string
	BackToQueue int // in-flight rows that go to pending
	Uncertain   int // in-flight rows past their commit point
}

// Errors.
var (
	ErrRunNotFound       = errors.New("engine: run not found")
	ErrBadState          = errors.New("engine: the run is in a state that does not allow this")
	ErrNoRows            = errors.New("engine: the data range has no rows")
	ErrNoProfiles        = errors.New("engine: a run needs at least one profile")
	ErrNotEnoughProfiles = errors.New("engine: concurrency is higher than the number of profiles")
	ErrNotUncertain      = errors.New("engine: the row is not uncertain")
	ErrBrowserDied       = errors.New("engine: the browser died")
)

// StateError says that op is not allowed in the run's state.
type StateError struct {
	RunID string
	State RunState
	Op    string
}

func (e *StateError) Error() string {
	return fmt.Sprintf("engine: cannot %s run %s in state %s", e.Op, e.RunID, e.State)
}
func (e *StateError) Unwrap() error { return ErrBadState }

// GuardFiredError is returned by ReleaseTakeover when the page still fails a
// guard of the macro. The run stays in takeover.
type GuardFiredError struct{ Err error }

func (e *GuardFiredError) Error() string {
	return "engine: the page still fails a guard: " + e.Err.Error()
}
func (e *GuardFiredError) Unwrap() error { return e.Err }

// Browser is one running browser of one profile.
type Browser interface {
	// PlayRow plays m for one row on a fresh tab and closes the tab. It returns
	// ErrBrowserDied (wrapped is fine) when the browser is gone.
	PlayRow(ctx context.Context, m *macro.Macro, row map[string]string, h player.Hooks) error
	// Recheck checks the guards of m against the tab of the row in progress.
	Recheck(ctx context.Context, m *macro.Macro) error
	// Done is closed when the browser process has exited.
	Done() <-chan struct{}
	Close() error
}

// Launcher starts browsers.
type Launcher interface {
	Launch(ctx context.Context, p profile.Profile) (Browser, error)
}
