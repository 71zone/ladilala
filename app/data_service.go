package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/8bu/ladilala/core/data"
	"github.com/8bu/ladilala/core/macro"
	"github.com/8bu/ladilala/core/store"
)

// DataService is the UI service for data sources: files and REST endpoints,
// previews, and the mapping of macro variables to columns.
type DataService struct {
	b    *Backend
	meta *metaStore
}

func newDataService(b *Backend) *DataService { return &DataService{b: b, meta: metaOf(b)} }

const (
	kindCSV   = "csv"
	kindExcel = "excel"
	kindJSON  = "json"
	kindREST  = "rest"

	maxPreviewRows = 200
	maxIssues      = 500
)

// dataSourceConfig is the stored form of a source. It holds no secret: REST
// header values, the bearer token and the basic password live in the secret
// store under source/<id>/....
type dataSourceConfig struct {
	Path      string `json:"path,omitempty"`
	Encoding  string `json:"encoding,omitempty"`
	Delimiter string `json:"delimiter,omitempty"`
	Sheet     string `json:"sheet,omitempty"`
	Nested    string `json:"nested,omitempty"` // "reject" (default) or "json"

	URL         string   `json:"url,omitempty"`
	RowsPath    string   `json:"rowsPath,omitempty"`
	AuthKind    string   `json:"authKind,omitempty"` // none, bearer, basic
	BasicUser   string   `json:"basicUser,omitempty"`
	HeaderNames []string `json:"headerNames,omitempty"`
	Paging      string   `json:"paging,omitempty"` // "", page, offset, link
	PageParam   string   `json:"pageParam,omitempty"`
	SizeParam   string   `json:"sizeParam,omitempty"`
	OffsetParam string   `json:"offsetParam,omitempty"`
	PageSize    int      `json:"pageSize,omitempty"`
	StartPage   int      `json:"startPage,omitempty"`
	NextPath    string   `json:"nextPath,omitempty"`
	AllowHTTP   bool     `json:"allowHttp,omitempty"`
}

func secretKey(id, what string) string { return "source/" + id + "/" + what }

// SourceDTO is a saved data source. It never carries a secret.
type SourceDTO struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Path        string   `json:"path"`
	Encoding    string   `json:"encoding"`
	Delimiter   string   `json:"delimiter"`
	Sheet       string   `json:"sheet"`
	Nested      string   `json:"nested"`
	URL         string   `json:"url"`
	RowsPath    string   `json:"rowsPath"`
	AuthKind    string   `json:"authKind"`
	BasicUser   string   `json:"basicUser"`
	HasAuth     bool     `json:"hasAuth"`
	HeaderNames []string `json:"headerNames"`
	Paging      string   `json:"paging"`
	PageSize    int      `json:"pageSize"`
	// Columns and RowCount are filled by GetSource. A list does not open the
	// sources: a REST source would send a request. RowCount -1 means unknown.
	Columns   []string `json:"columns"`
	RowCount  int      `json:"rowCount"`
	CreatedAt string   `json:"createdAt"`
	UpdatedAt string   `json:"updatedAt"`
}

func (c dataSourceConfig) dto(rec store.SourceRecord) SourceDTO {
	d := SourceDTO{ID: rec.ID, Name: rec.Name, Kind: rec.Kind, Path: c.Path, Encoding: c.Encoding, Delimiter: c.Delimiter, Sheet: c.Sheet, Nested: c.Nested,
		URL: c.URL, RowsPath: c.RowsPath, AuthKind: c.AuthKind, BasicUser: c.BasicUser, HasAuth: c.AuthKind == "bearer" || c.AuthKind == "basic" || len(c.HeaderNames) > 0,
		HeaderNames: append([]string{}, c.HeaderNames...), Paging: c.Paging, PageSize: c.PageSize, Columns: []string{}, RowCount: -1,
		CreatedAt: stamp(rec.CreatedAt), UpdatedAt: stamp(rec.UpdatedAt)}
	return d
}

// FileSourceInput describes a file source. It is used to inspect a file and to save it.
type FileSourceInput struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Encoding  string `json:"encoding"`  // empty: detect
	Delimiter string `json:"delimiter"` // empty: detect; "tab" for a tab
	Sheet     string `json:"sheet"`     // xlsx; empty: first sheet
	Nested    string `json:"nested"`    // json: "reject" or "json"
}

