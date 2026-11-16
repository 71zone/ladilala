package macro

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// maxDocumentSize bounds Load so a corrupt or hostile file cannot exhaust memory.
const maxDocumentSize = 16 << 20

// Migrate upgrades a raw macro document to CurrentVersion and returns the
// upgraded bytes. Only version 1 exists, so it validates the version and
// returns raw unchanged; future versions add one case per step here, each
// rewriting the document from version N to N+1.
func Migrate(raw []byte) ([]byte, error) {
	var head struct {
		SchemaVersion *int `json:"schemaVersion"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, fmt.Errorf("macro: reading schemaVersion: %w", err)
	}
	if head.SchemaVersion == nil {
		return nil, errors.New("macro: schemaVersion is missing")
	}
	switch v := *head.SchemaVersion; {
	case v == CurrentVersion:
		return raw, nil
	case v > CurrentVersion:
		return nil, fmt.Errorf("macro: schemaVersion %d is newer than supported version %d", v, CurrentVersion)
	default:
		return nil, fmt.Errorf("macro: unsupported schemaVersion %d", v)
	}
}

// Load reads one macro document: migrates it to the current schema, decodes
// it rejecting unknown fields (Migrate rejects trailing data), and validates it. Decode and
// migration failures are plain errors; semantic problems come back as
// *ValidationError.
func Load(r io.Reader) (*Macro, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxDocumentSize+1))
	if err != nil {
		return nil, fmt.Errorf("macro: read: %w", err)
	}
	if len(raw) > maxDocumentSize {
		return nil, fmt.Errorf("macro: document exceeds %d bytes", maxDocumentSize)
	}
	raw, err = Migrate(raw)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var m Macro
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("macro: decode: %w", err)
	}
	if err := Validate(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Marshal validates m and encodes it as indented JSON.
func Marshal(m *Macro) ([]byte, error) {
	if err := Validate(m); err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("macro: encode: %w", err)
	}
	return append(b, '\n'), nil
}
