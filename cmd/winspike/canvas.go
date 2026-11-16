package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/8bu/ladilala/core/driver"
)

// canvasProbes is the experiment matrix from docs/canvas-hang.md.
func canvasProbes() []driver.Probe {
	base := driver.Probe{Page: "http", Nav: true}
	with := func(name string, f func(p *driver.Probe)) driver.Probe {
		p := base
		p.Name = name
		f(&p)
		return p
	}
	return []driver.Probe{
		with("baseline", func(p *driver.Probe) {}),
		with("disable-gpu", func(p *driver.Probe) { p.Extra = []string{"--disable-gpu"} }),
		with("disable-gpu-compositing", func(p *driver.Probe) { p.Extra = []string{"--disable-gpu-compositing"} }),
		with("angle-swiftshader", func(p *driver.Probe) { p.Extra = []string{"--use-angle=swiftshader"} }),
		with("angle-d3d11", func(p *driver.Probe) { p.Extra = []string{"--use-angle=d3d11"} }),
		with("angle-gl", func(p *driver.Probe) { p.Extra = []string{"--use-angle=gl"} }),
		with("page-blank", func(p *driver.Probe) { p.Page = "blank" }),
		with("page-data", func(p *driver.Probe) { p.Page = "data" }),
		with("read-initial-tab", func(p *driver.Probe) { p.Nav = false }),
		with("wait-1s", func(p *driver.Probe) { p.Wait = time.Second }),
		with("wait-3s", func(p *driver.Probe) { p.Wait = 3 * time.Second }),
		with("wait-300ms", func(p *driver.Probe) { p.Wait = 300 * time.Millisecond }),
		with("blank-wait-1s", func(p *driver.Probe) { p.Page = "blank"; p.Wait = time.Second }),
		with("initial-tab-wait-1s", func(p *driver.Probe) { p.Nav = false; p.Wait = time.Second }),
		with("newtab-nowait", func(p *driver.Probe) { p.NewTab = true }),
		with("newtab-wait-1s", func(p *driver.Probe) { p.NewTab = true; p.Wait = time.Second }),
		with("control-no-canvas-spoof", func(p *driver.Probe) { p.Extra = []string{"--disable-spoofing=canvas"} }),
	}
}

type probeRow struct {
	Config   string
	N        int
	Hangs    int
	Leaks    int // first read equals the unspoofed hash
	Unstable int // first read differs from second read
	RelDiff  int // first read differs after relaunch with the same seed
	Tab2Bad  int // second tab read differs from the first tab's, or is real
	Tab2Hang int
	Errors   int
	Real     string
}

func runCanvasMatrix(args []string) error {
	fs := flag.NewFlagSet("canvasmatrix", flag.ExitOnError)
	engineDir := fs.String("enginedir", "", "directory holding the browser build")
	stock := fs.Bool("stock", false, "build is a stock chromium (no fingerprint flags)")
	n := fs.Int("n", 20, "fresh launches per config")
	only := fs.String("configs", "", "comma-separated config names (default all)")
	out := fs.String("out", "", "JSON-lines log file (one line per launch)")
	_ = fs.Parse(args)

	fe := driver.FingerprintChromium{SearchDirs: []string{*engineDir}}
	installs, err := fe.Detect()
	if err != nil || len(installs) == 0 {
		return fmt.Errorf("no browser under %q (%v)", *engineDir, err)
	}
	in := installs[0]
	ver, _ := fe.Version(in)
	l, err := driver.NewLauncher()
	if err != nil {
		return err
	}
	defer l.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html></html>")) }))
	defer srv.Close()
	root, err := os.MkdirTemp("", "canvasmatrix-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)

	var logf *os.File
	if *out != "" {
		if logf, err = os.OpenFile(*out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err != nil {
			return err
		}
		defer logf.Close()
	}
	want := map[string]bool{}
	for _, c := range strings.Split(*only, ",") {
		if c != "" {
			want[c] = true
		}
	}

	ctx := context.Background()
	var rows []probeRow
	for _, pr := range canvasProbes() {
		if len(want) > 0 && !want[pr.Name] {
			continue
		}
		// The unspoofed hash for this exact config (flags change the renderer).
		realPr := pr
		realPr.Extra = append(append([]string{}, pr.Extra...), "--disable-spoofing=canvas")
		realSet := map[string]bool{}
		var realList []string
		for i := 0; i < 8 && len(realList) < 3; i++ {
			r := driver.RunProbe(ctx, l, fe, in, realPr, uint64(1000+i), filepath.Join(root, fmt.Sprintf("%s-real-%d", pr.Name, i)), srv.URL)
			if r.First != "" {
				realList = append(realList, r.First[:4])
				realSet[r.First] = true
			}
		}
		real := strings.Join(realList, ",")
		row := probeRow{Config: pr.Name, N: *n, Real: real}
		for i := range *n {
			seed := uint64(1000 + i)
			r := driver.RunProbe(ctx, l, fe, in, pr, seed, filepath.Join(root, fmt.Sprintf("%s-%d", pr.Name, i)), srv.URL)
			switch {
			case r.Err != nil:
				row.Errors++
			case r.Hung:
				row.Hangs++
			default:
				if realSet[r.First] {
					row.Leaks++
				}
				if r.First != r.Second {
					row.Unstable++
				}
				if r.Relaunch != "" && r.Relaunch != r.First {
					row.RelDiff++
				}
				if r.Tab2Hung {
					row.Tab2Hang++
				} else if r.Tab2 != "" && (r.Tab2 != r.Second || realSet[r.Tab2]) {
					row.Tab2Bad++
				}
			}
			if logf != nil {
				b, _ := json.Marshal(map[string]any{"engine": ver, "config": pr.Name, "seed": seed, "hung": r.Hung,
					"first": r.First, "second": r.Second, "relaunch": r.Relaunch, "tab2": r.Tab2, "tab2hung": r.Tab2Hung, "real": real, "err": fmt.Sprint(r.Err)})
				fmt.Fprintln(logf, string(b))
			}
		}
		rows = append(rows, row)
		fmt.Fprintf(os.Stderr, "done %s hangs=%d leaks=%d\n", pr.Name, row.Hangs, row.Leaks)
	}

	name := ver
	if *stock {
		name += " (stock)"
	}
	fmt.Printf("engine %s\n", name)
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "config\tn\thang\tleak\tunstable\trelaunchdiff\ttab2bad\ttab2hang\terr\treal")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\n", r.Config, r.N, r.Hangs, r.Leaks, r.Unstable, r.RelDiff, r.Tab2Bad, r.Tab2Hang, r.Errors, r.Real)
	}
	return tw.Flush()
}
