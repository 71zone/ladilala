package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/8bu/ladilala/core/macro"
	"github.com/8bu/ladilala/core/profile"
)

// ---- proxies -------------------------------------------------------------------

// ProxyRecord is one saved proxy. The password is in Secrets under the key
// ProxySecretKey(ID), never in the table.
type ProxyRecord struct {
	ID        string
	Name      string
	Scheme    profile.ProxyScheme
	Host      string
	Port      int
	Username  string
	HasSecret bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ProxySecretKey is the Secrets key of a proxy password.
func ProxySecretKey(id string) string { return "proxy/" + id }

// ProxyRepo stores proxies.
type ProxyRepo struct{ s *Store }

// Put inserts or updates a proxy (empty ID: a new ID is made). password nil
// keeps the stored password; a pointer to "" removes it.
func (r *ProxyRepo) Put(ctx context.Context, p ProxyRecord, password *string) (ProxyRecord, error) {
	now := time.Now().UTC()
	if p.ID == "" {
		p.ID = NewID()
	}
	var created sql.NullInt64
	err := r.s.db.QueryRowContext(ctx, `SELECT created_at, has_secret FROM proxies WHERE id=?`, p.ID).Scan(&created, new(int))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return p, err
	}
	p.CreatedAt = now
	if created.Valid {
		p.CreatedAt = fromMS(created.Int64)
	}
	p.UpdatedAt = now
	if password != nil {
		if r.s.Secrets == nil {
			return p, errors.New("store: no secret store configured")
		}
		if *password == "" {
			if err := r.s.Secrets.Delete(ctx, ProxySecretKey(p.ID)); err != nil {
				return p, err
			}
			p.HasSecret = false
		} else {
			if err := r.s.Secrets.Set(ctx, ProxySecretKey(p.ID), *password); err != nil {
				return p, err
			}
			p.HasSecret = true
		}
	} else if created.Valid {
		if err := r.s.db.QueryRowContext(ctx, `SELECT has_secret FROM proxies WHERE id=?`, p.ID).Scan(&p.HasSecret); err != nil {
			return p, err
		}
	}
	_, err = r.s.db.ExecContext(ctx, `INSERT INTO proxies(id,name,scheme,host,port,username,has_secret,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name, scheme=excluded.scheme, host=excluded.host, port=excluded.port, username=excluded.username, has_secret=excluded.has_secret, updated_at=excluded.updated_at`,
		p.ID, p.Name, string(p.Scheme), p.Host, p.Port, p.Username, p.HasSecret, ms(p.CreatedAt), ms(p.UpdatedAt))
	return p, err
}

const proxyCols = `id,name,scheme,host,port,username,has_secret,created_at,updated_at`

func scanProxy(sc interface{ Scan(...any) error }) (ProxyRecord, error) {
	var p ProxyRecord
	var scheme string
	var c, u int64
	if err := sc.Scan(&p.ID, &p.Name, &scheme, &p.Host, &p.Port, &p.Username, &p.HasSecret, &c, &u); err != nil {
		return p, err
	}
	p.Scheme, p.CreatedAt, p.UpdatedAt = profile.ProxyScheme(scheme), fromMS(c), fromMS(u)
	return p, nil
}