// RESTSourceInput describes a REST source. Secret fields are sent to the Go
// side only; nothing reads them back.
type RESTSourceInput struct {
	Name          string            `json:"name"`
	URL           string            `json:"url"`
	Headers       map[string]string `json:"headers"`
	AuthKind      string            `json:"authKind"` // none, bearer, basic
	BearerToken   string            `json:"bearerToken"`
	BasicUser     string            `json:"basicUser"`
	BasicPassword string            `json:"basicPassword"`
	RowsPath      string            `json:"rowsPath"`
	Paging        string            `json:"paging"` // "", page, offset, link
	PageParam     string            `json:"pageParam"`
	SizeParam     string            `json:"sizeParam"`
	OffsetParam   string            `json:"offsetParam"`
	PageSize      int               `json:"pageSize"`
	StartPage     int               `json:"startPage"`
	NextPath      string            `json:"nextPath"`
	AllowHTTP     bool              `json:"allowHttp"`
}

// RowIssue is one problem in one row.
type RowIssue struct {
	RowIndex int `json:"rowIndex"`
	// Level is "error" or "warn".
	Level string `json:"level"`
	// Code is ragged, nested, notObject, emptyRequired or noColumn.
	Code   string         `json:"code"`
	Params map[string]any `json:"params,omitempty"`
}

// SourcePreview is the first rows of a source.
type SourcePreview struct {
	Kind      string     `json:"kind"`
	Encoding  string     `json:"encoding"`
	Delimiter string     `json:"delimiter"`
	Sheet     string     `json:"sheet"`
	Sheets    []string   `json:"sheets"`
	Columns   []string   `json:"columns"`
	Rows      [][]string `json:"rows"`
	// From is the index of the first returned row.
	From int `json:"from"`
	// Total is the number of data rows, or -1 when unknown (REST).
	Total int `json:"total"`
	// Duplicates lists header names that appear twice, ignoring case.
	Duplicates []string   `json:"duplicates"`
	Issues     []RowIssue `json:"issues"`
}

// MappingVariable is one macro variable and the column it reads.
type MappingVariable struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
	// Column is the bound column, empty when missing.
	Column string `json:"column"`
	// Suggestion is a column whose name is close (ignoring case, spaces and punctuation) when none is bound.
	Suggestion string `json:"suggestion"`
}

// MappingDTO is the match of macro variables to source columns.
type MappingDTO struct {
	MacroID    string            `json:"macroId"`
	SourceID   string            `json:"sourceId"`
	Variables  []MappingVariable `json:"variables"`
	Columns    []string          `json:"columns"`
	Missing    []string          `json:"missing"`
	Extra      []string          `json:"extra"`
	Duplicates []string          `json:"duplicates"`
	Overrides  map[string]string `json:"overrides"`
	OK         bool              `json:"ok"`
}

// ValidationResult is the answer of ValidateRows.
type ValidationResult struct {
	Checked   int        `json:"checked"`
	Issues    []RowIssue `json:"issues"`
	Truncated bool       `json:"truncated"`
}

// toDataAPIError maps core errors to the contract's codes.
func toDataAPIError(err error) error {
	if err == nil {
		return nil
	}
	var api *APIError
	if errors.As(err, &api) {
		return err
	}
	msg := err.Error()
	switch {
	case errors.Is(err, store.ErrNotFound):
		return apiErr("notFound", msg)
	case errors.Is(err, os.ErrNotExist):
		return apiErr("data.format", "file not found", "reason", "notFound")
	case errors.Is(err, data.ErrEmpty):
		return apiErr("data.format", msg, "reason", "empty")
	case strings.Contains(msg, "HTTP 401"), strings.Contains(msg, "HTTP 403"):
		return apiErr("data.auth", "the server refused the credentials")
	case strings.Contains(msg, "REST request"), strings.Contains(msg, "REST URL"), strings.Contains(msg, "plain HTTP"):
		return apiErr("network", msg, "reason", "rest")
	}
	var re data.RowError
	if errors.As(err, &re) {
		return apiErr("data.format", msg, "rowIndex", re.RowIndex())
	}
	var ve *macro.ValidationError
	if errors.As(err, &ve) {
		return toMacroAPIError(err)
	}
	if strings.Contains(msg, "unsupported file type") || strings.Contains(msg, "sheet") || strings.Contains(msg, "cannot open") ||
		strings.Contains(msg, "JSON") || strings.Contains(msg, "encoding") || strings.Contains(msg, "decod") {
		return apiErr("data.format", msg)
	}
	return apiErr("internal", msg)
}

