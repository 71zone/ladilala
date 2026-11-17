package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/8bu/ladilala/core/driver"
)

// engineFixture builds an EngineService on a temp data folder with fake
// detection, a fake probe and a fake version list that points at srv.
type engineFixture struct {
	svc    *EngineService
	dir    string
	events []JobDTO
	mu     sync.Mutex
}

func newEngineFixture(t *testing.T) *engineFixture {
	t.Helper()
	dir := t.TempDir()
	cfg, err := openSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	f := &engineFixture{dir: dir}
	s := newEngineService(&Backend{DataDir: dir, Settings: cfg})
	s.fpcOK = true
	s.platform = driver.CfTPlatform(runtime.GOOS, runtime.GOARCH)
	if s.platform == "" {
		s.platform = "win64"
	}
	s.detect = func() ([]driver.Install, error) { return nil, nil }
	s.version = func(driver.Install) (string, error) { return "150.0.7871.186", nil }
	s.probe = func(context.Context, driver.Install) (string, error) { return "150.0.0.0", nil }
	s.retryDelay = time.Millisecond
	s.client = &http.Client{}
	s.emit = func(name string, data any) {
		if j, ok := data.(JobDTO); ok && name == "engine:job" {
			f.mu.Lock()
			f.events = append(f.events, j)
			f.mu.Unlock()
		}
	}
	f.svc = s
	return f
}

