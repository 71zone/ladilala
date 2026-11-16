package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type migration struct {
	version  int
	name     string
	up, down string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	byVer := map[int]*migration{}
	for _, e := range entries {
		name := e.Name()
		parts := strings.SplitN(name, "_", 2)
		v, err := strconv.Atoi(parts[0])
		if err != nil || len(parts) != 2 {
			return nil, fmt.Errorf("store: bad migration file name %q", name)
		}
		raw, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, err
		}
		m := byVer[v]
		if m == nil {
			m = &migration{version: v}
			byVer[v] = m
		}
		switch {
		case strings.HasSuffix(name, ".up.sql"):
			m.up, m.name = string(raw), strings.TrimSuffix(parts[1], ".up.sql")
		case strings.HasSuffix(name, ".down.sql"):
			m.down = string(raw)
		default:
			return nil, fmt.Errorf("store: bad migration file name %q", name)
		}
	}
	out := make([]migration, 0, len(byVer))
	for _, m := range byVer {
		if m.up == "" || m.down == "" {
			return nil, fmt.Errorf("store: migration %d needs an up and a down file", m.version)
		}
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// LatestVersion is the highest migration this build knows.
func LatestVersion() (int, error) {
	ms, err := loadMigrations()
	if err != nil || len(ms) == 0 {
		return 0, err
	}
	return ms[len(ms)-1].version, nil
}

func (s *Store) ensureMigrationTable(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at INTEGER NOT NULL)`)
	return err
}

// Version returns the applied schema version (0 on an empty database).
func (s *Store) Version(ctx context.Context) (int, error) {
	if err := s.ensureMigrationTable(ctx); err != nil {
		return 0, err
	}
	var v sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, err
	}
	return int(v.Int64), nil
}

// Migrate applies every migration above the current version. Calling it again
// does nothing.
func (s *Store) Migrate(ctx context.Context) error {
	all, err := loadMigrations()
	if err != nil {
		return err
	}
	cur, err := s.Version(ctx)
	if err != nil {
		return err
	}
	for _, m := range all {
		if m.version <= cur {
			continue
		}
		err := s.tx(ctx, func(t *sql.Tx) error {
			if _, err := t.ExecContext(ctx, m.up); err != nil {
				return err
			}
			_, err := t.ExecContext(ctx, `INSERT INTO schema_migrations(version,name,applied_at) VALUES(?,?,?)`, m.version, m.name, time.Now().UnixMilli())
			return err
		})
		if err != nil {
			return fmt.Errorf("store: migration %d (%s): %w", m.version, m.name, err)
		}
	}
	return nil
}

// MigrateDown reverts migrations until the schema is at version target. It
// destroys data of the reverted tables.
func (s *Store) MigrateDown(ctx context.Context, target int) error {
	all, err := loadMigrations()
	if err != nil {
		return err
	}
	cur, err := s.Version(ctx)
	if err != nil {
		return err
	}
	for i := len(all) - 1; i >= 0; i-- {
		m := all[i]
		if m.version > cur || m.version <= target {
			continue
		}
		err := s.tx(ctx, func(t *sql.Tx) error {
			if _, err := t.ExecContext(ctx, m.down); err != nil {
				return err
			}
			_, err := t.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=?`, m.version)
			return err
		})
		if err != nil {
			return fmt.Errorf("store: revert %d (%s): %w", m.version, m.name, err)
		}
	}
	return nil
}
