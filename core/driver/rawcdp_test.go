package driver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBrowser answers commands over an in-memory pipe the way Chrome does.
type fakeBrowser struct {
	in  *bufio.Reader
	out *pipeWriter
}

type pipeWriter struct {
	mu sync.Mutex
	w  interface{ Write([]byte) (int, error) }
}

func (p *pipeWriter) send(v any) {
	b, _ := json.Marshal(v)
	p.mu.Lock()
	p.w.Write(append(b, 0))
	p.mu.Unlock()
}

func newPipePair(t *testing.T) (*rawConn, *fakeBrowser, chan [3]string) {
	t.Helper()
	cmdR, cmdW := ioPipe()
	evR, evW := ioPipe()
	events := make(chan [3]string, 8)
	c := newRawConn(evR, cmdW, closeBoth{cmdW, evR}, func(sid, m string, p []byte) { events <- [3]string{sid, m, string(p)} })
	t.Cleanup(func() { c.close() })
	return c, &fakeBrowser{in: bufio.NewReader(cmdR), out: &pipeWriter{w: evW}}, events
}

func TestRawConnFraming(t *testing.T) {
	c, b, events := newPipePair(t)
	big := strings.Repeat("x", 3<<20) // larger than any pipe buffer: framing must reassemble
	go func() {
		for {
			raw, err := b.in.ReadBytes(0)
			if err != nil {
				return
			}
			var m rawMsg
			if err := json.Unmarshal(bytes.TrimSuffix(raw, []byte{0}), &m); err != nil {
				return
			}
			switch m.Method {
			case "Test.echo":
				b.out.send(map[string]any{"id": m.ID, "result": map[string]any{"session": m.SessionID, "big": big}})
			case "Test.fail":
				b.out.send(map[string]any{"id": m.ID, "error": map[string]any{"code": -32000, "message": "nope"}})
			case "Test.event":
				b.out.send(map[string]any{"method": "Test.happened", "sessionId": m.SessionID, "params": map[string]any{"n": 1}})
				b.out.send(map[string]any{"id": m.ID, "result": map[string]any{}})
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var res struct {
		Session string `json:"session"`
		Big     string `json:"big"`
	}
	if err := c.session("S1").Call(ctx, "Test.echo", nil, &res); err != nil {
		t.Fatal(err)
	}
	if res.Session != "S1" || res.Big != big {
		t.Fatalf("echo mismatch: session %q, big len %d", res.Session, len(res.Big))
	}
	err := c.session("").Call(ctx, "Test.fail", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("want cdp error, got %v", err)
	}

	sub, stop := c.session("S2").Subscribe("Test.happened")
	defer stop()
	if err := c.session("S2").Call(ctx, "Test.event", nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case v := <-sub:
		if string(v) != `{"n":1}` {
			t.Fatalf("event payload %s", v)
		}
	case <-ctx.Done():
		t.Fatal("event not delivered")
	}
	if ev := <-events; ev[0] != "S2" || ev[1] != "Test.happened" {
		t.Fatalf("onEvent got %v", ev)
	}
}

func TestRawConnFailsPendingWhenBrowserDies(t *testing.T) {
	c, b, _ := newPipePair(t)
	go func() {
		b.in.ReadBytes(0) // the command arrives, the browser then dies
		c.close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.session("").Call(ctx, "Test.hang", nil, nil); err == nil || ctx.Err() != nil {
		t.Fatalf("want connection error before the deadline, got %v", err)
	}
}