func delimOf(s string) rune {
	switch s {
	case "":
		return 0
	case "tab", "\\t", "\t":
		return '\t'
	}
	return []rune(s)[0]
}

func delimName(r rune) string {
	if r == '\t' {
		return "tab"
	}
	return string(r)
}

func kindOfPath(path string) (string, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv", ".tsv", ".txt":
		return kindCSV, nil
	case ".json":
		return kindJSON, nil
	case ".xlsx", ".xlsm":
		return kindExcel, nil
	}
	return "", apiErr("data.format", "unsupported file type", "reason", "extension", "ext", filepath.Ext(path))
}

func openFileSource(cfg dataSourceConfig) (data.Source, error) {
	kind, err := kindOfPath(cfg.Path)
	if err != nil {
		return nil, err
	}
	switch kind {
	case kindCSV:
		return data.NewCSV(cfg.Path, data.CSVOptions{Encoding: cfg.Encoding, Delimiter: delimOf(cfg.Delimiter)})
	case kindJSON:
		nested := data.NestedReject
		if cfg.Nested == "json" {
			nested = data.NestedJSON
		}
		return data.NewJSON(cfg.Path, data.JSONOptions{Nested: nested}), nil
	}
	return data.NewXLSX(cfg.Path, data.XLSXOptions{Sheet: cfg.Sheet})
}

func buildRESTConfig(cfg dataSourceConfig, headers map[string]string, token, basicPass string) data.RESTConfig {
	rc := data.RESTConfig{URL: cfg.URL, Headers: headers, Path: cfg.RowsPath, Paging: data.PagingMode(cfg.Paging), PageParam: cfg.PageParam,
		SizeParam: cfg.SizeParam, OffsetParam: cfg.OffsetParam, PageSize: cfg.PageSize, StartPage: cfg.StartPage, NextPath: cfg.NextPath, AllowHTTP: cfg.AllowHTTP}
	switch cfg.AuthKind {
	case "bearer":
		rc.Auth.Bearer = token
	case "basic":
		rc.Auth.BasicUser, rc.Auth.BasicPass = cfg.BasicUser, basicPass
	}
	return rc
}

func decodeConfig(rec store.SourceRecord) (dataSourceConfig, error) {
	var c dataSourceConfig
	if err := json.Unmarshal(rec.Config, &c); err != nil {
		return c, fmt.Errorf("source %s: bad config: %w", rec.ID, err)
	}
	return c, nil
}

// openSourceRecord opens a saved source, reading its secrets. The caller closes it.
func openSourceRecord(ctx context.Context, st *store.Store, rec store.SourceRecord) (data.Source, error) {
	cfg, err := decodeConfig(rec)
	if err != nil {
		return nil, err
	}
	switch rec.Kind {
	case kindCSV, kindExcel, kindJSON:
		return openFileSource(cfg)
	case kindREST:
		var token, pass string
		var headers map[string]string
		get := func(what string) (string, error) {
			v, err := st.Secrets.Get(ctx, secretKey(rec.ID, what))
			if errors.Is(err, store.ErrNoSecret) {
				return "", nil
			}
			return v, err
		}
		if token, err = get("token"); err != nil {
			return nil, err
		}
		if pass, err = get("basicPass"); err != nil {
			return nil, err
		}
		raw, err := get("headers")
		if err != nil {
			return nil, err
		}
		if raw != "" {
			if err := json.Unmarshal([]byte(raw), &headers); err != nil {
				return nil, fmt.Errorf("source %s: bad header secret: %w", rec.ID, err)
			}
		}
		return data.NewREST(buildRESTConfig(cfg, headers, token, pass))
	}
	return nil, apiErr("data.format", "unsupported source kind", "kind", rec.Kind)
}

// openDataSource opens the saved source id. The run engine uses it: the shell
// gives the opened source to engine.CreateRunRequest.Source. The caller closes it.
func openDataSource(ctx context.Context, st *store.Store, id string) (data.Source, error) {
	rec, err := st.Sources.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return openSourceRecord(ctx, st, rec)
}

