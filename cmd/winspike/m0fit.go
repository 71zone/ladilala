package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/8bu/ladilala/core/driver"
	"github.com/8bu/ladilala/core/profile"
	"github.com/8bu/ladilala/core/proxy"
)

// m0fit checks how a fingerprint-chromium build fits the app: per-profile
// extensions, the proxy relay, and the profile data directory.

const m0ExtManifest = `{"manifest_version":3,"name":"m0-probe","version":"1.0",
 "background":{"service_worker":"bg.js"},
 "content_scripts":[{"matches":["http://127.0.0.1/*"],"js":["m.js"],"run_at":"document_start","world":"MAIN","all_frames":true}]}`

func runM0Fit(args []string) error {
	fs := flag.NewFlagSet("m0fit", flag.ExitOnError)
	dir := fs.String("enginedir", "", "fingerprint-chromium directory")
	fs.Parse(args)
	eng, in, version, err := m0Engine("fpc", *dir)
	if err != nil {
		return err
	}
	l, err := driver.NewLauncher()
	if err != nil {
		return err
	}
	defer l.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	root, _ := os.MkdirTemp("", "m0fit-")
	defer os.RemoveAll(root)
	fmt.Printf("ENGINE %s %s\n", version, in.Path)
	prof := func(tag string, seed uint64) profile.Profile {
		return profile.Profile{ID: tag, DataDir: filepath.Join(root, tag), Fingerprint: m0Fingerprint(seed, version)}
	}

	var hits atomic.Int64
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, "<!doctype html><title>m0fit</title><body>ok</body>")
	}))
	defer site.Close()

	// 1. Extensions.
	extDir := filepath.Join(root, "ext")
	_ = os.MkdirAll(extDir, 0o755)
	_ = os.WriteFile(filepath.Join(extDir, "manifest.json"), []byte(m0ExtManifest), 0o644)
	_ = os.WriteFile(filepath.Join(extDir, "m.js"), []byte(`window.__m0Ext='main-world';`), 0o644)
	_ = os.WriteFile(filepath.Join(extDir, "bg.js"), []byte(`self.__m0bg=1;`), 0o644)
	{
		e := driver.WithExtraArgs(eng, "--load-extension="+extDir)
		s, err := l.LaunchStockBare(ctx, e, in, prof("ext", 11), "")
		if err != nil {
			fmt.Println("EXT launch failed:", err)
		} else {
			pg, _ := s.WaitPage(ctx)
			var marker string
			if pg != nil && pg.Navigate(ctx, site.URL) == nil {
				_ = pg.Eval(ctx, `window.__m0Ext || 'absent'`, &marker)
			}
			time.Sleep(time.Second)
			var tg struct {
				TargetInfos []struct{ Type, URL string }
			}
			_ = s.Call(ctx, "Target.getTargets", nil, &tg)
			sw := 0
			for _, t := range tg.TargetInfos {
				if strings.HasPrefix(t.URL, "chrome-extension://") && t.Type == "service_worker" {
					sw++
				}
			}
			fmt.Printf("EXT --load-extension: content script MAIN world=%s; extension service workers=%d\n", marker, sw)
			_ = s.Close()
		}
	}
	{
		e := driver.WithExtraArgs(eng, "--enable-unsafe-extension-debugging")
		s, err := l.LaunchStockBare(ctx, e, in, prof("extdbg", 12), "")
		if err != nil {
			fmt.Println("EXT2 launch failed:", err)
		} else {
			cctx, c := context.WithTimeout(ctx, 20*time.Second)
			var res struct{ ID string }
			t0 := time.Now()
			err := s.Call(cctx, "Extensions.loadUnpacked", map[string]string{"path": extDir}, &res)
			c()
			fmt.Printf("EXT Extensions.loadUnpacked after %dms: id=%q err=%v\n", time.Since(t0).Milliseconds(), res.ID, err)
			_ = s.Close()
		}
	}

	// 2. Proxy relay (upstream with basic auth, plain HTTP through it).
	var upHits, upAuth atomic.Int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upHits.Add(1)
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte("u:p"))
		if r.Header.Get("Proxy-Authorization") != want {
			w.Header().Set("Proxy-Authenticate", `Basic realm="m0"`)
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		upAuth.Add(1)
		fmt.Fprint(w, "<!doctype html><title>via-proxy</title><body>via-proxy host="+r.Host+"</body>")
	}))
	defer up.Close()
	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(up.URL, "http://"))
	port, _ := strconv.Atoi(portStr)
	relay, err := proxy.New(profile.Proxy{Scheme: profile.ProxyHTTP, Host: host, Port: port, Username: "u", Password: "p"})
	if err != nil {
		return err
	}
	addr, err := relay.Start(ctx)
	if err != nil {
		return err
	}
	defer relay.Close()
	{
		p := prof("proxy", 13)
		p.Proxy = profile.Proxy{Scheme: profile.ProxyHTTP, Host: host, Port: port}
		a, aerr := eng.Args(p, addr)
		fmt.Printf("PROXY args=%v err=%v\n", a, aerr)
		s, err := l.LaunchStockBare(ctx, eng, in, p, addr)
		if err != nil {
			fmt.Println("PROXY launch failed:", err)
		} else {
			pg, _ := s.WaitPage(ctx)
			var title string
			if pg != nil && pg.Navigate(ctx, "http://m0proxy.test/") == nil {
				_ = pg.Eval(ctx, `document.title`, &title)
			}
			st := relay.Stats()
			fmt.Printf("PROXY page title=%q relay total=%d up=%d down=%d upstream hits=%d authed=%d\n", title, st.TotalConns, st.BytesUp, st.BytesDown, upHits.Load(), upAuth.Load())
			_ = s.Close()
		}
		// Without a relay address the launcher must refuse.
		if _, err := eng.Args(p, ""); err != nil {
			fmt.Printf("PROXY no relay address: %v\n", err)
		}
	}

	// 3. Data dir: layout, persistence across relaunch, lock, same seed same canvas.
	{
		p := prof("persist", 14)
		var h1, h2 string
		s, err := l.LaunchStockBare(ctx, eng, in, p, "")
		if err != nil {
			fmt.Println("DATA launch failed:", err)
			return nil
		}
		pg, _ := s.WaitPage(ctx)
		var seen string
		if pg != nil && pg.Navigate(ctx, site.URL) == nil {
			_ = pg.Eval(ctx, `localStorage.setItem('m0','kept'); document.cookie='m0c=1; max-age=86400'; indexedDB.open('m0db'); 'set'`, &seen)
			time.Sleep(time.Second)
			var o map[string]string
			_ = pg.Eval(ctx, relJS, &o)
			h1 = o["img"]
		}
		// A second launch on the same directory must be refused while it is open.
		_, lerr := l.LaunchStockBare(ctx, eng, in, p, "")
		fmt.Printf("DATA second launch on open dir: %v\n", lerr)
		_ = s.Close()
		time.Sleep(time.Second)
		ents, _ := os.ReadDir(p.DataDir)
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		fmt.Printf("DATA layout of data dir: %v\n", names)
		s2, err := l.LaunchStockBare(ctx, eng, in, p, "")
		if err != nil {
			fmt.Println("DATA relaunch failed:", err)
			return nil
		}
		pg2, _ := s2.WaitPage(ctx)
		var ls, ck string
		if pg2 != nil && pg2.Navigate(ctx, site.URL) == nil {
			_ = pg2.Eval(ctx, `localStorage.getItem('m0')||'lost'`, &ls)
			_ = pg2.Eval(ctx, `document.cookie||'lost'`, &ck)
			time.Sleep(time.Second)
			var o map[string]string
			_ = pg2.Eval(ctx, relJS, &o)
			h2 = o["img"]
		}
		fmt.Printf("DATA after relaunch: localStorage=%s cookie=%s canvas same=%v (%s)\n", ls, ck, h1 == h2 && h1 != "", h1)
		_ = s2.Close()
	}
	return nil
}