// chromeZip returns a zip that holds the browser binary where CfTInstall
// expects it, plus one more file.
func chromeZip(t *testing.T, platform string) []byte {
	t.Helper()
	rel, err := filepath.Rel("x", driver.CfTInstall("x", platform).Path)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{filepath.ToSlash(rel): strings.Repeat("chrome-binary ", 5000), "chrome-" + platform + "/locales/en-US.pak": "pak"} {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func (f *engineFixture) offer(url, version string) {
	f.svc.fetchVersions = func(context.Context) ([]driver.CfTBuild, error) {
		return []driver.CfTBuild{{Version: version, Channel: "Stable", Platform: f.svc.platform, URL: url}}, nil
	}
}

func (f *engineFixture) wait(t *testing.T, id string, states ...string) JobDTO {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		j, err := f.svc.GetJob(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range states {
			if j.State == s {
				return j
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	j, _ := f.svc.GetJob(context.Background(), id)
	t.Fatalf("job %s did not reach %v, state %s stage %s err %+v", id, states, j.State, j.Stage, j.Error)
	return j
}

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestEngineListThreeTiers(t *testing.T) {
	f := newEngineFixture(t)
	chrome := t.TempDir() + "/chrome"
	edge := t.TempDir() + "/msedge"
	for _, p := range []string{chrome, edge} {
		os.WriteFile(p, []byte("x"), 0o755)
	}
	f.svc.detect = func() ([]driver.Install, error) {
		return []driver.Install{{Path: chrome, Brand: driver.BrandChrome}, {Path: edge, Brand: driver.BrandEdge}}, nil
	}
	list, err := f.svc.ListEngines(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 5 {
		t.Fatalf("got %d engines", len(list))
	}
	want := []struct {
		id, status string
		tier       int
	}{{"chrome", "ready", 1}, {"edge", "ready", 1}, {"cft", "missing", 2}, {"fpc", "missing", 2}, {"patched", "unavailable", 3}}
	for i, w := range want {
		if list[i].ID != w.id || list[i].Status != w.status || list[i].Tier != w.tier {
			t.Errorf("engine %d = %+v", i, list[i])
		}
	}
	if !list[0].Default || list[1].Default || list[0].Active != "150.0.7871.186" || list[0].Path != chrome {
		t.Errorf("chrome row %+v", list[0])
	}

	// Without Chrome, Edge becomes the effective default.
	f.svc.detect = func() ([]driver.Install, error) { return []driver.Install{{Path: edge, Brand: driver.BrandEdge}}, nil }
	list, _ = f.svc.ListEngines(context.Background())
	if list[0].Status != "missing" || !list[1].Default {
		t.Errorf("edge fallback: %+v %+v", list[0], list[1])
	}
	// Nothing installed: the doctor says so and offers Chrome for Testing.
	f.svc.detect = func() ([]driver.Install, error) { return nil, nil }
	ds, _ := f.svc.RunDiagnostics(context.Background())
	if ds[0].ID != "browser" || ds[0].Level != "fail" || ds[0].FixID != "installCft" {
		t.Errorf("browser check: %+v", ds[0])
	}
	if err := f.svc.SetDefaultEngine(context.Background(), "chrome"); !isCode(err, "engine.missing") {
		t.Errorf("default of a missing engine: %v", err)
	}
	if err := f.svc.SetDefaultEngine(context.Background(), "patched"); !isCode(err, "engine.unavailable") {
		t.Errorf("default of the own Chromium: %v", err)
	}
}

func isCode(err error, code string) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Code == code
}

func TestEngineDoctor(t *testing.T) {
	f := newEngineFixture(t)
	chrome := t.TempDir() + "/chrome"
	os.WriteFile(chrome, []byte("x"), 0o755)
	f.svc.detect = func() ([]driver.Install, error) {
		return []driver.Install{{Path: chrome, Brand: driver.BrandChrome}}, nil
	}
	byID := func(ds []Diag) map[string]Diag {
		m := map[string]Diag{}
		for _, d := range ds {
			m[d.ID] = d
		}
		return m
	}
	ds, _ := f.svc.RunDiagnostics(context.Background())
	m := byID(ds)
	if len(ds) != 6 {
		t.Fatalf("want 6 checks, got %d", len(ds))
	}
	for _, id := range []string{"browser", "policy", "pipe", "folder", "job", "relay"} {
		if m[id].Level != "ok" {
			t.Errorf("%s: %+v", id, m[id])
		}
	}
	if m["pipe"].Params["version"] != "150.0.0.0" {
		t.Errorf("pipe params %+v", m["pipe"].Params)
	}

	f.svc.probe = func(ctx context.Context, _ driver.Install) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	f.svc.probeTimeout = 30 * time.Millisecond
	m = byID(mustDiag(t, f))
	if m["pipe"].Level != "fail" || m["pipe"].Code != "timeout" {
		t.Errorf("slow probe: %+v", m["pipe"])
	}
	f.svc.probe = func(context.Context, driver.Install) (string, error) {
		return "", errors.Join(driver.ErrDebuggingBlocked, errors.New("x"))
	}
	m = byID(mustDiag(t, f))
	if m["pipe"].Code != "blocked" || m["pipe"].FixID != "installCft" {
		t.Errorf("blocked probe: %+v", m["pipe"])
	}
	// A data folder that does not exist cannot be written.
	f.svc.dataDir = filepath.Join(f.dir, "missing")
	m = byID(mustDiag(t, f))
	if m["folder"].Level != "fail" || m["folder"].Code != "notWritable" {
		t.Errorf("folder: %+v", m["folder"])
	}
}

func mustDiag(t *testing.T, f *engineFixture) []Diag {
	t.Helper()
	ds, err := f.svc.RunDiagnostics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

func TestEngineInstallRecordsChecksum(t *testing.T) {
	f := newEngineFixture(t)
	body := chromeZip(t, f.svc.platform)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "chrome.zip", time.Time{}, bytes.NewReader(body))
	}))
	defer srv.Close()
	f.offer(srv.URL, "150.0.7871.186")

	job, err := f.svc.InstallEngine(context.Background(), InstallRequest{EngineID: "cft"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.InstallEngine(context.Background(), InstallRequest{EngineID: "cft"}); !isCode(err, "conflict") {
		t.Errorf("second job while one runs: %v", err)
	}
	j := f.wait(t, job.ID, jobDone, jobFailed)
	if j.State != jobDone {
		t.Fatalf("job %+v", j)
	}
	for _, s := range j.Stages {
		if s.State != "done" {
			t.Errorf("stage %+v", s)
		}
	}
	if j.BytesDone != int64(len(body)) || j.BytesTotal != int64(len(body)) {
		t.Errorf("bytes %d/%d, zip is %d", j.BytesDone, j.BytesTotal, len(body))
	}
	list, _ := f.svc.ListEngines(context.Background())
	cft := list[2]
	if cft.Status != "ready" || cft.Active != "150.0.7871.186" || len(cft.Installed) != 1 || cft.Installed[0].SHA256 != sha(body) || cft.Installed[0].SizeBytes == 0 {
		t.Errorf("cft row %+v", cft)
	}
	if _, err := os.Stat(driver.CfTInstall(f.svc.versionDir("150.0.7871.186"), f.svc.platform).Path); err != nil {
		t.Errorf("binary missing: %v", err)
	}
	if sums := f.svc.readSums(); sums[sumKey(f.svc.platform, "150.0.7871.186")] != sha(body) {
		t.Errorf("recorded sums %v", sums)
	}
	if _, err := os.Stat(f.svc.zipPath("150.0.7871.186")); err == nil {
		t.Error("zip was not removed after the switch")
	}
	f.mu.Lock()
	n := len(f.events)
	f.mu.Unlock()
	if n < 6 {
		t.Errorf("only %d job events", n)
	}
	// The same version again is refused.
	job, _ = f.svc.InstallEngine(context.Background(), InstallRequest{EngineID: "cft"})
	if j := f.wait(t, job.ID, jobDone, jobFailed); j.State != jobFailed || j.Error.Code != "engine.installed" {
		t.Errorf("reinstall: %+v", j)
	}
	// A stock engine and the own Chromium cannot be installed.
	if _, err := f.svc.InstallEngine(context.Background(), InstallRequest{EngineID: "chrome"}); !isCode(err, "engine.system") {
		t.Errorf("install chrome: %v", err)
	}
	if _, err := f.svc.InstallEngine(context.Background(), InstallRequest{EngineID: "patched"}); !isCode(err, "engine.unavailable") {
		t.Errorf("install patched: %v", err)
	}
	// Default switches to Chrome for Testing and the run engine follows.
	if err := f.svc.SetDefaultEngine(context.Background(), "cft"); err != nil {
		t.Fatal(err)
	}
	if got := f.svc.effectiveDefault(); got != "cft" {
		t.Errorf("default %s", got)
	}
}

func TestEngineCancelKeepsPartialThenResumes(t *testing.T) {
	f := newEngineFixture(t)
	body := chromeZip(t, f.svc.platform)
	var ranged atomic.Int32
	var first atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			ranged.Add(1)
			http.ServeContent(w, r, "chrome.zip", time.Time{}, bytes.NewReader(body))
			return
		}
		if first.CompareAndSwap(false, true) { // send a part, then stall until the client leaves
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.Write(body[:100])
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		http.ServeContent(w, r, "chrome.zip", time.Time{}, bytes.NewReader(body))
	}))
	defer srv.Close()
	f.offer(srv.URL, "150.0.7871.186")

	job, _ := f.svc.InstallEngine(context.Background(), InstallRequest{EngineID: "cft"})
	deadline := time.Now().Add(10 * time.Second)
	for fileSize(f.svc.partPath("150.0.7871.186")) < 100 {
		if time.Now().After(deadline) {
			t.Fatal("partial file never reached 100 bytes")
		}
		time.Sleep(5 * time.Millisecond)
	}
	j, err := f.svc.CancelJob(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != jobCancelled || !j.PartialKept {
		t.Fatalf("after cancel: %+v", j)
	}
	if list, _ := f.svc.ListEngines(context.Background()); list[2].Status != "missing" {
		t.Errorf("a cancelled job installed something: %+v", list[2])
	}

	job2, err := f.svc.ApplyJobFix(context.Background(), job.ID, "retry")
	if err != nil {
		t.Fatal(err)
	}
	if j := f.wait(t, job2.ID, jobDone, jobFailed); j.State != jobDone {
		t.Fatalf("resume: %+v", j)
	}
	if ranged.Load() == 0 {
		t.Error("the second attempt did not ask for a range")
	}
	if got := f.svc.readSums()[sumKey(f.svc.platform, "150.0.7871.186")]; got != sha(body) {
		t.Errorf("sha after resume %q", got)
	}
}

func TestEngineDownloadRetriesThenFails(t *testing.T) {
	f := newEngineFixture(t)
	body := chromeZip(t, f.svc.platform)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		http.ServeContent(w, r, "chrome.zip", time.Time{}, bytes.NewReader(body))
	}))
	defer srv.Close()
	f.offer(srv.URL, "150.0.7871.186")
	job, _ := f.svc.InstallEngine(context.Background(), InstallRequest{EngineID: "cft"})
	j := f.wait(t, job.ID, jobDone, jobFailed)
	if j.State != jobDone || j.Attempt != 3 {
		t.Fatalf("third attempt should work: %+v", j)
	}

	// A server that never works fails after three attempts with a retry fix.
	f2 := newEngineFixture(t)
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", 500) }))
	defer down.Close()
	f2.offer(down.URL, "150.0.7871.186")
	job, _ = f2.svc.InstallEngine(context.Background(), InstallRequest{EngineID: "cft"})
	j = f2.wait(t, job.ID, jobDone, jobFailed)
	if j.State != jobFailed || j.Error.Code != "engine.download" || j.Error.Details["fixId"] != "retry" || j.Stages[1].State != "fail" {
		t.Fatalf("down server: %+v", j)
	}

	// No network for the version list: a check failure.
	f3 := newEngineFixture(t)
	f3.svc.fetchVersions = func(context.Context) ([]driver.CfTBuild, error) { return nil, errors.New("dial tcp: no route") }
	job, _ = f3.svc.InstallEngine(context.Background(), InstallRequest{EngineID: "cft"})
	if j := f3.wait(t, job.ID, jobFailed); j.Error.Code != "engine.network" || j.Stages[0].State != "fail" {
		t.Errorf("no network: %+v", j)
	}
	if _, err := f3.svc.CheckEngineUpdates(context.Background()); !isCode(err, "engine.network") {
		t.Errorf("check updates: %v", err)
	}
}

