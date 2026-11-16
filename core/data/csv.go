package data

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// CSVOptions tunes the CSV reader. The zero value detects everything.
type CSVOptions struct {
	Encoding  string // one of the Enc* names; empty means detect
	Delimiter rune   // 0 means detect
}

// CSVSource streams a CSV file. It never loads the whole file.
type CSVSource struct {
	path      string
	encoding  string
	delimiter rune

	mu      sync.Mutex
	header  []string
	count   int
	counted bool
	closed  bool
}

// NewCSV opens a CSV file and detects its encoding and delimiter.
func NewCSV(path string, opts CSVOptions) (*CSVSource, error) {
	enc, delim, err := Sniff(path)
	if err != nil {
		return nil, err
	}
	if opts.Encoding != "" {
		enc = opts.Encoding
	}
	if opts.Delimiter != 0 {
		delim = opts.Delimiter
	}
	return &CSVSource{path: path, encoding: enc, delimiter: delim}, nil
}

// Encoding reports the encoding in use.
func (s *CSVSource) Encoding() string { return s.encoding }

// Delimiter reports the delimiter in use.
func (s *CSVSource) Delimiter() rune { return s.delimiter }

type csvIter struct {
	f      *os.File
	r      *csv.Reader
	header []string
	index  int
	skipHd bool
}

func (s *CSVSource) openIter() (*csvIter, error) {
	f, err := os.Open(s.path)
	if err != nil {
		return nil, fmt.Errorf("cannot open file: %w", err)
	}
	dec, err := decodeReader(f, s.encoding)
	if err != nil {
		f.Close()
		return nil, err
	}
	r := csv.NewReader(dec)
	r.Comma = s.delimiter
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	r.ReuseRecord = true
	return &csvIter{f: f, r: r}, nil
}

func (it *csvIter) readHeader() ([]string, error) {
	rec, err := it.r.Read()
	if errors.Is(err, io.EOF) {
		return nil, ErrEmpty
	}
	if err != nil {
		return nil, wrapCSVErr(err)
	}
	h := make([]string, len(rec))
	for i, c := range rec {
		h[i] = strings.TrimSpace(c)
	}
	return h, nil
}

func wrapCSVErr(err error) error {
	var pe *csv.ParseError
	if errors.As(err, &pe) {
		return fmt.Errorf("CSV format error at line %d: %v", pe.Line, pe.Err)
	}
	return fmt.Errorf("cannot read CSV: %w", err)
}

func (it *csvIter) next(ctx context.Context) (int, Row, bool, error) {
	rec, err := it.r.Read()
	if errors.Is(err, io.EOF) {
		return 0, nil, false, nil
	}
	if err != nil {
		return 0, nil, false, wrapCSVErr(err)
	}
	idx := it.index
	it.index++
	if len(rec) != len(it.header) {
		return idx, nil, false, &RaggedRowError{Index: idx, Want: len(it.header), Got: len(rec)}
	}
	return idx, buildRow(it.header, rec), true, nil
}

func (it *csvIter) close() error { return it.f.Close() }

func (s *CSVSource) checkOpen() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	return nil
}

// Columns returns the header names.
func (s *CSVSource) Columns(ctx context.Context) ([]string, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.header != nil {
		return append([]string(nil), s.header...), nil
	}
	it, err := s.openIter()
	if err != nil {
		return nil, err
	}
	defer it.close()
	h, err := it.readHeader()
	if err != nil {
		return nil, err
	}
	s.header = h
	return append([]string(nil), h...), nil
}

// Count counts data rows with one streaming pass. Ragged rows are counted.
func (s *CSVSource) Count(ctx context.Context) (int, error) {
	if err := s.checkOpen(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	if s.counted {
		defer s.mu.Unlock()
		return s.count, nil
	}
	s.mu.Unlock()
	it, err := s.openIter()
	if err != nil {
		return 0, err
	}
	defer it.close()
	if _, err := it.readHeader(); err != nil {
		return 0, err
	}
	n := 0
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		_, err := it.r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, wrapCSVErr(err)
		}
		n++
	}
	s.mu.Lock()
	s.count, s.counted = n, true
	s.mu.Unlock()
	return n, nil
}

// Open returns a cursor over rows [from,to).
func (s *CSVSource) Open(ctx context.Context, from, to int) (Rows, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	it, err := s.openIter()
	if err != nil {
		return nil, err
	}
	h, err := it.readHeader()
	if err != nil {
		it.close()
		return nil, err
	}
	it.header = h
	return newRange(it, from, to), nil
}

// Close marks the source closed.
func (s *CSVSource) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}
