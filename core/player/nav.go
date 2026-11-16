package player

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	jsonv2 "github.com/chromedp/cdproto/cdp/jsonv2"
)

// navSettle is the longest the player waits, after an action returned, for the
// browser to announce a navigation the action caused. The announcement
// (Page.frameRequestedNavigation) is sent while the page handles the input, so
// it is normally already queued when the action's command returns; this cap
// only covers a late event. It replaces a fixed 400 ms wait.
const navSettle = 40 * time.Millisecond

// subFrameNavWait bounds the wait for a navigation of a frame inside the tab.
const subFrameNavWait = 5 * time.Second

// navWatch sees whether an action started a navigation and when it ended.
type navWatch struct {
	p                           *Player
	reqs, life, within, stopped <-chan jsonv2.Value
	stops                       []func()
}

// watchNav subscribes before the action so no event is missed. Call stop when done.
func (p *Player) watchNav() *navWatch {
	w := &navWatch{p: p}
	sub := func(method string) <-chan jsonv2.Value {
		ch, stop := p.Page.Subscribe(method)
		w.stops = append(w.stops, stop)
		return ch
	}
	w.reqs = sub("Page.frameRequestedNavigation")
	w.life = sub("Page.lifecycleEvent")
	w.within = sub("Page.navigatedWithinDocument")
	w.stopped = sub("Page.frameStoppedLoading")
	return w
}

func (w *navWatch) stop() {
	for _, s := range w.stops {
		s()
	}
}

func frameOf(v jsonv2.Value) string {
	var ev struct {
		FrameID string `json:"frameId"`
		Name    string `json:"name"`
	}
	_ = json.Unmarshal(v, &ev)
	if ev.Name != "" && ev.Name != "load" {
		return ""
	}
	return ev.FrameID
}

// wait returns once the action has no navigation pending: at once when the
// browser announced none, else when the requested frame has loaded, finished
// loading or changed its URL within the document.
func (w *navWatch) wait(ctx context.Context) error {
	// A cheap round trip to the page: the browser handles the page's reply
	// after the navigation request the input caused.
	_ = w.p.Page.Call(ctx, "Runtime.evaluate", map[string]any{"expression": "0"}, nil)
	var frame string
	select {
	case v := <-w.reqs:
		var ev struct {
			FrameID string `json:"frameId"`
		}
		_ = json.Unmarshal(v, &ev)
		frame = ev.FrameID
	case <-time.After(navSettle):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
	limit, mainFrame := subFrameNavWait, frame == w.p.Page.TargetID
	if mainFrame {
		limit = navTimeout
	}
	timeout := time.After(limit)
	for {
		select {
		case v := <-w.life:
			if frameOf(v) == frame {
				return nil
			}
		case v := <-w.within:
			if frameOf(v) == frame {
				return nil
			}
		case v := <-w.stopped:
			if frameOf(v) == frame {
				return nil
			}
		case <-timeout:
			if mainFrame {
				return errors.New("page did not finish loading")
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
