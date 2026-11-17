package driver

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
	"sync/atomic"
	"testing"
	"time"
)

// fakeBinaryName is the file FingerprintChromium.Detect looks for on this OS.
func fakeBinaryName() string { return binaryNames(runtime.GOOS)[len(binaryNames(runtime.GOOS))-1] }

// fakeZip builds a tiny zip with a browser binary under one folder. extra adds
// more entries (name to body).
func fakeZip(t testing.TB, withBinary bool, extra map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	if withBinary {
		add("ungoogled/"+fakeBinaryName(), strings.Repeat("binary ", 2000))
	}
	for n, b := range extra {
		add(n, b)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildFor(url string, body []byte) FPCBuild {
	sum := sha256.Sum256(body)
	return FPCBuild{Tag: "148.0.1.2", URL: url, Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:]), ChromiumMajor: 148}
}

func serve(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "x.zip", time.Time{}, bytes.NewReader(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// nothingLeft fails when the engine folder holds anything but empty
// directories: no tag folder, no staging folder, no partial download.
func nothingLeft(t *testing.T, dir string) {
	t.Helper()
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			t.Errorf("left behind: %s", p)
		}
		return nil
	})
	if _, err := os.Stat(FPCTagDir(dir, "148.0.1.2")); err == nil {
		t.Errorf("tag folder exists")
	}
}

func TestInstallFPCGood(t *testing.T) {
	body := fakeZip(t, true, nil)
	srv := serve(t, body)
	dir := t.TempDir()
	b := buildFor(srv.URL, body)
	var stages []string
	var done int64
	meta, err := InstallFPC(context.Background(), FPCInstallOptions{
		Dir: dir, Build: b, Client: srv.Client(),
		Stage: func(s string) { stages = append(stages, s) },
		Hooks: DownloadHooks{Progress: func(d, _ int64) { done = d }},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(stages, ",") != "download,verify,unpack" || done != b.Size {
		t.Errorf("stages %v, progress %d of %d", stages, done, b.Size)
	}
	m, in, ok := FPCInstalled(dir, b.Tag)
	if !ok || m.SHA256 != b.SHA256 || meta.Tag != b.Tag || !in.FPC || !strings.HasSuffix(in.Path, fakeBinaryName()) {
		t.Fatalf("installed = %v %+v %+v", ok, m, in)
	}
	if _, ok := FPCPartial(dir, b.Tag); ok {
		t.Error("the partial download stays after a good install")
	}
	if _, err := os.Stat(FPCTagDir(dir, b.Tag) + ".staging"); err == nil {
		t.Error("staging folder stays")
	}
	if err := RemoveFPC(dir, b.Tag); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := FPCInstalled(dir, b.Tag); ok {
		t.Error("still installed after RemoveFPC")
	}
}

func TestInstallFPCHashMismatchLeavesNothing(t *testing.T) {
	body := fakeZip(t, true, nil)
	srv := serve(t, body)
	dir := t.TempDir()
	b := buildFor(srv.URL, body)
	b.SHA256 = strings.Repeat("ab", 32)
	_, err := InstallFPC(context.Background(), FPCInstallOptions{Dir: dir, Build: b, Client: srv.Client()})
	if !errors.Is(err, ErrFPCHashMismatch) {
		t.Fatalf("err = %v", err)
	}
	nothingLeft(t, dir)
}

func TestInstallFPCRejectsZipSlip(t *testing.T) {
	body := fakeZip(t, true, map[string]string{"../../evil.txt": "x"})
	srv := serve(t, body)
	root := t.TempDir()
	dir := filepath.Join(root, "engines")
	b := buildFor(srv.URL, body)
	_, err := InstallFPC(context.Background(), FPCInstallOptions{Dir: dir, Build: b, Client: srv.Client()})
	var uz *UnsafeZipError
	if !errors.As(err, &uz) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "evil.txt")); err == nil {
		t.Error("the entry escaped the folder")
	}
	nothingLeft(t, dir)
}

func TestInstallFPCNoBinary(t *testing.T) {
	body := fakeZip(t, false, map[string]string{"readme.txt": "x"})
	srv := serve(t, body)
	dir := t.TempDir()
	_, err := InstallFPC(context.Background(), FPCInstallOptions{Dir: dir, Build: buildFor(srv.URL, body), Client: srv.Client()})
	if !errors.Is(err, ErrFPCNoBinary) {
		t.Fatalf("err = %v", err)
	}
	nothingLeft(t, dir)
}

func TestInstallFPCOfflineLeavesNothing(t *testing.T) {
	body := fakeZip(t, true, nil)
	srv := serve(t, body)
	b := buildFor(srv.URL, body)
	srv.Close() // the server is gone
	dir := t.TempDir()
	var attempts int
	_, err := InstallFPC(context.Background(), FPCInstallOptions{
		Dir: dir, Build: b, Client: &http.Client{Timeout: 2 * time.Second}, RetryDelay: time.Millisecond,
		Attempt: func(n int) { attempts = n },
	})
	if !errors.Is(err, ErrFPCDownload) || attempts != 3 {
		t.Fatalf("err = %v after %d attempts", err, attempts)
	}
	nothingLeft(t, dir)
}

func TestInstallFPCCancelKeepsPartialThenResumes(t *testing.T) {
	body := fakeZip(t, true, nil)
	half := len(body) / 2
	var hits, ranged atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 { // first request: send half, then stall
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.WriteHeader(http.StatusOK)
			w.Write(body[:half])
			w.(http.Flusher).Flush()
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		if r.Header.Get("Range") != "" {
			ranged.Add(1)
		}
		http.ServeContent(w, r, "x.zip", time.Time{}, bytes.NewReader(body))
	}))
	defer srv.Close()
	defer close(release)
	dir := t.TempDir()
	b := buildFor(srv.URL, body)

	ctx, cancel := context.WithCancel(context.Background())
	_, err := InstallFPC(ctx, FPCInstallOptions{
		Dir: dir, Build: b, Client: srv.Client(),
		Hooks: DownloadHooks{Progress: func(done, _ int64) {
			if done >= int64(half) {
				cancel()
			}
		}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if _, _, ok := FPCInstalled(dir, b.Tag); ok {
		t.Error("a cancelled install must not install")
	}
	part, ok := FPCPartial(dir, b.Tag)
	if !ok {
		t.Fatal("cancel must keep the partial download")
	}
	if st, _ := os.Stat(part); st.Size() == 0 || st.Size() >= int64(len(body)) {
		t.Fatalf("partial size %d of %d", st.Size(), len(body))
	}
	if _, err := InstallFPC(context.Background(), FPCInstallOptions{Dir: dir, Build: b, Client: srv.Client()}); err != nil {
		t.Fatal(err)
	}
	if ranged.Load() != 1 {
		t.Errorf("the second install must continue with a Range request, got %d", ranged.Load())
	}
}

func TestPinnedBuildIsComplete(t *testing.T) {
	for _, b := range FPCBuilds {
		if !validFPCTag(b.Tag) || len(b.SHA256) != 64 || b.Size <= 0 || !strings.Contains(b.URL, b.Tag) || b.ChromiumMajor == 0 {
			t.Errorf("incomplete pin %+v", b)
		}
		if strings.HasPrefix(b.Tag, "150.") {
			t.Errorf("150 crashes on the first canvas read and must never be offered: %+v", b)
		}
	}
}
