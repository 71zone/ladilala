package main

import (
	"context"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/8bu/ladilala/core/driver"
	"github.com/8bu/ladilala/core/macro"
	"github.com/8bu/ladilala/core/profile"
	"github.com/8bu/ladilala/core/recorder"
	"github.com/8bu/ladilala/core/recorder/rectest"
	"github.com/8bu/ladilala/core/store"
)

type eventLog struct {
	mu     sync.Mutex
	states []string
	steps  map[int]macro.Step
	warns  []map[string]any
}

func (l *eventLog) emit(name string, data any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	m, _ := data.(map[string]any)
	switch name {
	case "record.state":
		l.states = append(l.states, m["state"].(string))
	case "record.step":
		if l.steps == nil {
			l.steps = map[int]macro.Step{}
		}
		l.steps[m["stepIndex"].(int)] = m["step"].(macro.Step)
	case "record.warning":
		l.warns = append(l.warns, m)
	}
}

func (l *eventLog) counts() (steps, warns int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.steps), len(l.warns)
}

// TestE2ERecordingService records through MacroService with real Chrome:
// a scripted user types into a field, then clicks a canvas and a file input.
// LADILALA_E2E=1 turns it on.
func TestE2ERecordingService(t *testing.T) {
	if os.Getenv("LADILALA_E2E") == "" {
		t.Skip("LADILALA_E2E not set")
	}
	env := newSvcmEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	in, err := rectest.PickInstall("chrome")
	if err != nil {
		t.Skipf("no Chrome: %v", err)
	}
	version, _ := driver.StockChrome{}.Version(in)
	fp := profile.NewFingerprint(17, profile.Options{Platform: platformOfHost(), BrowserVersion: version})
	prof, err := env.b.Store.Profiles.Put(ctx, store.ProfileRecord{Profile: profile.Profile{Name: "E2E", DataDir: t.TempDir() + "/p", Fingerprint: fp}})
	if err != nil {
		t.Fatal(err)
	}
	site, err := recorder.NewSite()
	if err != nil {
		t.Fatal(err)
	}
	defer site.Close()
	log := &eventLog{}
	env.macros.emit = log.emit

	id, err := env.macros.StartRecording(ctx, StartRecordingInput{ProfileID: prof.ID, StartURL: site.URL("form.html"), Name: "E2E signup"})
	if err != nil {
		t.Skipf("start recording: %v", err)
	}
	env.macros.mu.Lock()
	r := env.macros.recs[id]
	env.macros.mu.Unlock()
	defer func() {
		if _, err := env.macros.takeRecording(id); err == nil {
			_ = env.macros.DiscardRecording(ctx, id)
		}
	}()
	pg, err := r.sess.WaitPage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	u := rectest.NewUser(ctx, r.sess, pg)
	for _, f := range []func() error{
		func() error { u.Wait(800 * time.Millisecond); return nil },
		func() error { return u.ClickAndType("#first", rectest.Recorded["first_name"]) },
		func() error { return u.Click("input[name=last_name]") }, // leaving the field ends the fill
		func() error { return u.Navigate(site.URL("unsupported.html")) },
		func() error { u.Wait(600 * time.Millisecond); return nil },
		func() error { return u.Click("#cv") },
		func() error { return u.Click("#f1") },
	} {
		if err := f(); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if s, w := log.counts(); s >= 3 && w >= 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if s, w := log.counts(); s < 3 || w < 2 {
		log.mu.Lock()
		t.Logf("steps %+v states %v warns %v", log.steps, log.states, log.warns)
		log.mu.Unlock()
		t.Fatalf("live events: %d steps, %d warnings", s, w)
	}
	if err := env.macros.PauseRecording(ctx, id); err != nil {
		t.Fatal(err)
	}
	st, _ := env.macros.GetRecordingStatus(ctx, id)
	if st.State != recPaused {
		t.Errorf("status after pause = %+v", st)
	}
	if err := env.macros.ResumeRecording(ctx, id); err != nil {
		t.Fatal(err)
	}
	if sent := r.sess.SentMethods(); sent["Runtime.enable"] != 0 {
		t.Errorf("Runtime.enable was sent %d times", sent["Runtime.enable"])
	}

	draft, err := env.macros.StopRecording(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Problems) != 0 {
		t.Fatalf("draft problems: %+v", draft.Problems)
	}
	m := draft.Macro
	if err := macro.Validate(&m); err != nil {
		t.Fatalf("StopRecording returned a macro that does not validate: %v", err)
	}
	kinds := map[string]int{}
	for _, w := range draft.Warnings {
		kinds[w.Kind]++
	}
	if kinds["canvas_unsupported"] == 0 || kinds["file_input_unsupported"] == 0 {
		t.Errorf("warnings = %+v, want a canvas and a file input", draft.Warnings)
	}
	var actions []string
	fillID := ""
	for _, s := range m.Steps {
		actions = append(actions, string(s.Action))
		if s.Action == macro.ActionFill && s.Value != nil && s.Value.Expr == rectest.Recorded["first_name"] {
			fillID = s.ID
		}
	}
	if len(actions) != 3 || actions[0] != "navigate" || actions[1] != "fill" || actions[2] != "navigate" || fillID == "" {
		t.Fatalf("recorded actions = %v (fill id %q)", actions, fillID)
	}
	if n, _ := log.counts(); n != len(m.Steps) {
		t.Errorf("live step events = %d, macro has %d steps", n, len(m.Steps))
	}
	log.mu.Lock()
	states := log.states
	log.mu.Unlock()
	if len(states) < 3 || states[0] != recRecording || states[1] != recPaused || states[2] != recRecording {
		t.Errorf("record.state events = %v", states)
	}
	if _, err := env.macros.GetRecordingStatus(ctx, id); apiCode(t, err) != "notFound" {
		t.Errorf("a stopped recording must be gone: %v", err)
	}

	saved, err := env.macros.SaveMacro(ctx, SaveMacroInput{Macro: m, Warnings: &draft.Warnings})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := env.macros.GetMacro(ctx, saved.Macro.ID); len(got.Warnings) != len(draft.Warnings) {
		t.Errorf("the editor must keep the warnings: %+v", got.Warnings)
	}
	mapped, err := env.macros.MapVariable(ctx, saved.Macro.ID, fillID, "first_name")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range mapped.Macro.Steps {
		if s.ID == fillID && s.Value.Expr != "{{first_name}}" {
			t.Errorf("mapped value = %q", s.Value.Expr)
		}
	}
	if _, err := env.macros.StartRecording(ctx, StartRecordingInput{ProfileID: prof.ID, StartURL: "file:///etc/passwd"}); apiCode(t, err) != "invalid" {
		t.Errorf("a non-http start URL must be refused: %v", err)
	}
}

func platformOfHost() string {
	switch runtime.GOOS {
	case "darwin":
		return profile.PlatformMacOS
	case "linux":
		return profile.PlatformLinux
	}
	return profile.PlatformWindows
}
