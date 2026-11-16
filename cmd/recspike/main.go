// Command recspike is the harness of docs/recorder-spike.md.
//
//	recspike record -url URL -out macro.json     record by hand until Stop in the overlay
//	recspike replay -macro macro.json -row a=1,b=2   replay for one data row
//	recspike detect [-engine chrome|edge] [-only sannysoft,rebrowser]   detector pages with the overlay on
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/8bu/ladilala/core/macro"
	"github.com/8bu/ladilala/core/player"
	"github.com/8bu/ladilala/core/recorder"
	"github.com/8bu/ladilala/core/recorder/rectest"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: recspike record|replay|detect [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "record":
		err = runRecord(os.Args[2:])
	case "replay":
		err = runReplay(os.Args[2:])
	case "detect":
		err = runDetect(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "recspike:", err)
		os.Exit(1)
	}
}

func runRecord(args []string) error {
	fs := flag.NewFlagSet("record", flag.ExitOnError)
	url := fs.String("url", "", "start URL")
	out := fs.String("out", "macro.json", "macro file to write")
	engine := fs.String("engine", "chrome", "chrome | edge")
	limit := fs.Duration("limit", 10*time.Minute, "stop after this long")
	fs.Parse(args)
	if *url == "" {
		return fmt.Errorf("-url is required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	rec := recorder.New(recorder.Options{ID: "recording", Name: "Recording"})
	b, err := rectest.Launch(ctx, *engine, uint64(time.Now().UnixNano()), rec.Hook)
	if err != nil {
		return err
	}
	defer b.Close()
	pg, err := b.Session.WaitPage(ctx)
	if err != nil {
		return err
	}
	if err := pg.Navigate(ctx, *url); err != nil {
		return err
	}
	fmt.Println("recording; press Stop in the overlay (top right) or Ctrl-C")
	select {
	case <-rec.Done():
	case <-ctx.Done():
	case <-time.After(*limit):
	}
	rec.Stop()
	m, err := rec.Macro()
	if err != nil {
		return fmt.Errorf("recorded macro is invalid: %w", err)
	}
	raw, err := macro.Marshal(m)
	if err != nil {
		return err
	}
	st := rec.Stats()
	fmt.Printf("%d steps, gaps %v, errors %v\n", len(m.Steps), st.Gaps, rec.Errors())
	return os.WriteFile(*out, raw, 0o644)
}

type rowFlag map[string]string

func (r rowFlag) String() string { return fmt.Sprint(map[string]string(r)) }
func (r rowFlag) Set(s string) error {
	for _, kv := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("row entry %q is not key=value", kv)
		}
		r[strings.TrimSpace(k)] = v
	}
	return nil
}

func runReplay(args []string) error {
	fs := flag.NewFlagSet("replay", flag.ExitOnError)
	file := fs.String("macro", "", "macro file")
	engine := fs.String("engine", "chrome", "chrome | edge")
	row := rowFlag{}
	fs.Var(row, "row", "data row as k=v,k=v")
	fs.Parse(args)
	f, err := os.Open(*file)
	if err != nil {
		return err
	}
	m, err := macro.Load(f)
	f.Close()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	b, err := rectest.Launch(ctx, *engine, uint64(time.Now().UnixNano()), nil)
	if err != nil {
		return err
	}
	defer b.Close()
	pg, err := b.Session.WaitPage(ctx)
	if err != nil {
		return err
	}
	p := player.New(b.Session, pg)
	p.Log = func(f string, a ...any) { fmt.Printf(f+"\n", a...) }
	return p.Run(ctx, m, row)
}

type detector struct {
	name, url, until string
	min, max         time.Duration
}

var detectors = []detector{
	{"sannysoft", "https://bot.sannysoft.com/", "Fingerprint Scanner", 6 * time.Second, 40 * time.Second},
	{"rebrowser", "https://bot-detector.rebrowser.net/", "runtimeEnableLeak", 10 * time.Second, 60 * time.Second},
	{"creepjs", "https://abrahamjuliot.github.io/creepjs/", "trust score", 25 * time.Second, 90 * time.Second},
	{"browserscan", "https://www.browserscan.net/bot-detection", "Bot Detection", 15 * time.Second, 60 * time.Second},
}