// previewOf reads the header and rows [from, from+limit) of src.
func previewOf(ctx context.Context, src data.Source, from, limit int) (SourcePreview, error) {
	if limit <= 0 || limit > maxPreviewRows {
		limit = maxPreviewRows
	}
	if from < 0 {
		from = 0
	}
	p := SourcePreview{Rows: [][]string{}, Sheets: []string{}, From: from, Issues: []RowIssue{}, Duplicates: []string{}}
	cols, err := src.Columns(ctx)
	if err != nil {
		return p, err
	}
	p.Columns = cols
	p.Total, err = src.Count(ctx)
	if err != nil {
		return p, err
	}
	rep := data.Mapping{}.Resolve(cols)
	p.Duplicates = append(p.Duplicates, rep.Duplicates...)
	rows, err := src.Open(ctx, from, from+limit)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for {
		_, row, ok, err := rows.Next(ctx)
		var re data.RowError
		if errors.As(err, &re) {
			p.Issues = append(p.Issues, rowErrorIssue(err))
			p.Rows = append(p.Rows, make([]string, len(cols)))
			continue
		}
		if err != nil {
			return p, err
		}
		if !ok {
			break
		}
		cells := make([]string, len(cols))
		for i, c := range cols {
			cells[i] = row[c]
		}
		p.Rows = append(p.Rows, cells)
	}
	switch s := src.(type) {
	case *data.CSVSource:
		p.Kind, p.Encoding, p.Delimiter = kindCSV, s.Encoding(), delimName(s.Delimiter())
	case *data.XLSXSource:
		p.Kind, p.Sheet, p.Sheets = kindExcel, s.Sheet(), s.Sheets()
	case *data.JSONSource:
		p.Kind = kindJSON
	case *data.RESTSource:
		p.Kind = kindREST
	}
	return p, nil
}

func rowErrorIssue(err error) RowIssue {
	var re data.RowError
	if !errors.As(err, &re) {
		return RowIssue{Level: "error", Code: "format"}
	}
	is := RowIssue{RowIndex: re.RowIndex(), Level: "error", Params: map[string]any{}}
	var rg *data.RaggedRowError
	var ne *data.NestedValueError
	var no *data.NotObjectError
	var rv *data.RowValidationError
	switch {
	case errors.As(err, &rg):
		is.Code, is.Params["want"], is.Params["got"] = "ragged", rg.Want, rg.Got
	case errors.As(err, &ne):
		is.Code, is.Params["column"] = "nested", ne.Column
	case errors.As(err, &no):
		is.Code = "notObject"
	case errors.As(err, &rv):
		is.Code, is.Params["variable"], is.Params["column"] = "emptyRequired", rv.Variable, rv.Column
		if rv.Column == "" {
			is.Code = "noColumn"
		}
	default:
		is.Code = "format"
	}
	return is
}

// PickDataFile opens the native file dialog for a CSV, JSON or Excel file. It
// returns an empty path when the user cancels.
func (d *DataService) PickDataFile(_ context.Context) (string, error) {
	return pickOpenPath(d.b, "CSV, JSON, Excel", "*.csv;*.tsv;*.txt;*.json;*.xlsx;*.xlsm")
}

// InspectFile reads the header and the first rows of a file with the given
// options, without saving anything. The wizard calls it again when the user
// changes the encoding, the delimiter or the sheet.
func (d *DataService) InspectFile(ctx context.Context, in FileSourceInput) (SourcePreview, error) {
	cfg := dataSourceConfig{Path: in.Path, Encoding: in.Encoding, Delimiter: in.Delimiter, Sheet: in.Sheet, Nested: in.Nested}
	src, err := openFileSource(cfg)
	if err != nil {
		return SourcePreview{}, toDataAPIError(err)
	}
	defer src.Close()
	p, err := previewOf(ctx, src, 0, 8)
	if err != nil {
		return p, toDataAPIError(err)
	}
	return p, nil
}