func TestEngineChecksumMismatchOnRedownload(t *testing.T) {
	f := newEngineFixture(t)
	body := chromeZip(t, f.svc.platform)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "chrome.zip", time.Time{}, bytes.NewReader(body))
	}))
	defer srv.Close()
	f.offer(srv.URL, "150.0.7871.186")
	job, _ := f.svc.InstallEngine(context.Background(), InstallRequest{EngineID: "cft"})
	f.wait(t, job.ID, jobDone)

	// The same file again passes and keeps the record.
	job, _ = f.svc.RedownloadEngine(context.Background(), "cft", "150.0.7871.186")
	if j := f.wait(t, job.ID, jobDone, jobFailed); j.State != jobDone || j.Stages[2].Code != "shaMatches" {
		t.Fatalf("redownload of the same file: %+v", j)
	}

	// The server now serves other bytes for the same version.
	other := chromeZip(t, f.svc.platform)
	other = append(other[:len(other):len(other)], 0) // trailing byte: still a zip, other hash
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "chrome.zip", time.Time{}, bytes.NewReader(other))
	}))
	defer srv2.Close()
	f.offer(srv2.URL, "150.0.7871.186")
	job, _ = f.svc.RedownloadEngine(context.Background(), "cft", "150.0.7871.186")
	j := f.wait(t, job.ID, jobDone, jobFailed)
	if j.State != jobFailed || j.Error.Code != "engine.checksum" || j.Error.Details["fixId"] != "redownload" {
		t.Fatalf("changed file: %+v", j)
	}
	if f.svc.metaOf("150.0.7871.186") != nil {
		t.Error("a mismatching download was installed")
	}
}