// Get returns one proxy.
func (r *ProxyRepo) Get(ctx context.Context, id string) (ProxyRecord, error) {
	p, err := scanProxy(r.s.db.QueryRowContext(ctx, `SELECT `+proxyCols+` FROM proxies WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// List returns all proxies by name.
func (r *ProxyRepo) List(ctx context.Context) ([]ProxyRecord, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT `+proxyCols+` FROM proxies ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProxyRecord
	for rows.Next() {
		p, err := scanProxy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Password reads the proxy password from the secret store.
func (r *ProxyRepo) Password(ctx context.Context, id string) (string, error) {
	if r.s.Secrets == nil {
		return "", errors.New("store: no secret store configured")
	}
	return r.s.Secrets.Get(ctx, ProxySecretKey(id))
}

// Delete removes a proxy and its password. Profiles that used it keep no proxy.
func (r *ProxyRepo) Delete(ctx context.Context, id string) error {
	res, err := r.s.db.ExecContext(ctx, `DELETE FROM proxies WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if r.s.Secrets != nil {
		return r.s.Secrets.Delete(ctx, ProxySecretKey(id))
	}
	return nil
}

// ---- profiles ------------------------------------------------------------------

// ProfileRecord is a profile plus its storage fields. Profile.Proxy is not
// stored: it is built from ProxyID on read and ignored on write.
type ProfileRecord struct {
	profile.Profile
	ProxyID   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ProfileRepo stores profiles.
type ProfileRepo struct{ s *Store }

// Put inserts or updates a profile (empty ID: a new ID is made).
func (r *ProfileRepo) Put(ctx context.Context, p ProfileRecord) (ProfileRecord, error) {
	now := time.Now().UTC()
	if p.ID == "" {
		p.ID = NewID()
	}
	fp, err := json.Marshal(p.Fingerprint)
	if err != nil {
		return p, err
	}
	tags, _ := json.Marshal(append([]string{}, p.Tags...))
	var proxyID any
	if p.ProxyID != "" {
		proxyID = p.ProxyID
	}
	var created sql.NullInt64
	err = r.s.db.QueryRowContext(ctx, `SELECT created_at FROM profiles WHERE id=?`, p.ID).Scan(&created)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return p, err
	}
	p.CreatedAt = now
	if created.Valid {
		p.CreatedAt = fromMS(created.Int64)
	}
	p.UpdatedAt = now
	_, err = r.s.db.ExecContext(ctx, `INSERT INTO profiles(id,name,grp,tags,notes,proxy_id,fingerprint,data_dir,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name, grp=excluded.grp, tags=excluded.tags, notes=excluded.notes, proxy_id=excluded.proxy_id, fingerprint=excluded.fingerprint, data_dir=excluded.data_dir, updated_at=excluded.updated_at`,
		p.ID, p.Name, p.Group, string(tags), p.Notes, proxyID, string(fp), p.DataDir, ms(p.CreatedAt), ms(p.UpdatedAt))
	return p, err
}

// Get returns a profile with its proxy resolved, password included.
func (r *ProfileRepo) Get(ctx context.Context, id string) (ProfileRecord, error) {
	var p ProfileRecord
	var tags, fp string
	var proxyID sql.NullString
	var c, u int64
	err := r.s.db.QueryRowContext(ctx, `SELECT id,name,grp,tags,notes,proxy_id,fingerprint,data_dir,created_at,updated_at FROM profiles WHERE id=?`, id).
		Scan(&p.ID, &p.Name, &p.Group, &tags, &p.Notes, &proxyID, &fp, &p.DataDir, &c, &u)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	p.CreatedAt, p.UpdatedAt = fromMS(c), fromMS(u)
	if err := json.Unmarshal([]byte(tags), &p.Tags); err != nil {
		return p, fmt.Errorf("store: profile %s tags: %w", id, err)
	}
	if len(p.Tags) == 0 {
		p.Tags = nil
	}
	if err := json.Unmarshal([]byte(fp), &p.Fingerprint); err != nil {
		return p, fmt.Errorf("store: profile %s fingerprint: %w", id, err)
	}
	if proxyID.Valid {
		p.ProxyID = proxyID.String
		pr, err := r.s.Proxies.Get(ctx, p.ProxyID)
		if err != nil {
			return p, err
		}
		p.Proxy = profile.Proxy{Scheme: pr.Scheme, Host: pr.Host, Port: pr.Port, Username: pr.Username}
		if pr.HasSecret {
			if p.Proxy.Password, err = r.s.Proxies.Password(ctx, pr.ID); err != nil {
				return p, err
			}
		}
	}
	return p, nil
}

// List returns all profiles by name, proxies resolved.
func (r *ProfileRepo) List(ctx context.Context) ([]ProfileRecord, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT id FROM profiles ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]ProfileRecord, 0, len(ids))
	for _, id := range ids {
		p, err := r.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// Delete removes a profile row. It does not touch the data directory.
func (r *ProfileRepo) Delete(ctx context.Context, id string) error {
	res, err := r.s.db.ExecContext(ctx, `DELETE FROM profiles WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- macros --------------------------------------------------------------------

// MacroRecord is the stored form of a macro. Body is the canonical JSON.
type MacroRecord struct {
	ID        string
	Name      string
	Version   int // 1, and +1 each time the body changes
	Hash      string
	Body      []byte
	CreatedAt time.Time
	UpdatedAt time.Time
}

// MacroRepo stores macros.
type MacroRepo struct{ s *Store }

// HashBody returns the hash stored for a macro body.
func HashBody(body []byte) string {
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:])
}

// Put validates and stores m under m.ID. The version rises only when the
// canonical JSON changed.
func (r *MacroRepo) Put(ctx context.Context, m *macro.Macro) (MacroRecord, error) {
	if err := macro.Validate(m); err != nil {
		return MacroRecord{}, err
	}
	body, err := macro.Marshal(m)
	if err != nil {
		return MacroRecord{}, err
	}
	rec := MacroRecord{ID: m.ID, Name: m.Name, Hash: HashBody(body), Body: body, Version: 1}
	now := time.Now().UTC()
	rec.CreatedAt, rec.UpdatedAt = now, now
	err = r.s.tx(ctx, func(t *sql.Tx) error {
		var ver int
		var hash string
		var created int64
		switch err := t.QueryRowContext(ctx, `SELECT version,hash,created_at FROM macros WHERE id=?`, m.ID).Scan(&ver, &hash, &created); {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		default:
			rec.CreatedAt = fromMS(created)
			rec.Version = ver
			if hash != rec.Hash {
				rec.Version = ver + 1
			}
		}
		_, err := t.ExecContext(ctx, `INSERT INTO macros(id,name,version,hash,body,created_at,updated_at) VALUES(?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name, version=excluded.version, hash=excluded.hash, body=excluded.body, updated_at=excluded.updated_at`,
			rec.ID, rec.Name, rec.Version, rec.Hash, string(body), ms(rec.CreatedAt), ms(rec.UpdatedAt))
		return err
	})
	return rec, err
}

// Get returns a macro record with its body.
func (r *MacroRepo) Get(ctx context.Context, id string) (MacroRecord, error) {
	var m MacroRecord
	var body string
	var c, u int64
	err := r.s.db.QueryRowContext(ctx, `SELECT id,name,version,hash,body,created_at,updated_at FROM macros WHERE id=?`, id).
		Scan(&m.ID, &m.Name, &m.Version, &m.Hash, &body, &c, &u)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	m.Body, m.CreatedAt, m.UpdatedAt = []byte(body), fromMS(c), fromMS(u)
	return m, err
}

// List returns macro records without bodies, newest first.
func (r *MacroRepo) List(ctx context.Context) ([]MacroRecord, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT id,name,version,hash,created_at,updated_at FROM macros ORDER BY updated_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MacroRecord
	for rows.Next() {
		var m MacroRecord
		var c, u int64
		if err := rows.Scan(&m.ID, &m.Name, &m.Version, &m.Hash, &c, &u); err != nil {
			return nil, err
		}
		m.CreatedAt, m.UpdatedAt = fromMS(c), fromMS(u)
		out = append(out, m)
	}
	return out, rows.Err()
}

// Delete removes a macro. Runs keep their own copy of the body.
func (r *MacroRepo) Delete(ctx context.Context, id string) error {
	res, err := r.s.db.ExecContext(ctx, `DELETE FROM macros WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- data sources --------------------------------------------------------------

// SourceRecord is the saved configuration of a data source. Config must not
// hold secrets (tokens go to Secrets, the config keeps a reference).
type SourceRecord struct {
	ID        string
	Name      string
	Kind      string // csv, excel, json, rest, gsheet
	Config    json.RawMessage
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SourceRepo stores data source configs.
type SourceRepo struct{ s *Store }

// Put inserts or updates a source (empty ID: a new ID is made).
func (r *SourceRepo) Put(ctx context.Context, d SourceRecord) (SourceRecord, error) {
	now := time.Now().UTC()
	if d.ID == "" {
		d.ID = NewID()
	}
	if len(d.Config) == 0 {
		d.Config = json.RawMessage(`{}`)
	}
	if !json.Valid(d.Config) {
		return d, errors.New("store: source config is not valid JSON")
	}
	var created sql.NullInt64
	err := r.s.db.QueryRowContext(ctx, `SELECT created_at FROM data_sources WHERE id=?`, d.ID).Scan(&created)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return d, err
	}
	d.CreatedAt = now
	if created.Valid {
		d.CreatedAt = fromMS(created.Int64)
	}
	d.UpdatedAt = now
	_, err = r.s.db.ExecContext(ctx, `INSERT INTO data_sources(id,name,kind,config,created_at,updated_at) VALUES(?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name, kind=excluded.kind, config=excluded.config, updated_at=excluded.updated_at`,
		d.ID, d.Name, d.Kind, string(d.Config), ms(d.CreatedAt), ms(d.UpdatedAt))
	return d, err
}

// Get returns one source config.
func (r *SourceRepo) Get(ctx context.Context, id string) (SourceRecord, error) {
	var d SourceRecord
	var cfg string
	var c, u int64
	err := r.s.db.QueryRowContext(ctx, `SELECT id,name,kind,config,created_at,updated_at FROM data_sources WHERE id=?`, id).Scan(&d.ID, &d.Name, &d.Kind, &cfg, &c, &u)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	d.Config, d.CreatedAt, d.UpdatedAt = json.RawMessage(cfg), fromMS(c), fromMS(u)
	return d, err
}

// List returns all source configs by name.
func (r *SourceRepo) List(ctx context.Context) ([]SourceRecord, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT id,name,kind,config,created_at,updated_at FROM data_sources ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SourceRecord
	for rows.Next() {
		var d SourceRecord
		var cfg string
		var c, u int64
		if err := rows.Scan(&d.ID, &d.Name, &d.Kind, &cfg, &c, &u); err != nil {
			return nil, err
		}
		d.Config, d.CreatedAt, d.UpdatedAt = json.RawMessage(cfg), fromMS(c), fromMS(u)
		out = append(out, d)
	}
	return out, rows.Err()
}

// Delete removes a source config.
func (r *SourceRepo) Delete(ctx context.Context, id string) error {
	res, err := r.s.db.ExecContext(ctx, `DELETE FROM data_sources WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
