package main

import (
	"encoding/json"
	"errors"
)

// APIError is the error every UI service returns. Wails sends err.Error() to
// the UI, so the text is JSON: {"code":"notFound","message":"...","details":{...}}.
// The UI translates the code. Codes are listed in docs/api-contract.md.
type APIError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message,omitempty"` // English text for logs only
	Details   map[string]any `json:"details,omitempty"`
	Retryable bool           `json:"retryable,omitempty"`
}

func (e *APIError) Error() string {
	b, _ := json.Marshal(e)
	return string(b)
}

// apiErr builds an APIError. details is an optional flat key/value list.
func apiErr(code, message string, details ...any) error {
	e := &APIError{Code: code, Message: message}
	if len(details) > 1 {
		e.Details = map[string]any{}
		for i := 0; i+1 < len(details); i += 2 {
			if k, ok := details[i].(string); ok {
				e.Details[k] = details[i+1]
			}
		}
	}
	return e
}

// asAPIError returns err unchanged when it is already an APIError, otherwise
// wraps it as code "internal".
func asAPIError(err error) error {
	if err == nil {
		return nil
	}
	var a *APIError
	if errors.As(err, &a) {
		return err
	}
	return &APIError{Code: "internal", Message: err.Error()}
}
