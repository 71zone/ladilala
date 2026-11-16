package driver

import (
	"os"
	"strings"
	"testing"
)

func TestParseCfTStable(t *testing.T) {
	f, err := os.Open("testdata/cft-last-known-good.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, err := ParseCfTStable(f, "win64")
	if err != nil {
		t.Fatal(err)
	}
	if b.Version != "157.0.8103.78" || !strings.HasSuffix(b.URL, "/157.0.8103.78/win64/chrome-win64.zip") {
		t.Fatalf("unexpected build %+v", b)
	}
	if _, err := ParseCfTStable(strings.NewReader(`{"channels":{"Stable":{"version":"1.0.0.0","downloads":{"chrome":[]}}}}`), "win64"); err == nil {
		t.Fatal("missing platform must fail")
	}
	if _, err := ParseCfTStable(strings.NewReader(`{"channels":{}}`), "win64"); err == nil {
		t.Fatal("missing Stable must fail")
	}
}

func TestCfTPlatform(t *testing.T) {
	for _, c := range [][3]string{{"windows", "amd64", "win64"}, {"darwin", "arm64", "mac-arm64"}, {"linux", "amd64", "linux64"}} {
		if got := CfTPlatform(c[0], c[1]); got != c[2] {
			t.Errorf("%s/%s = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

func TestParseCfTVersions(t *testing.T) {
	f, err := os.Open("testdata/cft-last-known-good.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := ParseCfTVersions(f, "win64")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 || got[0].Channel != "Stable" || got[0].Version != "157.0.8103.78" {
		t.Fatalf("Stable must come first: %+v", got)
	}
	seen := map[string]bool{}
	for _, b := range got {
		if seen[b.Channel] || b.Platform != "win64" || !strings.HasSuffix(b.URL, "/win64/chrome-win64.zip") {
			t.Errorf("bad build %+v", b)
		}
		seen[b.Channel] = true
	}
	if _, err := ParseCfTVersions(strings.NewReader(`{"channels":{"Stable":{"version":"1.0.0.0","downloads":{"chrome":[]}}}}`), "win64"); err == nil {
		t.Fatal("no download for the platform must fail")
	}
}
