package data

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
)

// NestedMode says what to do with a JSON value that is an object or an array.
type NestedMode int

const (
	// NestedReject gives a NestedValueError for the row. This is the default.
	NestedReject NestedMode = iota
	// NestedJSON turns the value into compact JSON text.
	NestedJSON
)

// MaxJSONFileSize is the largest JSON file that is read into memory.
const MaxJSONFileSize = 256 << 20

type kv struct {
	key string
	val any
}

// parseObject reads one JSON object and keeps the key order.
func parseObject(raw []byte) ([]kv, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errNotObject
	}
	var out []kv
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := kt.(string)
		var v any
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		out = append(out, kv{key, v})
	}
	return out, nil
}

var errNotObject = errors.New("not an object")

// flattenValue turns one JSON value into cell text.
func flattenValue(v any, mode NestedMode) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "", true
	case string:
		return x, true
	case json.Number:
		return x.String(), true
	case bool:
		return strconv.FormatBool(x), true
	}
	if mode == NestedJSON {
		b, err := json.Marshal(v)
		if err != nil {
			return "", false
		}
		return string(b), true
	}
	return "", false
}

// objectToRow converts a parsed object to a Row and records new column names.
func objectToRow(index int, obj []kv, mode NestedMode, cols *columnSet) (Row, error) {
	row := make(Row, len(obj))
	var rowErr error
	for _, p := range obj {
		cols.add(p.key)
		text, ok := flattenValue(p.val, mode)
		if !ok {
			if rowErr == nil {
				rowErr = &NestedValueError{Index: index, Column: p.key}
			}
			continue
		}
		if _, dup := row[p.key]; !dup {
			row[p.key] = text
		}
	}
	return row, rowErr
}

// columnSet keeps column names in order of first appearance.
type columnSet struct {
	order []string
	seen  map[string]bool
}

func (c *columnSet) add(name string) {
	if c.seen == nil {
		c.seen = map[string]bool{}
	}
	if !c.seen[name] {
		c.seen[name] = true
		c.order = append(c.order, name)
	}
}

// JSONOptions tunes the JSON reader.
type JSONOptions struct {
	Nested NestedMode
}

// JSONSource reads a JSON file: an array of flat objects, or {"rows": [...]}.
// The file is read into memory, up to MaxJSONFileSize.
type JSONSource struct {
	path string
	opts JSONOptions

	once    sync.Once
	loadErr error
	cols    []string
	rows    []Row
	rowErrs map[int]error
	closed  bool
	mu      sync.Mutex
}

// NewJSON prepares a JSON source. The file is read on first use.
func NewJSON(path string, opts JSONOptions) *JSONSource {
	return &JSONSource{path: path, opts: opts}
}

func (s *JSONSource) load() error {
	s.once.Do(func() { s.loadErr = s.read() })
	return s.loadErr
}

func (s *JSONSource) read() error {
	st, err := os.Stat(s.path)
	if err != nil {
		return fmt.Errorf("cannot open file: %w", err)
	}
	if st.Size() > MaxJSONFileSize {
		return fmt.Errorf("JSON file is %d MB; the limit is %d MB", st.Size()>>20, MaxJSONFileSize>>20)
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return fmt.Errorf("cannot read file: %w", err)
	}
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	if len(bytes.TrimSpace(b)) == 0 {
		return ErrEmpty
	}
	items, err := splitRows(b)
	if err != nil {
		return err
	}
	var cols columnSet
	s.rowErrs = map[int]error{}
	for i, raw := range items {
		obj, err := parseObject(raw)
		if err != nil {
			if errors.Is(err, errNotObject) {
				s.rowErrs[i] = &NotObjectError{Index: i}
				s.rows = append(s.rows, nil)
				continue
			}
			return fmt.Errorf("JSON format error in row %d: %v", i+1, err)
		}
		row, rerr := objectToRow(i, obj, s.opts.Nested, &cols)
		if rerr != nil {
			s.rowErrs[i] = rerr
		}
		s.rows = append(s.rows, row)
	}
	s.cols = cols.order
	// Rows miss the keys they do not have. Fill them so every row has every column.
	for _, r := range s.rows {
		for _, c := range s.cols {
			if r != nil {
				if _, ok := r[c]; !ok {
					r[c] = ""
				}
			}
		}
	}
	return nil
}

// splitRows finds the array of rows in a JSON document.
func splitRows(b []byte) ([]json.RawMessage, error) {
	var arr []json.RawMessage
	trim := bytes.TrimSpace(b)
	if trim[0] == '[' {
		if err := json.Unmarshal(trim, &arr); err != nil {
			return nil, fmt.Errorf("JSON format error: %v", err)
		}
		return arr, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trim, &obj); err != nil {
		return nil, fmt.Errorf("JSON format error: %v", err)
	}
	raw, ok := obj["rows"]
	if !ok {
		return nil, errors.New(`JSON must be an array of objects or an object with a "rows" array`)
	}
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf(`JSON "rows" must be an array: %v`, err)
	}
	return arr, nil
}

func (s *JSONSource) checkOpen() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	return nil
}

// Columns returns the union of all object keys in order of first appearance.
func (s *JSONSource) Columns(ctx context.Context) ([]string, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return append([]string(nil), s.cols...), nil
}

// Count returns the number of rows.
func (s *JSONSource) Count(ctx context.Context) (int, error) {
	if err := s.checkOpen(); err != nil {
		return 0, err
	}
	if err := s.load(); err != nil {
		return 0, err
	}
	return len(s.rows), nil
}

type jsonIter struct {
	s *JSONSource
	i int
}

func (it *jsonIter) next(ctx context.Context) (int, Row, bool, error) {
	if it.i >= len(it.s.rows) {
		return 0, nil, false, nil
	}
	idx := it.i
	it.i++
	if err, bad := it.s.rowErrs[idx]; bad {
		return idx, nil, false, err
	}
	return idx, cloneRow(it.s.rows[idx]), true, nil
}

func (it *jsonIter) close() error { return nil }

func cloneRow(r Row) Row {
	c := make(Row, len(r))
	for k, v := range r {
		c[k] = v
	}
	return c
}

// Open returns a cursor over rows [from,to).
func (s *JSONSource) Open(ctx context.Context, from, to int) (Rows, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return newRange(&jsonIter{s: s}, from, to), nil
}

// Close marks the source closed.
func (s *JSONSource) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}
