package engine

import (
	"context"
	"sync"
	"sync/atomic"
)

const subBuffer = 256

type sub struct {
	ch      chan Event
	dropped atomic.Int64
}

// hub fans events out to subscribers. Publishing never blocks: a subscriber
// that does not read loses its oldest events and gets the count in Dropped.
type hub struct {
	mu   sync.Mutex
	subs map[string]map[*sub]struct{}
}

func (h *hub) subscribe(ctx context.Context, runID string) <-chan Event {
	s := &sub{ch: make(chan Event, subBuffer)}
	h.mu.Lock()
	if h.subs == nil {
		h.subs = map[string]map[*sub]struct{}{}
	}
	if h.subs[runID] == nil {
		h.subs[runID] = map[*sub]struct{}{}
	}
	h.subs[runID][s] = struct{}{}
	h.mu.Unlock()
	go func() {
		<-ctx.Done()
		h.mu.Lock()
		delete(h.subs[runID], s)
		if len(h.subs[runID]) == 0 {
			delete(h.subs, runID)
		}
		h.mu.Unlock()
		close(s.ch) // no publisher holds s after the delete above: publish sends under h.mu
	}()
	return s.ch
}

func (h *hub) publish(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs[e.RunID] {
		ev := e
		ev.Dropped = int(s.dropped.Swap(0))
		select {
		case s.ch <- ev:
			continue
		default:
		}
		select { // full: drop the oldest
		case <-s.ch:
			s.dropped.Add(1)
		default:
		}
		ev.Dropped = int(s.dropped.Swap(0))
		select {
		case s.ch <- ev:
		default:
			s.dropped.Add(int64(ev.Dropped) + 1)
		}
	}
}