// CreateSourceFromFile saves a file source after reading its header.
func (d *DataService) CreateSourceFromFile(ctx context.Context, in FileSourceInput) (SourceDTO, error) {
	kind, err := kindOfPath(in.Path)
	if err != nil {
		return SourceDTO{}, err
	}
	if _, err := os.Stat(in.Path); err != nil {
		return SourceDTO{}, toDataAPIError(err)
	}
	cfg := dataSourceConfig{Path: in.Path, Encoding: in.Encoding, Delimiter: in.Delimiter, Sheet: in.Sheet, Nested: in.Nested}
	src, err := openFileSource(cfg)
	if err != nil {
		return SourceDTO{}, toDataAPIError(err)
	}
	cols, err := src.Columns(ctx)
	_ = src.Close()
	if err != nil {
		return SourceDTO{}, toDataAPIError(err)
	}
	if kind == kindExcel && cfg.Sheet == "" {
		// Pin the sheet the preview showed, so a later file edit cannot move it.
		if x, err := data.NewXLSX(cfg.Path, data.XLSXOptions{}); err == nil {
			cfg.Sheet = x.Sheet()
			_ = x.Close()
		}
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = filepath.Base(in.Path)
	}
	raw, _ := json.Marshal(cfg)
	rec, err := d.b.Store.Sources.Put(ctx, store.SourceRecord{Name: name, Kind: kind, Config: raw})
	if err != nil {
		return SourceDTO{}, toDataAPIError(err)
	}
	out := cfg.dto(rec)
	out.Columns = cols
	return out, nil
}

func normalizeREST(in *RESTSourceInput) error {
	in.URL = strings.TrimSpace(in.URL)
	switch in.AuthKind {
	case "", "none":
		in.AuthKind = "none"
	case "bearer", "basic":
	default:
		return apiErr("invalid", "unknown auth kind", "problems", []ProblemDTO{{Path: "authKind", Msg: "must be none, bearer or basic"}})
	}
	switch in.Paging {
	case "", "page", "offset", "link":
	default:
		return apiErr("invalid", "unknown paging", "problems", []ProblemDTO{{Path: "paging", Msg: "must be page, offset or link"}})
	}
	return nil
}

func (in RESTSourceInput) config() dataSourceConfig {
	names := make([]string, 0, len(in.Headers))
	for k := range in.Headers {
		names = append(names, k)
	}
	sort.Strings(names)
	return dataSourceConfig{URL: in.URL, RowsPath: in.RowsPath, AuthKind: in.AuthKind, BasicUser: in.BasicUser, HeaderNames: names, Paging: in.Paging,
		PageParam: in.PageParam, SizeParam: in.SizeParam, OffsetParam: in.OffsetParam, PageSize: in.PageSize, StartPage: in.StartPage, NextPath: in.NextPath, AllowHTTP: in.AllowHTTP}
}

// TestRESTSource sends the first request and returns the columns and the
// first rows. Nothing is saved.
func (d *DataService) TestRESTSource(ctx context.Context, in RESTSourceInput) (SourcePreview, error) {
	if err := normalizeREST(&in); err != nil {
		return SourcePreview{}, err
	}
	src, err := data.NewREST(buildRESTConfig(in.config(), in.Headers, in.BearerToken, in.BasicPassword))
	if err != nil {
		return SourcePreview{}, toDataAPIError(err)
	}
	defer src.Close()
	p, err := previewOf(ctx, src, 0, 8)
	if err != nil {
		return p, toDataAPIError(err)
	}
	return p, nil
}

// CreateRESTSource saves a REST source. The token, the basic password and the
// header values go to the secret store.
func (d *DataService) CreateRESTSource(ctx context.Context, in RESTSourceInput) (SourceDTO, error) {
	if err := normalizeREST(&in); err != nil {
		return SourceDTO{}, err
	}
	cfg := in.config()
	probe, err := data.NewREST(buildRESTConfig(cfg, in.Headers, in.BearerToken, in.BasicPassword))
	if err != nil {
		return SourceDTO{}, toDataAPIError(err)
	}
	_ = probe.Close()
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = cfg.URL
	}
	raw, _ := json.Marshal(cfg)
	rec, err := d.b.Store.Sources.Put(ctx, store.SourceRecord{Name: name, Kind: kindREST, Config: raw})
	if err != nil {
		return SourceDTO{}, toDataAPIError(err)
	}
	set := func(what, v string) error {
		if v == "" {
			return d.b.Store.Secrets.Delete(ctx, secretKey(rec.ID, what))
		}
		return d.b.Store.Secrets.Set(ctx, secretKey(rec.ID, what), v)
	}
	hdr := ""
	if len(in.Headers) > 0 {
		b, _ := json.Marshal(in.Headers)
		hdr = string(b)
	}
	token, pass := "", ""
	if in.AuthKind == "bearer" {
		token = in.BearerToken
	}
	if in.AuthKind == "basic" {
		pass = in.BasicPassword
	}
	for _, kv := range [][2]string{{"token", token}, {"basicPass", pass}, {"headers", hdr}} {
		if err := set(kv[0], kv[1]); err != nil {
			_ = d.b.Store.Sources.Delete(ctx, rec.ID)
			return SourceDTO{}, toDataAPIError(err)
		}
	}
	return cfg.dto(rec), nil
}

