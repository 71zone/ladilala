package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/8bu/ladilala/core/driver"
	"github.com/8bu/ladilala/core/engine"
	"github.com/8bu/ladilala/core/inject"
	"github.com/8bu/ladilala/core/store"
)

// TestE2EProfileService starts a profile in real Chrome through ProfileService.
// The profile has a proxy with a login. The page is a name that only the proxy
// can resolve, so the page loads only when the relay sent the browser through
// the proxy with the stored credentials. The page also reports the navigator
// values of the profile. Then the profile stops, the data folder stays and the
// profile starts again from it.
//
//	LADILALA_E2E=1 go test -count=1 -v -run TestE2EProfileService ./app/
func TestE2EProfileService(t *testing.T) {
	if os.Getenv("LADILALA_E2E") == "" {
		t.Skip("LADILALA_E2E not set")
	}
	if ins, _ := (driver.StockChrome{}).Detect(); len(ins) == 0 {
		t.Skip("no Chrome or Edge installed")
	}
	var served atomic.Int32
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served.Add(1)
		fmt.Fprint(w, "<!doctype html><title>exit</title><body>EXIT-MARKER</body>")
	}))
	defer site.Close()
	su, _ := url.Parse(site.URL)
	phost, pport := pfStartProxyHosts(t, "alice", "pw-e2e", map[string]string{"exit-probe.test:80": su.Host, "exit-probe.test": su.Host})

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "db"), &store.MemorySecrets{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	l, err := engine.NewStockLauncher()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ev := &pfEvents{}
	pr := newProxyProber(dir)
	ps := newProfileServiceWith(st, dir, l, ev.emit, pr)
	px := newProxyServiceWith(st, dir, ev.emit, pr, ps.isRunning)

	pw := "pw-e2e"
	proxyDTO, err := px.CreateProxy(ctx, ProxyInput{Scheme: "http", Host: phost, Port: pport, Username: "alice", Password: &pw})
	if err != nil {
		t.Fatal(err)
	}
	p, err := ps.CreateProfile(ctx, ProfileInput{Name: "E2E", Preset: "windows-11", Language: "de-DE", Timezone: "Europe/Berlin", ProxyID: proxyDTO.ID})
	if err != nil {
		t.Fatal(err)
	}
	fp := p.Fingerprint.Fingerprint

	start := func() *driver.StockSession {
		res, err := ps.StartProfiles(ctx, []string{p.ID})
		if err != nil || !res[0].OK {
			t.Fatalf("start: %+v %v", res, err)
		}
		ps.mu.Lock()
		rp := ps.running[p.ID]
		ps.mu.Unlock()
		sb, ok := rp.browser.(interface{ Session() *driver.StockSession })
		if !ok {
			t.Fatalf("browser %T has no Session", rp.browser)
		}
		return sb.Session()
	}
	sess := start()
	if got, _ := ps.GetProfile(ctx, p.ID); got.State != "running" {
		t.Fatalf("state %s", got.State)
	}
	pg, err := sess.WaitPage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := pg.Navigate(ctx, "http://exit-probe.test/"); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Body string `json:"body"`
		UA   string `json:"ua"`
		Plat string `json:"plat"`
		HW   int    `json:"hw"`
		Lang string `json:"lang"`
		TZ   string `json:"tz"`
	}
	const js = `({ body: document.body.innerText, ua: navigator.userAgent, plat: navigator.platform, hw: navigator.hardwareConcurrency, lang: navigator.language, tz: Intl.DateTimeFormat().resolvedOptions().timeZone })`
	if err := pg.Eval(ctx, js, &got); err != nil {
		t.Fatal(err)
	}
	cfg := inject.NewConfig(fp)
	if got.Body != "EXIT-MARKER" || served.Load() == 0 {
		t.Fatalf("the page did not load through the proxy: %+v served=%d", got, served.Load())
	}
	if got.UA != fp.UserAgent || got.Plat != cfg.NavigatorPlatform || got.HW != fp.HardwareThreads || got.Lang != "de-DE" || got.TZ != "Europe/Berlin" {
		t.Fatalf("navigator values differ from the profile: %+v want ua=%q hw=%d", got, fp.UserAgent, fp.HardwareThreads)
	}

	if err := ps.StopProfiles(ctx, []string{p.ID}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sess.Done():
	case <-time.After(15 * time.Second):
		t.Fatal("the browser is still alive after Stop")
	}
	if got, _ := ps.GetProfile(ctx, p.ID); got.State != "stopped" {
		t.Fatalf("state after stop %s", got.State)
	}
	ents, err := os.ReadDir(p.DataDir)
	if err != nil || len(ents) == 0 {
		t.Fatalf("the data folder did not persist: %v %v", ents, err)
	}
	// The second start reuses the folder and the lock is free.
	sess2 := start()
	if _, err := sess2.WaitPage(ctx); err != nil {
		t.Fatal(err)
	}
	if err := ps.StopAll(ctx); err != nil {
		t.Fatal(err)
	}
	if s := ev.states(p.ID); len(s) != 4 || s[0] != "running//" || s[1] != "stopped//" {
		t.Fatalf("events %v", s)
	}
}