func TestEngineSelftestFailureKeepsOldEngine(t *testing.T) {
	f := newEngineFixture(t)
	body := chromeZip(t, f.svc.platform)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "chrome.zip", time.Time{}, bytes.NewReader(body))
	}))
	defer srv.Close()
	f.offer(srv.URL, "150.0.1.1")
	job, _ := f.svc.InstallEngine(context.Background(), InstallRequest{EngineID: "cft"})
	f.wait(t, job.ID, jobDone)

	f.svc.probe = func(context.Context, driver.Install) (string, error) { return "", driver.ErrDebuggingBlocked }
	f.offer(srv.URL, "151.0.1.1")
	job, _ = f.svc.UpdateEngine(context.Background(), "cft")
	j := f.wait(t, job.ID, jobDone, jobFailed)
	if j.State != jobFailed || j.Error.Code != "engine.policy" || j.Stages[4].State != "fail" {
		t.Fatalf("policy: %+v", j)
	}
	if st := f.svc.readState(); st.Active != "150.0.1.1" {
		t.Errorf("old engine lost: %+v", st)
	}
	if f.svc.metaOf("151.0.1.1") != nil {
		t.Error("the failed version stayed installed")
	}

	f.svc.probe = func(context.Context, driver.Install) (string, error) { return "", errors.New("no answer") }
	job, _ = f.svc.UpdateEngine(context.Background(), "cft")
	if j := f.wait(t, job.ID, jobDone, jobFailed); j.Error.Code != "engine.selftest" {
		t.Errorf("selftest code %+v", j.Error)
	}
}

