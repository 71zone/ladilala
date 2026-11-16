package recorder

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/8bu/ladilala/core/driver"
	"github.com/8bu/ladilala/core/macro"
)

//go:embed placeholder.js
var placeholderJS string

// Column is a data column the user made by typing {{name}} in a page field.
type Column struct {
	Name string
	// Kind is the field kind: text, email, phone, number, url, date or password.
	Kind macro.VarKind
	// Protected is true for a password field: the sample value is never shown or stored.
	Protected bool
}

// colState is a Column with its test token. The token is the value the page
// holds while recording; it lives in the overlay and here, never in the macro.
type colState struct {
	Column
	token string
}

type rawPart struct {
	L string `json:"l,omitempty"`
	V string `json:"v,omitempty"`
}

var colName = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,39}$`)

func validKind(k macro.VarKind) bool {
	switch k {
	case macro.KindText, macro.KindEmail, macro.KindPhone, macro.KindNumber, macro.KindURL, macro.KindDate, macro.KindPassword:
		return true
	}
	return false
}

// SetOptions sets the overlay language ("vi" or "en") and whether it shows the
// first-use hint card. Call it before the browser starts.
func (r *Recorder) SetOptions(lang string, hint bool) {
	if lang != "vi" {
		lang = "en"
	}
	r.mu.Lock()
	r.lang, r.hint = lang, hint
	r.mu.Unlock()
}

// Columns returns the data columns made so far, in the order they were made.
func (r *Recorder) Columns() []Column {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Column, 0, len(r.cols))
	for _, c := range r.cols {
		out = append(out, c.Column)
	}
	return out
}

// HintState reports how the user closed the hint card: closed is true after
// the X, a first placeholder or "never show again"; never is true after the last.
func (r *Recorder) HintState() (closed, never bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hintClosed, r.hintNever
}

// useCount counts the steps that read each column. The caller holds r.mu.
func (r *Recorder) useCount() map[string]int {
	n := map[string]int{}
	for _, s := range r.steps {
		for _, name := range stepRefs(s) {
			n[name]++
		}
	}
	return n
}

func stepRefs(s macro.Step) []string {
	if s.Value == nil {
		return nil
	}
	parts, err := macro.ParseExpr(s.Value.Expr)
	if err != nil {
		return nil
	}
	return macro.Refs(parts)
}

type syncCol struct {
	Name      string        `json:"name"`
	Kind      macro.VarKind `json:"kind"`
	Token     string        `json:"token"`
	Protected bool          `json:"protected"`
}

// syncJS is the call that gives an overlay frame the language, the hint state,
// the columns with their tokens and how many steps use each. The caller holds r.mu.
func (r *Recorder) syncJS() string {
	cols := make([]syncCol, 0, len(r.cols))
	for _, c := range r.cols {
		cols = append(cols, syncCol{c.Name, c.Kind, c.token, c.Protected})
	}
	raw, _ := json.Marshal(map[string]any{
		"lang": r.lang, "hint": r.hint && !r.hintClosed, "vars": cols, "counts": r.useCount(), "dead": append([]string{}, r.dead...),
	})
	return "__ladiRec.sync(" + string(raw) + ")"
}

// pushSync tells every overlay frame the current columns and counts when they
// changed since the last push.
func (r *Recorder) pushSync() {
	r.mu.Lock()
	js := r.syncJS()
	same := js == r.lastSync
	r.lastSync = js
	r.mu.Unlock()
	if !same {
		r.broadcast(js, "")
	}
}

func (r *Recorder) col(name string) int {
	for i, c := range r.cols {
		if c.Name == name {
			return i
		}
	}
	return -1
}

// onVar adds a column the overlay made. A second add of the same name, from
// another frame, is ignored: one column has one token.
func (r *Recorder) onVar(ev rawEvent) {
	k := macro.VarKind(ev.Kind)
	if !colName.MatchString(ev.Var) || !validKind(k) || ev.Token == "" {
		r.mu.Lock()
		r.stats.Gaps["bad-column"]++
		r.mu.Unlock()
		return
	}
	r.mu.Lock()
	if r.stopped || r.col(ev.Var) >= 0 {
		r.mu.Unlock()
		r.pushSync()
		return
	}
	r.cols = append(r.cols, colState{Column: Column{Name: ev.Var, Kind: k, Protected: ev.Protected || k == macro.KindPassword}, token: ev.Token})
	r.hintClosed = true
	r.mu.Unlock()
	r.pushSync()
}

// onVarOp renames or deletes a column from the overlay panel. Delete also
// removes the steps that use it: a fixed typed text cannot replace the column.
func (r *Recorder) onVarOp(ev rawEvent) {
	r.mu.Lock()
	i := r.col(ev.Var)
	switch {
	case i < 0 || r.stopped:
	case ev.Op == "rename" && colName.MatchString(ev.To) && r.col(ev.To) < 0:
		r.cols[i].Name = ev.To
		from, to := "{{"+ev.Var+"}}", "{{"+ev.To+"}}"
		for j, s := range r.steps {
			if s.Value != nil {
				v := *s.Value
				v.Expr = strings.ReplaceAll(v.Expr, from, to)
				r.steps[j].Value = &v
			}
		}
	case ev.Op == "delete":
		r.dead = append(r.dead, r.cols[i].token)
		r.cols = append(r.cols[:i], r.cols[i+1:]...)
		r.dropSteps(ev.Var)
	}
	r.mu.Unlock()
	r.pushSync()
}

// dropSteps removes every step that reads column name and renumbers the step
// IDs. The caller holds r.mu.
func (r *Recorder) dropSteps(name string) {
	kept := r.steps[:0:0]
	for _, s := range r.steps {
		uses := false
		for _, ref := range stepRefs(s) {
			uses = uses || ref == name
		}
		if !uses {
			kept = append(kept, s)
			continue
		}
		for j := range r.warnings {
			if r.warnings[j].StepIndex > len(kept) {
				r.warnings[j].StepIndex--
			}
		}
	}
	for j := range kept {
		kept[j].ID = fmt.Sprintf("s%03d-%s", j+1, kept[j].Action)
	}
	r.steps = kept
}

// partsExpr turns the parts of a field value into a step expression. Literal
// text is escaped; a column part must be a known column.
func (r *Recorder) partsExpr(parts []rawPart) (string, bool) {
	expr := ""
	for _, p := range parts {
		if p.V != "" {
			if r.col(p.V) < 0 {
				return "", false
			}
			expr += "{{" + p.V + "}}"
		} else {
			expr += macro.EscapeLiteral(p.L)
		}
	}
	return expr, true
}

// OverlayPoint returns the viewport center of an overlay control: "cols" (the
// Data columns button), "stop", "rename:<name>", "delete:<name>", "confirm",
// "hint-chip", "hint-close" or "hint-never". Tests use it to aim real mouse
// events at the overlay; it reads the page and changes nothing.
func (r *Recorder) OverlayPoint(ctx context.Context, pg *driver.Page, what string) (x, y float64, err error) {
	r.mu.Lock()
	var ids []int
	for k, id := range r.contexts {
		if k.session == pg.SessionID {
			ids = append(ids, id)
		}
	}
	r.mu.Unlock()
	q, _ := json.Marshal(what)
	for _, id := range ids {
		cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		var res struct {
			Result struct {
				Value []float64 `json:"value"`
			} `json:"result"`
		}
		err = pg.Call(cctx, "Runtime.evaluate", map[string]any{
			"contextId": id, "returnByValue": true, "expression": "typeof __ladiRec==='object'?__ladiRec.rect(" + string(q) + "):null",
		}, &res)
		cancel()
		if err == nil && len(res.Result.Value) == 2 {
			return res.Result.Value[0], res.Result.Value[1], nil
		}
	}
	return 0, 0, fmt.Errorf("recorder: overlay control %q not found", what)
}
