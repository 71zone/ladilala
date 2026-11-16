// Package macro defines the versioned macro document: its types, JSON
// (de)serialisation, validation and value-expression language. It has no
// dependency on the browser runtime, the engine or profile data, so editors,
// the recorder and the player can all share it.
package macro

import (
	"encoding/json"
	"fmt"
	"time"
)

// CurrentVersion is the only schema version the package understands natively.
// Older documents are upgraded by Migrate before decoding.
const CurrentVersion = 1

// Macro is one recorded/authored automation.
type Macro struct {
	SchemaVersion int    `json:"schemaVersion"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	// AllowedOrigins bounds where the macro may navigate or act; entries are
	// bare origins ("https://example.com") or "https://*.example.com".
	AllowedOrigins []string       `json:"allowedOrigins"`
	Variables      []VariableDecl `json:"variables,omitempty"`
	Steps          []Step         `json:"steps"`
	Guards         []Guard        `json:"guards,omitempty"`
}

// VarType is the declared type of a variable.
type VarType string

const (
	VarString VarType = "string"
	VarNumber VarType = "number"
	VarBool   VarType = "bool"
	VarDate   VarType = "date" // ISO-8601 "2006-01-02" or RFC 3339
)

// VarKind is the kind of page field a variable was recorded from. It does not
// change how a run works; the Excel template guide and the editor show it.
type VarKind string

const (
	KindText     VarKind = "text"
	KindEmail    VarKind = "email"
	KindPhone    VarKind = "phone"
	KindNumber   VarKind = "number"
	KindURL      VarKind = "url"
	KindDate     VarKind = "date"
	KindPassword VarKind = "password"
)

// VariableDecl declares an input a run can supply for {{name}} references.
type VariableDecl struct {
	Name     string  `json:"name"`
	Type     VarType `json:"type"`
	Required bool    `json:"required,omitempty"`
	// Kind is the page field kind the recorder saw (empty when unknown).
	Kind VarKind `json:"kind,omitempty"`
	// Default is a JSON value matching Type (string/number/bool/date string).
	Default any `json:"default,omitempty"`
}

// Action is the kind of operation a step performs.
type Action string

const (
	ActionNavigate Action = "navigate"
	ActionClick    Action = "click"
	// ActionDblClick is a real double click: mousedown, mouseup, click twice
	// (click count 1 then 2), then dblclick, as the browser itself produces.
	ActionDblClick   Action = "dblclick"
	ActionFill       Action = "fill"
	ActionSelect     Action = "select"
	ActionPress      Action = "press"
	ActionCheck      Action = "check"
	ActionUploadFile Action = "uploadFile"
	ActionWait       Action = "wait"
	ActionAssert     Action = "assert"
	ActionScroll     Action = "scroll"
	// ActionManual hands control to the human (captcha, 2FA, review) and
	// resumes when they confirm; Value carries the instruction shown to them.
	ActionManual Action = "manual"
)

// OnFail says what the player does once a step has exhausted its retries.
type OnFail string

const (
	OnFailStop  OnFail = "stop"
	OnFailSkip  OnFail = "skip"
	OnFailPause OnFail = "pause"
)

// Step is one unit of a macro.
type Step struct {
	ID     string  `json:"id"`
	Action Action  `json:"action"`
	Target *Target `json:"target,omitempty"`
	// Value meaning by action: navigate=URL, fill/select=text/option,
	// press=key chord, check="true"/"false" (default true), uploadFile=file
	// path, scroll="top"|"bottom"|pixel delta, manual=instruction text.
	Value *Value `json:"value,omitempty"`
	// CommitPoint marks an irreversible side effect (submit, pay, send); the
	// player may require confirmation or skip it in dry runs. Only click,
	// press and fill steps can be commit points.
	CommitPoint bool          `json:"commitPoint,omitempty"`
	Expect      []Expectation `json:"expect,omitempty"`
	Timeout     Duration      `json:"timeout,omitempty"`
	Retries     int           `json:"retries,omitempty"`
	OnFail      OnFail        `json:"onFail,omitempty"`
	// Disabled keeps the step in the macro but the player skips it, so a user
	// can switch a step off without deleting it.
	Disabled bool `json:"disabled,omitempty"`
}

// LocatorKind selects how a Locator finds an element.
type LocatorKind string

const (
	LocRole   LocatorKind = "role"   // Value=ARIA role, Name=accessible name
	LocLabel  LocatorKind = "label"  // form control by label text
	LocText   LocatorKind = "text"   // element by visible text
	LocCSS    LocatorKind = "css"    // CSS selector
	LocXPath  LocatorKind = "xpath"  // XPath expression
	LocTestID LocatorKind = "testid" // data-testid attribute
)

// Locator is one strategy for finding an element.
type Locator struct {
	Kind  LocatorKind `json:"kind"`
	Value string      `json:"value"`
	// Name is the accessible name; only meaningful for Kind==role.
	Name string `json:"name,omitempty"`
}

// Target is an element reference. Locators are tried in order; the first that
// resolves to exactly one element wins, so put the most stable one first.
type Target struct {
	Locators []Locator `json:"locators"`
	// FramePath lists CSS selectors of nested iframes, outermost first.
	FramePath []string `json:"framePath,omitempty"`
	// ShadowPath lists CSS selectors of nested shadow hosts, outermost first,
	// that must be pierced before the locators are applied.
	ShadowPath []string `json:"shadowPath,omitempty"`
}

// Value is a literal or a template referencing variables ({{name}}), with
// optional transforms applied to the final string.
type Value struct {
	Expr       string   `json:"expr"`
	Transforms []string `json:"transforms,omitempty"`
}

// ExpectKind is the condition an Expectation checks after a step.
type ExpectKind string

const (
	ExpectURLMatches     ExpectKind = "urlMatches"     // Value is a regexp
	ExpectElementPresent ExpectKind = "elementPresent" // Target
	ExpectElementAbsent  ExpectKind = "elementAbsent"  // Target
	ExpectTextContains   ExpectKind = "textContains"   // Target + Value
	ExpectTextEquals     ExpectKind = "textEquals"     // Target + Value
	ExpectValueEquals    ExpectKind = "valueEquals"    // Target + Value (form control value)
)

// Expectation is a post-condition checked after a step (and all of an assert
// step's reason to exist).
type Expectation struct {
	Kind   ExpectKind `json:"kind"`
	Target *Target    `json:"target,omitempty"`
	Value  *Value     `json:"value,omitempty"`
}

// GuardKind is what a Guard watches for.
type GuardKind string

const (
	GuardURLMatches     GuardKind = "urlMatches"
	GuardElementPresent GuardKind = "elementPresent"
	GuardElementAbsent  GuardKind = "elementAbsent"
)

// GuardAction is the player's reaction when a guard fires.
type GuardAction string

const (
	GuardNeedsLogin GuardAction = "needsLogin" // session expired: ask the user to sign in
	GuardPause      GuardAction = "pause"
	GuardFail       GuardAction = "fail"
)

// Guard detects session expiry or unexpected pages and is evaluated between
// steps. It fires when its condition holds: urlMatches when the URL matches
// Pattern, elementPresent when Locator is found, elementAbsent when it is not.
type Guard struct {
	Kind    GuardKind   `json:"kind"`
	Pattern string      `json:"pattern,omitempty"` // regexp, urlMatches only
	Locator *Locator    `json:"locator,omitempty"` // elementPresent/Absent only
	Action  GuardAction `json:"action"`
}

// Duration is a time.Duration serialised as a Go duration string ("2.5s").
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string like \"5s\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}