func TestEngineUpdateKeepsPreviousAndRollsBack(t *testing.T) {
	f := newEngineFixture(t)
	body := chromeZip(t, f.svc.platform)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "chrome.zip", time.Time{}, bytes.NewReader(body))
	}))
	defer srv.Close()
	install := func(v string) {
		t.Helper()
		f.offer(srv.URL, v)
		job, err := f.svc.UpdateEngine(context.Background(), "cft")
		if err != nil {
			t.Fatal(err)
		}
		if j := f.wait(t, job.ID, jobDone, jobFailed); j.State != jobDone {
			t.Fatalf("%s: %+v", v, j)
		}
		time.Sleep(2 * time.Millisecond) // installedAt orders the versions
	}
	install("150.0.1.1")
	install("151.0.1.1")
	st := f.svc.readState()
	if st.Active != "151.0.1.1" || st.Previous != "150.0.1.1" {
		t.Fatalf("state %+v", st)
	}
	install("152.0.1.1") // keepOld is 1: 150 goes away
	st = f.svc.readState()
	if st.Active != "152.0.1.1" || st.Previous != "151.0.1.1" || f.svc.metaOf("150.0.1.1") != nil {
		t.Fatalf("after prune %+v", st)
	}
	if err := f.svc.RollbackEngine(context.Background(), "cft"); err != nil {
		t.Fatal(err)
	}
	if st = f.svc.readState(); st.Active != "151.0.1.1" || st.Previous != "152.0.1.1" {
		t.Fatalf("after rollback %+v", st)
	}
	// Remove the active version: the previous one takes over.
	if err := f.svc.RemoveEngine(context.Background(), "cft", "151.0.1.1"); err != nil {
		t.Fatal(err)
	}
	if st = f.svc.readState(); st.Active != "152.0.1.1" || st.Previous != "" {
		t.Fatalf("after remove %+v", st)
	}
	if err := f.svc.RemoveEngine(context.Background(), "cft", "../etc"); !isCode(err, "notFound") {
		t.Errorf("path version: %v", err)
	}
	if err := f.svc.RemoveEngine(context.Background(), "chrome", ""); !isCode(err, "engine.system") {
		t.Errorf("remove chrome: %v", err)
	}
	// While a run is active the browser must not be removed.
	f.svc.activeRuns = func() int { return 1 }
	if err := f.svc.RemoveEngine(context.Background(), "cft", ""); !isCode(err, "conflict") {
		t.Errorf("remove during run: %v", err)
	}
	f.svc.activeRuns = nil
	if err := f.svc.RemoveEngine(context.Background(), "cft", ""); err != nil {
		t.Fatal(err)
	}
	if st = f.svc.readState(); st.Active != "" || f.svc.installFor("cft") != nil {
		t.Errorf("after remove all %+v", st)
	}
}

