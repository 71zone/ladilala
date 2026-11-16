package main

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// fakeLocations creates keys below HKCU\Software\LadilalaTest so the detection
// runs against a registry the test owns, never the real EdgeUpdate keys.
func fakeLocations(t *testing.T, pvs ...*string) []regLocation {
	t.Helper()
	base := `Software\LadilalaTest\` + strings.ReplaceAll(t.Name(), "/", "_")
	t.Cleanup(func() {
		for i := range pvs {
			_ = registry.DeleteKey(registry.CURRENT_USER, base+`\`+string(rune('a'+i)))
		}
		_ = registry.DeleteKey(registry.CURRENT_USER, base)
	})
	var locs []regLocation
	for i, pv := range pvs {
		path := base + `\` + string(rune('a'+i))
		if pv != nil {
			k, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
			if err != nil {
				t.Fatal(err)
			}
			if err := k.SetStringValue("pv", *pv); err != nil {
				t.Fatal(err)
			}
			k.Close()
		}
		locs = append(locs, regLocation{registry.CURRENT_USER, path, registry.QUERY_VALUE})
	}
	return locs
}

func sp(s string) *string { return &s }

func TestVersionAt(t *testing.T) {
	cases := []struct {
		name string
		pvs  []*string
		want string
	}{
		{"no key", []*string{nil, nil}, ""},
		{"empty pv", []*string{sp("")}, ""},
		{"uninstalled marker", []*string{sp("0.0.0.0")}, ""},
		{"marker then real", []*string{sp("0.0.0.0"), sp("120.0.1.2")}, "120.0.1.2"},
		{"first real wins", []*string{sp("1.2.3.4"), sp("5.6.7.8")}, "1.2.3.4"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := versionAt(fakeLocations(t, c.pvs...)); got != c.want {
				t.Errorf("version = %q, want %q", got, c.want)
			}
		})
	}
}

func TestEnsureWebView2Messages(t *testing.T) {
	missing := func() string { return "" }
	yes, no := func() bool { return true }, func() bool { return false }
	ok := func() error { return nil }

	if err := ensureWebView2With(func() string { return "1.0" }, no, nil); err != nil {
		t.Errorf("runtime present: %v", err)
	}
	err := ensureWebView2With(missing, no, ok)
	if err == nil || !strings.Contains(err.Error(), "missing") || !strings.Contains(err.Error(), webView2Page) {
		t.Errorf("declined: %v must say it is missing and link the download", err)
	}
	err = ensureWebView2With(missing, yes, func() error { return errors.New("HTTP 503") })
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") || !strings.Contains(err.Error(), webView2Page) {
		t.Errorf("installer failure: %v must carry the cause and the link", err)
	}
	err = ensureWebView2With(missing, yes, ok)
	if err == nil || !strings.Contains(err.Error(), "still missing") {
		t.Errorf("installer ran but runtime absent: %v", err)
	}
	n := 0
	if err := ensureWebView2With(func() string { n++; return map[int]string{1: "", 2: "9.9"}[n] }, yes, ok); err != nil {
		t.Errorf("installer succeeded: %v", err)
	}
}
