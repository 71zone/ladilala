package driver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/8bu/ladilala/core/profile"
)

func flagValue(fl []flag, name string) (any, bool) {
	var v any
	var ok bool
	for _, f := range fl { // last write wins, as in chromedp
		if f.name == name {
			v, ok = f.value, true
		}
	}
	return v, ok
}

func TestLaunchFlags(t *testing.T) {
	fl := launchFlags("/data/p1", []string{"--lang=de-DE", "--disable-non-proxied-udp"})
	for _, name := range []string{"headless", "enable-automation", "disable-extensions", "disable-popup-blocking", "start-maximized"} {
		if v, ok := flagValue(fl, name); !ok || v != false {
			t.Errorf("%s must be removed, got %v %v", name, v, ok)
		}
	}
	for name, want := range map[string]any{
		"no-first-run": true, "no-default-browser-check": true, "disable-session-crashed-bubble": true,
		"user-data-dir": "/data/p1", "lang": "de-DE", "disable-non-proxied-udp": true,
	} {
		if v, _ := flagValue(fl, name); v != want {
			t.Errorf("%s = %v, want %v", name, v, want)
		}
	}
}

func TestLaunchRejectsBeforeStarting(t *testing.T) {
	l := &Launcher{}
	ctx := context.Background()
	p := profile.Profile{DataDir: ""}
	if _, err := l.Launch(ctx, SystemChrome{}, Install{Path: os.Args[0]}, p, ""); !errors.Is(err, ErrInvalidDataDir) {
		t.Fatalf("empty dir: %v", err)
	}
	p.DataDir = filepath.Join(t.TempDir(), "p")
	if _, err := l.Launch(ctx, SystemChrome{}, Install{Path: filepath.Join(p.DataDir, "nope")}, p, ""); !errors.Is(err, ErrBinaryNotFound) {
		t.Fatalf("missing binary: %v", err)
	}
	p.Proxy = profile.Proxy{Scheme: profile.ProxyHTTP, Host: "h", Port: 1}
	if _, err := l.Launch(ctx, SystemChrome{}, Install{Path: os.Args[0]}, p, ""); !errors.Is(err, ErrProxyRequired) {
		t.Fatalf("proxy without relay: %v", err)
	}
}

func TestLauncherTracksOpenDirs(t *testing.T) {
	l := &Launcher{}
	k := dirKey("/x/y")
	if !l.acquire(k) || l.acquire(k) {
		t.Fatal("second acquire must fail")
	}
	l.release(k)
	if !l.acquire(k) {
		t.Fatal("acquire after release must succeed")
	}
}

func TestClassifyStartError(t *testing.T) {
	err := classifyStartError(os.ErrNotExist, Install{Path: "x"}, t.TempDir())
	if !errors.Is(err, ErrBinaryNotFound) {
		t.Fatalf("got %v", err)
	}
	if err := classifyStartError(context.Canceled, Install{}, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestProfileLockedOnDisk(t *testing.T) {
	if hostOS == "windows" {
		t.Skip("lockfile probe needs a running Chrome")
	}
	dir := t.TempDir()
	if profileLockedOnDisk(dir) {
		t.Fatal("empty dir reported locked")
	}
	lock := filepath.Join(dir, "SingletonLock")
	if err := os.Symlink("host-"+strconv.Itoa(os.Getpid()), lock); err != nil {
		t.Fatal(err)
	}
	if !profileLockedOnDisk(dir) {
		t.Fatal("live owner not reported")
	}
	os.Remove(lock)
	if err := os.Symlink("host-999999999", lock); err != nil {
		t.Fatal(err)
	}
	if profileLockedOnDisk(dir) {
		t.Fatal("stale lock reported as held")
	}
}