func TestEngineUnzipRefusesEscape(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("../evil.txt")
	w.Write([]byte("x"))
	zw.Close()
	dir := t.TempDir()
	zp := filepath.Join(dir, "a.zip")
	os.WriteFile(zp, buf.Bytes(), 0o600)
	_, _, err := unzipTo(context.Background(), zp, filepath.Join(dir, "out"))
	if !isCode(err, "engine.unpack") {
		t.Fatalf("zip slip: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "evil.txt")); err == nil {
		t.Fatal("file written outside the folder")
	}
}

func TestEnginePinsAndSettings(t *testing.T) {
	f := newEngineFixture(t)
	chrome := t.TempDir() + "/chrome"
	os.WriteFile(chrome, []byte("x"), 0o755)
	f.svc.detect = func() ([]driver.Install, error) {
		return []driver.Install{{Path: chrome, Brand: driver.BrandChrome}}, nil
	}
	ctx := context.Background()
	if err := f.svc.SetGroupPin(ctx, "Ads", "chrome"); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetProfileEngine(ctx, "p1", "edge"); !isCode(err, "engine.missing") {
		t.Errorf("pin to a missing engine: %v", err)
	}
	if err := f.svc.SetProfileEngine(ctx, "p2", "chrome"); err != nil {
		t.Fatal(err)
	}
	pins, _ := f.svc.GetPins(ctx)
	if pins.Groups["Ads"] != "chrome" || pins.Profiles["p2"] != "chrome" {
		t.Errorf("pins %+v", pins)
	}
	f.svc.SetGroupPin(ctx, "Ads", "")
	if pins, _ = f.svc.GetPins(ctx); len(pins.Groups) != 0 {
		t.Errorf("pin not removed %+v", pins)
	}
	if _, err := f.svc.SetUpdateSettings(ctx, EngineUpdateSettings{Channel: "Nightly", AutoCheck: "daily", KeepOld: 9}); !isCode(err, "invalid") {
		t.Errorf("bad settings: %v", err)
	}
	got, err := f.svc.SetUpdateSettings(ctx, EngineUpdateSettings{Channel: "Beta", AutoCheck: "weekly", KeepOld: 2, BandwidthLimit: 1 << 20})
	if err != nil || got.Channel != "Beta" || got.KeepOld != 2 {
		t.Fatalf("settings %+v %v", got, err)
	}
	// Check for updates caches the list and records the time.
	f.svc.fetchVersions = func(context.Context) ([]driver.CfTBuild, error) {
		return []driver.CfTBuild{{Version: "157.0.1.1", Channel: "Stable", URL: "http://127.0.0.1:1/x"}, {Version: "158.0.1.1", Channel: "Beta", URL: "http://127.0.0.1:1/y"}}, nil
	}
	list, err := f.svc.CheckEngineUpdates(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("builds %+v %v", list, err)
	}
	engines, _ := f.svc.ListEngines(ctx)
	if engines[2].Latest != "158.0.1.1" {
		t.Errorf("latest of the Beta channel: %q", engines[2].Latest)
	}
	if u, _ := f.svc.GetUpdateSettings(ctx); u.LastChecked == "" {
		t.Error("last check not recorded")
	}
}
