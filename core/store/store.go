// Package store keeps Ladilala data in one SQLite file (pure Go driver, no
// cgo) and secrets in a separate Secrets store. Tables: docs/data-model.md.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// EnvDataDir overrides the data folder (tests, portable installs).
const EnvDataDir = "LADILALA_DATA"

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("store: not found")

// DataDir returns the data folder: $LADILALA_DATA, else %APPDATA%\Ladilala on
// Windows and os.UserConfigDir()/Ladilala elsewhere. It does not create it.
func DataDir() (string, error) {
	if d := os.Getenv(EnvDataDir); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "Ladilala"), nil
}

// Store is the open database and its repositories.
type Store struct {
	db  *sql.DB
	dir string

	Secrets  Secrets
	Profiles *ProfileRepo
	Proxies  *ProxyRepo
	Macros   *MacroRepo
	Sources  *SourceRepo
	Runs     *RunRepo
	Rows     *RowRepo
	Events   *EventRepo
}

// Open opens (creating it if needed) ladilala.db in dir and applies all
// migrations. secrets may be nil only when no proxy has a password.
func Open(ctx context.Context, dir string, secrets Secrets) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	q := url.Values{}
	for _, p := range []string{"busy_timeout(10000)", "journal_mode(WAL)", "foreign_keys(1)", "synchronous(FULL)"} {
		q.Add("_pragma", p)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "ladilala.db")+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db, dir: dir, Secrets: secrets}
	if err := s.Migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	s.Profiles = &ProfileRepo{s}
	s.Proxies = &ProxyRepo{s}
	s.Macros = &MacroRepo{s}
	s.Sources = &SourceRepo{s}
	s.Runs = &RunRepo{s}
	s.Rows = &RowRepo{s}
	s.Events = &EventRepo{s}
	return s, nil
}

// Dir is the folder that holds the database.
func (s *Store) Dir() string { return s.dir }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the handle for tests and diagnostics.
func (s *Store) DB() *sql.DB { return s.db }

// tx runs f in a transaction.
func (s *Store) tx(ctx context.Context, f func(*sql.Tx) error) error {
	t, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := f(t); err != nil {
		_ = t.Rollback()
		return err
	}
	return t.Commit()
}

// NewID returns a random 128-bit hex id.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("store: no randomness: %v", err))
	}
	return hex.EncodeToString(b[:])
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func fromMS(v int64) time.Time { return time.UnixMilli(v).UTC() }

func nullMS(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixMilli()
}

func scanMS(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := fromMS(v.Int64)
	return &t
}