// CreateSheetsSource is not built: ADR 0016 (Google Sheets scope) is open.
func (d *DataService) CreateSheetsSource(_ context.Context, _ string) (SourceDTO, error) {
	return SourceDTO{}, apiErr("data.auth", "Google Sheets needs setup", "reason", "needsSetup")
}

// ListSources returns every saved source without opening it.
func (d *DataService) ListSources(ctx context.Context) ([]SourceDTO, error) {
	recs, err := d.b.Store.Sources.List(ctx)
	if err != nil {
		return nil, toDataAPIError(err)
	}
	out := make([]SourceDTO, 0, len(recs))
	for _, r := range recs {
		cfg, err := decodeConfig(r)
		if err != nil {
			return nil, toDataAPIError(err)
		}
		out = append(out, cfg.dto(r))
	}
	return out, nil
}

// GetSource opens a source and adds its columns and row count. For a REST
// source it sends the first request and RowCount stays -1.
func (d *DataService) GetSource(ctx context.Context, id string) (SourceDTO, error) {
	rec, err := d.b.Store.Sources.Get(ctx, id)
	if err != nil {
		return SourceDTO{}, toDataAPIError(err)
	}
	cfg, err := decodeConfig(rec)
	if err != nil {
		return SourceDTO{}, toDataAPIError(err)
	}
	out := cfg.dto(rec)
	src, err := openSourceRecord(ctx, d.b.Store, rec)
	if err != nil {
		return out, toDataAPIError(err)
	}
	defer src.Close()
	if out.Columns, err = src.Columns(ctx); err != nil {
		return out, toDataAPIError(err)
	}
	if out.RowCount, err = src.Count(ctx); err != nil {
		return out, toDataAPIError(err)
	}
	return out, nil
}

// DeleteSource removes a source and its secrets. It is refused with
// "conflict" while a run that is not finished uses it.
func (d *DataService) DeleteSource(ctx context.Context, id string) error {
	if _, err := d.b.Store.Sources.Get(ctx, id); err != nil {
		return toDataAPIError(err)
	}
	n, err := activeRunCount(ctx, d.b.Store, "source_id", id)
	if err != nil {
		return toDataAPIError(err)
	}
	if n > 0 {
		return apiErr("conflict", "a run uses this source", "reason", "run", "runs", n)
	}
	if err := d.b.Store.Sources.Delete(ctx, id); err != nil {
		return toDataAPIError(err)
	}
	for _, what := range []string{"token", "basicPass", "headers"} {
		if err := d.b.Store.Secrets.Delete(ctx, secretKey(id, what)); err != nil {
			return toDataAPIError(err)
		}
	}
	return toDataAPIError(d.meta.forgetSource(id))
}

// PreviewSource returns the header and rows [from, from+limit) of a saved source.
func (d *DataService) PreviewSource(ctx context.Context, id string, from, limit int) (SourcePreview, error) {
	src, err := openDataSource(ctx, d.b.Store, id)
	if err != nil {
		return SourcePreview{}, toDataAPIError(err)
	}
	defer src.Close()
	p, err := previewOf(ctx, src, from, limit)
	if err != nil {
		return p, toDataAPIError(err)
	}
	return p, nil
}

