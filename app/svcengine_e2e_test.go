package main

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestE2EEngineService runs the real detection, doctor probe and test launch
// against the browser installed on this machine. It needs LADILALA_E2E=1.
func TestE2EEngineService(t *testing.T) {
	if os.Getenv("LADILALA_E2E") == "" {
		t.Skip("LADILALA_E2E not set")
	}
	dir := t.TempDir()
	cfg, err := openSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := newEngineService(&Backend{DataDir: dir, Settings: cfg})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	list, err := s.ListEngines(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var stock *EngineDTO
	for i := range list {
		t.Logf("engine %s tier %d status %s version %q path %s default %v", list[i].ID, list[i].Tier, list[i].Status, list[i].Active, list[i].Path, list[i].Default)
		if list[i].Tier == 1 && list[i].Status == "ready" && stock == nil {
			stock = &list[i]
		}
	}
	if stock == nil {
		t.Skip("no Chrome or Edge on this machine")
	}
	if stock.Active == "" || stock.Path == "" {
		t.Fatalf("stock engine without version or path: %+v", stock)
	}

	ds, err := s.RunDiagnostics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ds {
		t.Logf("doctor %s %s %s %v", d.ID, d.Level, d.Code, d.Params)
		if d.Level == "fail" {
			t.Errorf("doctor check failed: %+v", d)
		}
	}
	steps, err := s.TestLaunch(ctx, stock.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range steps {
		t.Logf("test launch %s %s %s %v", st.ID, st.State, st.Code, st.Params)
	}
	for _, st := range steps[:3] {
		if st.State != "ok" {
			t.Errorf("step %+v", st)
		}
	}
	rep, _ := s.DiagnosticsReport(ctx)
	if rep == "" {
		t.Error("empty report")
	}
}