func runDetect(args []string) error {
	fs := flag.NewFlagSet("detect", flag.ExitOnError)
	engine := fs.String("engine", "chrome", "chrome | edge")
	only := fs.String("only", "sannysoft,rebrowser", "comma list of detectors")
	out := fs.String("out", "recspike-out", "directory for page texts")
	overlay := fs.Bool("overlay", true, "attach the recorder overlay (false = control)")
	fs.Parse(args)
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	rec := recorder.New(recorder.Options{ID: "detect"})
	hook := rec.Hook
	if !*overlay {
		hook = nil
	}
	b, err := rectest.Launch(ctx, *engine, 7, hook)
	if err != nil {
		return err
	}
	defer b.Close()
	fmt.Printf("ENGINE %s %s overlay=%v\n", *engine, b.Install.Path, *overlay)
	for _, d := range detectors {
		if !strings.Contains(*only, d.name) {
			continue
		}
		if err := runOne(ctx, b, d, *out, *engine, *overlay); err != nil {
			fmt.Printf("DETECTOR %s error: %v\n", d.name, err)
		}
	}
	if *overlay {
		st := rec.Stats()
		fmt.Printf("OVERLAY mounts=%d overlayMs=%v hookMs=%v errors=%v\n", len(st.OverlayMs), st.OverlayMs, st.HookMs, rec.Errors())
	}
	sent := b.Session.SentMethods()
	fmt.Printf("RUNTIME.ENABLE sent=%d\n", sent["Runtime.enable"])
	return nil
}

func runOne(ctx context.Context, b *rectest.Browser, d detector, out, engine string, overlay bool) error {
	dctx, cancel := context.WithTimeout(ctx, d.max+40*time.Second)
	defer cancel()
	pg, err := b.Session.NewPage(dctx, "about:blank")
	if err != nil {
		return err
	}
	nctx, ncancel := context.WithTimeout(dctx, 30*time.Second)
	navErr := pg.Navigate(nctx, d.url)
	ncancel()
	u := rectest.NewUser(dctx, b.Session, pg)
	for _, x := range []float64{200, 400, 600} { // hover: the highlight code runs
		_ = u.MoveTo(x, 200)
	}
	start := time.Now()
	var text string
	stable := 0
	for time.Since(start) < d.max {
		time.Sleep(time.Second)
		var cur string
		if err := pg.Eval(dctx, `document.body ? document.body.innerText : ''`, &cur); err != nil {
			continue
		}
		if len(cur) == len(text) {
			stable++
		} else {
			stable = 0
		}
		text = cur
		if time.Since(start) >= d.min && strings.Contains(strings.ToLower(text), strings.ToLower(d.until)) && stable >= 4 {
			break
		}
	}
	summary := ""
	switch d.name {
	case "sannysoft":
		var n struct{ Failed, Passed int }
		_ = pg.Eval(dctx, `({Failed: document.querySelectorAll('td.failed').length, Passed: document.querySelectorAll('td.passed').length})`, &n)
		summary = fmt.Sprintf("failed=%d passed=%d", n.Failed, n.Passed)
	case "rebrowser":
		var js string
		if pg.Eval(dctx, `JSON.stringify((window.detections||[]).map(d => ({type: d.type, rating: d.rating})))`, &js) == nil {
			var ds []struct{ Type, Rating any }
			_ = json.Unmarshal([]byte(js), &ds)
			var parts []string
			for _, d := range ds {
				parts = append(parts, fmt.Sprintf("%v=%v", d.Type, d.Rating))
			}
			summary = strings.Join(parts, " ")
		}
		text += "\n--JSON--\n" + js
	case "creepjs":
		var js string
		if pg.Eval(dctx, `JSON.stringify({lies: (window.Fingerprint && window.Fingerprint.lies) || null, trust: (window.Fingerprint && window.Fingerprint.trustScore) || null})`, &js) == nil {
			summary = js
			text += "\n--JSON--\n" + js
		}
	}
	var host string
	_ = pg.Eval(dctx, `Array.from(document.documentElement.children).map((e) => e.localName).filter((n) => n.startsWith('ladi-')).join(',')`, &host)
	file := fmt.Sprintf("%s/%s-overlay%v-%s.txt", out, engine, overlay, d.name)
	_ = os.WriteFile(file, []byte(text), 0o644)
	fmt.Printf("DETECTOR %s chars=%d navErr=%v overlayHostInHtml=%q %s file=%s\n", d.name, len(text), navErr, host, summary, file)
	return nil
}