func squash(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// mappingOf matches the variables of m to columns, applying the saved overrides.
func mappingOf(m *macro.Macro, columns []string, overrides map[string]string) (*data.MappingReport, data.Mapping) {
	mp := data.Mapping{Overrides: overrides}
	for _, v := range m.Variables {
		mp.Vars = append(mp.Vars, v.Name)
		if v.Required {
			mp.Required = append(mp.Required, v.Name)
		}
	}
	return mp.Resolve(columns), mp
}

func (d *DataService) loadMacro(ctx context.Context, id string) (*macro.Macro, error) {
	rec, err := d.b.Store.Macros.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return macro.Load(bytes.NewReader(rec.Body))
}

// ValidateMapping matches the variables of a macro to the columns of a source,
// with the overrides the user saved. Missing lists variables with no column,
// Extra lists columns no variable reads, Duplicates lists repeated headers.
func (d *DataService) ValidateMapping(ctx context.Context, macroID, sourceID string) (MappingDTO, error) {
	m, err := d.loadMacro(ctx, macroID)
	if err != nil {
		return MappingDTO{}, toDataAPIError(err)
	}
	src, err := openDataSource(ctx, d.b.Store, sourceID)
	if err != nil {
		return MappingDTO{}, toDataAPIError(err)
	}
	defer src.Close()
	cols, err := src.Columns(ctx)
	if err != nil {
		return MappingDTO{}, toDataAPIError(err)
	}
	meta, err := d.meta.get(macroID)
	if err != nil {
		return MappingDTO{}, toDataAPIError(err)
	}
	return buildMapping(m, macroID, sourceID, cols, meta.Mappings[sourceID]), nil
}

func buildMapping(m *macro.Macro, macroID, sourceID string, cols []string, overrides map[string]string) MappingDTO {
	rep, _ := mappingOf(m, cols, overrides)
	out := MappingDTO{MacroID: macroID, SourceID: sourceID, Columns: cols, Variables: []MappingVariable{}, Overrides: map[string]string{}, OK: rep.OK(),
		Missing: append([]string{}, rep.Missing...), Extra: append([]string{}, rep.Extra...), Duplicates: append([]string{}, rep.Duplicates...)}
	for k, v := range overrides {
		out.Overrides[k] = v
	}
	for _, v := range m.Variables {
		mv := MappingVariable{Name: v.Name, Type: string(v.Type), Required: v.Required, Column: rep.Bound[v.Name]}
		if mv.Column == "" {
			for _, c := range cols {
				if squash(c) == squash(v.Name) {
					mv.Suggestion = c
					break
				}
			}
		}
		out.Variables = append(out.Variables, mv)
	}
	return out
}

// SetMapping saves the variable to column overrides of one macro and source
// pair and returns the new mapping. An override to a column the source does
// not have is refused. An empty column removes the override.
func (d *DataService) SetMapping(ctx context.Context, macroID, sourceID string, overrides map[string]string) (MappingDTO, error) {
	m, err := d.loadMacro(ctx, macroID)
	if err != nil {
		return MappingDTO{}, toDataAPIError(err)
	}
	src, err := openDataSource(ctx, d.b.Store, sourceID)
	if err != nil {
		return MappingDTO{}, toDataAPIError(err)
	}
	cols, err := src.Columns(ctx)
	_ = src.Close()
	if err != nil {
		return MappingDTO{}, toDataAPIError(err)
	}
	have := map[string]bool{}
	for _, c := range cols {
		have[c] = true
	}
	declared := map[string]bool{}
	for _, v := range m.Variables {
		declared[v.Name] = true
	}
	clean := map[string]string{}
	var problems []ProblemDTO
	for v, c := range overrides {
		switch {
		case !declared[v]:
			problems = append(problems, ProblemDTO{Path: "overrides." + v, Msg: "no such variable"})
		case c == "":
		case !have[c]:
			problems = append(problems, ProblemDTO{Path: "overrides." + v, Msg: "no such column"})
		default:
			clean[v] = c
		}
	}
	if len(problems) > 0 {
		sort.Slice(problems, func(i, j int) bool { return problems[i].Path < problems[j].Path })
		return MappingDTO{}, apiErr("invalid", "bad mapping", "problems", problems)
	}
	err = d.meta.update(macroID, func(mm *macroMeta) {
		if mm.Mappings == nil {
			mm.Mappings = map[string]map[string]string{}
		}
		if len(clean) == 0 {
			delete(mm.Mappings, sourceID)
		} else {
			mm.Mappings[sourceID] = clean
		}
		mm.SourceID = sourceID
	})
	if err != nil {
		return MappingDTO{}, toDataAPIError(err)
	}
	return buildMapping(m, macroID, sourceID, cols, clean), nil
}

// ValidateRows checks the rows [from, to) of a source against the macro: a
// required variable needs a column and a value, and the file must be well
// formed. to < 0 means to the end. It stops after 500 issues.
func (d *DataService) ValidateRows(ctx context.Context, macroID, sourceID string, from, to int) (ValidationResult, error) {
	m, err := d.loadMacro(ctx, macroID)
	if err != nil {
		return ValidationResult{}, toDataAPIError(err)
	}
	src, err := openDataSource(ctx, d.b.Store, sourceID)
	if err != nil {
		return ValidationResult{}, toDataAPIError(err)
	}
	defer src.Close()
	cols, err := src.Columns(ctx)
	if err != nil {
		return ValidationResult{}, toDataAPIError(err)
	}
	meta, err := d.meta.get(macroID)
	if err != nil {
		return ValidationResult{}, toDataAPIError(err)
	}
	rep, mp := mappingOf(m, cols, meta.Mappings[sourceID])
	rows, err := src.Open(ctx, from, to)
	if err != nil {
		return ValidationResult{}, toDataAPIError(err)
	}
	vr := data.Validated(rows, mp.RequiredValidator(rep))
	defer vr.Close()
	res := ValidationResult{Issues: []RowIssue{}}
	for {
		_, _, ok, err := vr.Next(ctx)
		var re data.RowError
		if errors.As(err, &re) {
			res.Checked++
			if len(res.Issues) >= maxIssues {
				res.Truncated = true
				break
			}
			res.Issues = append(res.Issues, rowErrorIssue(err))
			continue
		}
		if err != nil {
			return res, toDataAPIError(err)
		}
		if !ok {
			break
		}
		res.Checked++
	}
	return res, nil
}

// mappedSource wraps a source so each row also carries the variable names
// the macro reads: a row key is the column name, and the variable name is added
// for every bound variable. The run engine uses it so a user override works.
type mappedSource struct {
	data.Source
	bound map[string]string
}

type mappedRows struct {
	data.Rows
	bound map[string]string
}

func (s mappedSource) Open(ctx context.Context, from, to int) (data.Rows, error) {
	r, err := s.Source.Open(ctx, from, to)
	if err != nil {
		return nil, err
	}
	return mappedRows{Rows: r, bound: s.bound}, nil
}

func (s mappedSource) Columns(ctx context.Context) ([]string, error) {
	cols, err := s.Source.Columns(ctx)
	if err != nil {
		return nil, err
	}
	out := append([]string{}, cols...)
	have := map[string]bool{}
	for _, c := range cols {
		have[c] = true
	}
	for v := range s.bound {
		if !have[v] {
			out = append(out, v)
		}
	}
	sort.Strings(out[len(cols):])
	return out, nil
}

func (r mappedRows) Next(ctx context.Context) (int, data.Row, bool, error) {
	i, row, ok, err := r.Rows.Next(ctx)
	if err != nil || !ok {
		return i, row, ok, err
	}
	out := make(data.Row, len(row)+len(r.bound))
	for k, v := range row {
		out[k] = v
	}
	for v, col := range r.bound {
		out[v] = row[col]
	}
	return i, out, true, nil
}

// openMappedSource opens a saved source with the macro's saved mapping
// applied. The caller closes it.
func openMappedSource(ctx context.Context, b *Backend, macroID, sourceID string) (data.Source, error) {
	rec, err := b.Store.Macros.Get(ctx, macroID)
	if err != nil {
		return nil, err
	}
	m, err := macro.Load(bytes.NewReader(rec.Body))
	if err != nil {
		return nil, err
	}
	src, err := openDataSource(ctx, b.Store, sourceID)
	if err != nil {
		return nil, err
	}
	cols, err := src.Columns(ctx)
	if err != nil {
		_ = src.Close()
		return nil, err
	}
	meta, err := metaOf(b).get(macroID)
	if err != nil {
		_ = src.Close()
		return nil, err
	}
	rep, _ := mappingOf(m, cols, meta.Mappings[sourceID])
	return mappedSource{Source: src, bound: rep.Bound}, nil
}

// mappedRow returns row rowIndex of a source with the mapping applied.
func mappedRow(ctx context.Context, b *Backend, macroID string, _ *macro.Macro, sourceID string, rowIndex int) (map[string]string, error) {
	src, err := openMappedSource(ctx, b, macroID, sourceID)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	rows, err := src.Open(ctx, rowIndex, rowIndex+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	_, row, ok, err := rows.Next(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apiErr("notFound", "no such row", "what", "row")
	}
	return row, nil
}
