package recorder

import (
	"embed"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

//go:embed testdata
var testPages embed.FS

// SinkEntry is one form submission the test pages report back to the server.
type SinkEntry struct {
	Page string
	Data map[string]any
}

// Site serves the static test pages (testdata/) on two origins: the main one
// on 127.0.0.1 and a cross-origin one on localhost, so that iframes from the
// second origin run in their own process. Form pages post their submitted
// values to /sink, which tests read to check what a replay did.
type Site struct {
	Main  *httptest.Server
	Cross *httptest.Server

	mu    sync.Mutex
	sinks []SinkEntry
}

// NewSite starts both servers. Call Close when done.
func NewSite() (*Site, error) {
	s := &Site{}
	s.Main = httptest.NewServer(http.HandlerFunc(s.serve))
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		s.Main.Close()
		return nil, err
	}
	s.Cross = httptest.NewUnstartedServer(http.HandlerFunc(s.serve))
	s.Cross.Listener.Close()
	s.Cross.Listener = l
	s.Cross.Start()
	return s, nil
}

// Close stops both servers.
func (s *Site) Close() {
	s.Main.Close()
	s.Cross.Close()
}

// URL returns the main-origin URL of a page.
func (s *Site) URL(page string) string { return s.Main.URL + "/" + page }

// CrossOrigin is the origin of the second server, spelled with the host
// "localhost" so that it is a different site from the main origin.
func (s *Site) CrossOrigin() string {
	_, port, _ := net.SplitHostPort(s.Cross.Listener.Addr().String())
	return "http://localhost:" + port
}

// Sinks returns the submissions of one page, oldest first.
func (s *Site) Sinks(page string) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []map[string]any
	for _, e := range s.sinks {
		if e.Page == page {
			out = append(out, e.Data)
		}
	}
	return out
}

// ResetSinks forgets all submissions.
func (s *Site) ResetSinks() {
	s.mu.Lock()
	s.sinks = nil
	s.mu.Unlock()
}

func (s *Site) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/sink" {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var e SinkEntry
		var raw struct {
			Page string         `json:"page"`
			Data map[string]any `json:"data"`
		}
		if json.Unmarshal(b, &raw) == nil {
			e.Page, e.Data = raw.Page, raw.Data
			s.mu.Lock()
			s.sinks = append(s.sinks, e)
			s.mu.Unlock()
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.URL.Path == "/redir" { // a 302 hop, like the redirect of a search engine
		http.Redirect(w, r, "/results.html?"+r.URL.RawQuery, http.StatusFound)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		name = "form.html"
	}
	b, err := testPages.ReadFile("testdata/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	body := strings.NewReplacer("@SINK@", s.Main.URL+"/sink", "@CROSS@", s.CrossOrigin()).Replace(string(b))
	if strings.HasSuffix(name, ".js") {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "no-store")
	io.WriteString(w, body)
}
