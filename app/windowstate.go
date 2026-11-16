package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const (
	defaultW = 1280
	defaultH = 800
)

// windowState is the saved window geometry. X and Y are the normal (not
// maximised) position; Placed is false until a position was saved or accepted.
type windowState struct {
	X, Y, W, H int
	Maximised  bool
	Placed     bool

	path string
	mu   sync.Mutex
	save *time.Timer
}

func loadWindowState(app *application.App, dir string) *windowState {
	s := &windowState{W: defaultW, H: defaultH, path: filepath.Join(dir, "window.json")}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return s
	}
	var saved struct {
		X, Y, W, H int
		Maximised  bool
	}
	if json.Unmarshal(b, &saved) != nil || saved.W < 400 || saved.H < 300 {
		return s
	}
	s.W, s.H, s.Maximised = saved.W, saved.H, saved.Maximised
	// A saved position on a monitor that is gone would open the window off
	// screen: accept it only when its title bar lands on a current screen.
	title := application.Rect{X: saved.X, Y: saved.Y, Width: saved.W, Height: 40}
	for _, sc := range app.Screen.GetAll() {
		if overlaps(title, sc.Bounds) {
			s.X, s.Y, s.Placed = saved.X, saved.Y, true
			break
		}
	}
	return s
}

func overlaps(a, b application.Rect) bool {
	return a.X < b.X+b.Width && b.X < a.X+a.Width && a.Y < b.Y+b.Height && b.Y < a.Y+a.Height
}

// track saves the geometry 500 ms after the last move or resize, and on close.
func (s *windowState) track(win *application.WebviewWindow) {
	schedule := func(*application.WindowEvent) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.save != nil {
			s.save.Stop()
		}
		s.save = time.AfterFunc(500*time.Millisecond, func() { s.write(win) })
	}
	win.OnWindowEvent(events.Common.WindowDidMove, schedule)
	win.OnWindowEvent(events.Common.WindowDidResize, schedule)
	win.OnWindowEvent(events.Common.WindowMaximise, schedule)
	win.OnWindowEvent(events.Common.WindowRestore, schedule)
	win.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) { s.write(win) })
}

func (s *windowState) write(win *application.WebviewWindow) {
	max := win.IsMaximised()
	cur := struct {
		X, Y, W, H int
		Maximised  bool
	}{Maximised: max}
	if max {
		// Keep the last normal geometry so un-maximising restores it.
		b, err := os.ReadFile(s.path)
		if err == nil {
			_ = json.Unmarshal(b, &cur)
		}
		cur.Maximised = true
	} else {
		cur.X, cur.Y = win.Position()
		cur.W, cur.H = win.Size()
	}
	if cur.W == 0 {
		return
	}
	if b, err := json.Marshal(cur); err == nil {
		tmp := s.path + ".tmp"
		if os.WriteFile(tmp, b, 0o600) == nil {
			_ = os.Rename(tmp, s.path)
		}
	}
}
