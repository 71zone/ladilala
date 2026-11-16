package driver

import (
	"os"
	"path/filepath"
	"testing"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestScanDirDepth(t *testing.T) {
	root := t.TempDir()
	near := filepath.Join(root, "ungoogled-chromium_150_windows_x64", "chrome.exe")
	far := filepath.Join(root, "a", "b", "c", "d", "chrome.exe")
	touch(t, near)
	touch(t, far)
	touch(t, filepath.Join(root, "other.exe"))
	got, err := scanDir(root, binaryNames("windows"), 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != near {
		t.Fatalf("got %v, want only %s", got, near)
	}
	if got, err := scanDir(filepath.Join(root, "missing"), binaryNames("windows"), 4); err != nil || got != nil {
		t.Fatalf("missing dir: %v %v", got, err)
	}
}

func TestFingerprintChromiumDetectUsesOnlySearchDirs(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "engine", binaryNames(hostOS)[0]))
	got, err := FingerprintChromium{SearchDirs: []string{root, root}}.Detect()
	if err != nil || len(got) != 1 || got[0].Brand != BrandChromium {
		t.Fatalf("got %v %v", got, err)
	}
	if got, _ := (FingerprintChromium{}).Detect(); len(got) != 0 {
		t.Fatalf("no search dirs must find nothing, got %v", got)
	}
}

func TestSystemCandidatesOrderAndExistence(t *testing.T) {
	root := t.TempDir()
	env := map[string]string{"PROGRAMFILES": filepath.Join(root, "pf"), "LOCALAPPDATA": filepath.Join(root, "local")}
	cands := systemCandidates("windows", func(k string) string { return env[k] }, "")
	if len(cands) != 4 || cands[0].Brand != BrandChrome || cands[2].Brand != BrandEdge {
		t.Fatalf("candidates %v", cands)
	}
	touch(t, cands[3].Path) // Edge under LocalAppData
	touch(t, cands[0].Path) // Chrome under Program Files
	got := existingInstalls(append(cands, cands[0]), false)
	if len(got) != 2 || got[0].Brand != BrandChrome || got[1].Brand != BrandEdge {
		t.Fatalf("installs %v", got)
	}
	if d := systemCandidates("darwin", nil, "/Users/a"); d[0].Brand != BrandChrome || d[len(d)-1].Brand != BrandEdge {
		t.Fatalf("darwin order %v", d)
	}
}
