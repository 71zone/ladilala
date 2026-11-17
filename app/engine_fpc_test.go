package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/8bu/ladilala/core/driver"
)

// fpcFixture serves a tiny fake fingerprint-chromium zip and pins it.
func fpcFixture(t *testing.T, mutate func(*driver.FPCBuild)) (*engineFixture, driver.FPCBuild, *httptest.Server) {
	t.Helper()
	f := newEngineFixture(t)
	bin := "chrome"
	if runtime.GOOS == "windows" {
		bin = "chrome.exe"
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{"ungoogled/" + bin: strings.Repeat("fpc-binary ", 3000), "ungoogled/locales/en-US.pak": "pak"} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	body := buf.Bytes()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "x.zip", time.Time{}, bytes.NewReader(body))
	}))
	t.Cleanup(srv.Close)
	sum := sha256.Sum256(body)
	b := driver.FPCBuild{Tag: "148.0.1.2", URL: srv.URL, Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:]), ChromiumMajor: 148}
	if mutate != nil {
		mutate(&b)
	}
	f.svc.fpcBuilds = []driver.FPCBuild{b}
	return f, b, srv
}

func leftovers(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func TestFPCInstallListAndRemove(t *testing.T) {
	f, b, _ := fpcFixture(t, nil)
	ctx := context.Background()
	job, err := f.svc.InstallEngine(ctx, InstallRequest{EngineID: engineFPC})
	if err != nil {
		t.Fatal(err)
	}
	if job.Version != "" && job.Version != b.Tag {
		t.Errorf("job version %q", job.Version)
	}
	j := f.wait(t, job.ID, jobDone, jobFailed)
	if j.State != jobDone {
		t.Fatalf("job %+v error %+v", j, j.Error)
	}
	list, _ := f.svc.ListEngines(ctx)
	var e EngineDTO
	for _, x := range list {
		if x.ID == engineFPC {
			e = x
		}
	}
	if e.Status != "ready" || e.Active != b.Tag || len(e.Installed) != 1 || e.Latest != "" || e.ChromiumMajor != 148 || len(e.Notes) == 0 {
		t.Fatalf("fpc row %+v", e)
	}
	if in := f.svc.installFor(engineFPC); in == nil || !in.FPC {
		t.Fatalf("installFor = %+v", in)
	}
	// A pin to fpc works once it is installed, and goes away with the engine.
	if err := f.svc.SetProfileEngine(ctx, "p1", engineFPC); err != nil {
		t.Fatal(err)
	}
	// A second install of the same tag is refused.
	again, _ := f.svc.InstallEngine(ctx, InstallRequest{EngineID: engineFPC})
	if j := f.wait(t, again.ID, jobDone, jobFailed); j.State != jobFailed || j.Error == nil || j.Error.Code != "engine.installed" {
		t.Errorf("second install: %+v %+v", j.State, j.Error)
	}
	if err := f.svc.RemoveEngine(ctx, engineFPC, ""); err != nil {
		t.Fatal(err)
	}
	if f.svc.installFor(engineFPC) != nil || len(leftovers(t, filepath.Join(f.dir, "engines", "fingerprint-chromium"))) != 0 {
		t.Errorf("removal left %v", leftovers(t, f.dir))
	}
	if pins, _ := f.svc.GetPins(ctx); pins.Profiles["p1"] != "" {
		t.Errorf("pin survived removal: %+v", pins)
	}
}

func TestFPCHashMismatchInstallsNothing(t *testing.T) {
	f, _, _ := fpcFixture(t, func(b *driver.FPCBuild) { b.SHA256 = strings.Repeat("cd", 32) })
	job, err := f.svc.InstallEngine(context.Background(), InstallRequest{EngineID: engineFPC})
	if err != nil {
		t.Fatal(err)
	}
	j := f.wait(t, job.ID, jobDone, jobFailed)
	if j.State != jobFailed || j.Error == nil || j.Error.Code != "engine.checksum" {
		t.Fatalf("job %+v error %+v", j.State, j.Error)
	}
	if got := leftovers(t, filepath.Join(f.dir, "engines")); len(got) != 0 || f.svc.installFor(engineFPC) != nil {
		t.Errorf("left behind: %v", got)
	}
}

func TestFPCOfflineFails(t *testing.T) {
	f, _, srv := fpcFixture(t, nil)
	srv.Close()
	f.svc.client = &http.Client{Timeout: time.Second}
	job, _ := f.svc.InstallEngine(context.Background(), InstallRequest{EngineID: engineFPC})
	j := f.wait(t, job.ID, jobDone, jobFailed)
	if j.State != jobFailed || j.Error == nil || j.Error.Code != "engine.download" {
		t.Fatalf("job %+v error %+v", j.State, j.Error)
	}
	if got := leftovers(t, filepath.Join(f.dir, "engines")); len(got) != 0 {
		t.Errorf("left behind: %v", got)
	}
}

func TestFPCSelfCheckFailureInstallsNothing(t *testing.T) {
	f, _, _ := fpcFixture(t, nil)
	f.svc.probe = func(context.Context, driver.Install) (string, error) { return "", driver.ErrFPCCanvasBroken }
	job, _ := f.svc.InstallEngine(context.Background(), InstallRequest{EngineID: engineFPC})
	j := f.wait(t, job.ID, jobDone, jobFailed)
	if j.State != jobFailed || j.Error == nil || j.Error.Code != "engine.canvas" {
		t.Fatalf("job %+v error %+v", j.State, j.Error)
	}
	if f.svc.installFor(engineFPC) != nil {
		t.Error("a build that crashes on canvas must not stay installed")
	}
}

func TestFPCRemoveBlockedWhileRunActive(t *testing.T) {
	f, _, _ := fpcFixture(t, nil)
	ctx := context.Background()
	job, _ := f.svc.InstallEngine(ctx, InstallRequest{EngineID: engineFPC})
	if j := f.wait(t, job.ID, jobDone, jobFailed); j.State != jobDone {
		t.Fatalf("install: %+v", j.Error)
	}
	f.svc.activeRuns = func() int { return 1 }
	err := f.svc.RemoveEngine(ctx, engineFPC, "")
	if !isCode(err, "conflict") || f.svc.installFor(engineFPC) == nil {
		t.Fatalf("remove during a run: %v, installed %v", err, f.svc.installFor(engineFPC) != nil)
	}
	f.svc.activeRuns = func() int { return 0 }
	if err := f.svc.RemoveEngine(ctx, engineFPC, ""); err != nil {
		t.Fatal(err)
	}
}

func TestFPCDefaultAndUnavailableOffWindows(t *testing.T) {
	f, _, _ := fpcFixture(t, nil)
	ctx := context.Background()
	if err := f.svc.SetDefaultEngine(ctx, engineFPC); !isCode(err, "engine.missing") {
		t.Errorf("default of a missing fpc: %v", err)
	}
	f.svc.fpcOK = false
	list, _ := f.svc.ListEngines(ctx)
	for _, e := range list {
		if e.ID == engineFPC && e.Status != "unavailable" {
			t.Errorf("fpc off Windows: %+v", e)
		}
	}
	if _, err := f.svc.InstallEngine(ctx, InstallRequest{EngineID: engineFPC}); !isCode(err, "engine.platform") {
		t.Errorf("install off Windows: %v", err)
	}
}
