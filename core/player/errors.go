package player

import (
	"errors"
	"fmt"
	"strings"

	"github.com/8bu/ladilala/core/macro"
)

// Sentinels for errors.Is.
var (
	ErrNotFound      = errors.New("element not found")
	ErrAmbiguous     = errors.New("locator matches more than one element")
	ErrObscured      = errors.New("another element covers the click point")
	ErrOrigin        = errors.New("origin is not allowed by the macro")
	ErrMissingColumn = errors.New("no value for variable")
	ErrUnsupported   = errors.New("step is not supported by the player")
	ErrBadValue      = errors.New("value is not valid for the field")
)

// Tried is how many elements one locator matched.
type Tried struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// StepError wraps what went wrong in one step.
type StepError struct {
	StepID string
	Action macro.Action
	Err    error
}

func (e *StepError) Error() string { return fmt.Sprintf("step %s (%s): %v", e.StepID, e.Action, e.Err) }
func (e *StepError) Unwrap() error { return e.Err }

// LocatorError says why a target did not resolve to one clickable element.
// Reason is ErrNotFound, ErrAmbiguous or ErrObscured.
type LocatorError struct {
	Reason error
	Tried  []Tried
	Count  int    // matches of the first ambiguous locator
	Detail string // shadow host or cover element
}

func (e *LocatorError) Error() string {
	var b strings.Builder
	b.WriteString(e.Reason.Error())
	if e.Count > 0 {
		fmt.Fprintf(&b, " (%d matches)", e.Count)
	}
	if e.Detail != "" {
		fmt.Fprintf(&b, " [%s]", e.Detail)
	}
	if len(e.Tried) > 0 {
		b.WriteString("; tried")
		for _, t := range e.Tried {
			fmt.Fprintf(&b, " %s=%d", t.Kind, t.Count)
		}
	}
	return b.String()
}
func (e *LocatorError) Unwrap() error { return e.Reason }

// MissingColumnError says the data row has no value for a variable the macro
// needs. Step is empty when the variable is required by its declaration.
type MissingColumnError struct {
	Column string
	Step   string
}

func (e *MissingColumnError) Error() string {
	if e.Step == "" {
		return fmt.Sprintf("no value for required variable %q in the data row", e.Column)
	}
	return fmt.Sprintf("step %s needs variable %q but the data row has no value", e.Step, e.Column)
}
func (e *MissingColumnError) Unwrap() error { return ErrMissingColumn }

// OriginError is a refusal to navigate to or act on a page outside the
// macro's allowedOrigins.
type OriginError struct {
	URL  string
	Step string
}

func (e *OriginError) Error() string {
	return fmt.Sprintf("step %s: %s is outside allowedOrigins", e.Step, e.URL)
}
func (e *OriginError) Unwrap() error { return ErrOrigin }

// BadValueError says a value cannot go into a field: a date input that is not
// given as ISO text, or one the field's min or max refuses.
type BadValueError struct {
	Step, Field, Type, Value string
	Want                     string // the format required, for example YYYY-MM-DD
	Reason                   string // empty for a format error
}

func (e *BadValueError) Error() string {
	msg := fmt.Sprintf("step %s: field %q (type=%s): value %q is not valid", e.Step, e.Field, e.Type, e.Value)
	if e.Reason != "" {
		msg += ": " + e.Reason
	} else {
		msg += "; expected " + e.Want
	}
	return msg
}
func (e *BadValueError) Unwrap() error { return ErrBadValue }
