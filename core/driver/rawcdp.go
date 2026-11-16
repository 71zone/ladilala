package driver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/chromedp/cdproto/cdp"
	jsonv2 "github.com/chromedp/cdproto/cdp/jsonv2"
	"github.com/chromedp/chromedp"
)

// rawConn is a minimal CDP client over the NUL-delimited pipe of
// --remote-debugging-pipe. It exists because chromedp sends Runtime.enable
// when it attaches to a target (chromedp.go, attachTarget), which anti-bot
// scripts detect. rawConn sends exactly the commands its callers send.
//
// Messages are JSON; each ends with one zero byte in both directions.
type rawConn struct {
	r *bufio.Reader
	w io.Writer
	c io.Closer

	wmu    sync.Mutex
	nextID atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan rawMsg
	subs    map[subKey]map[int]chan jsonv2.Value
	subSeq  int
	sent    map[string]int // methods sent, for audits of what a session never sends

	onEvent func(sessionID, method string, params []byte)

	done    chan struct{}
	readErr error
}

type subKey struct{ session, method string }

type rawMsg struct {
	ID        int64           `json:"id,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *rawError       `json:"error,omitempty"`
}

// rawError is a CDP error response.
type rawError struct {
	Code    int64  `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

func (e *rawError) Error() string {
	if e.Data != "" {
		return fmt.Sprintf("cdp error %d: %s (%s)", e.Code, e.Message, e.Data)
	}
	return fmt.Sprintf("cdp error %d: %s", e.Code, e.Message)
}

// newRawConn starts the read loop. r is the browser's output, w its input and
// closer closes both. onEvent runs on the read goroutine and must not block.
func newRawConn(r io.Reader, w io.Writer, closer io.Closer, onEvent func(sessionID, method string, params []byte)) *rawConn {
	c := &rawConn{
		r: bufio.NewReaderSize(r, 1<<20), w: w, c: closer,
		pending: map[int64]chan rawMsg{},
		subs:    map[subKey]map[int]chan jsonv2.Value{},
		sent:    map[string]int{},
		onEvent: onEvent,
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c
}

func (c *rawConn) readLoop() {
	defer close(c.done)
	for {
		b, err := c.r.ReadBytes(0)
		if err != nil {
			c.mu.Lock()
			c.readErr = err
			for id, ch := range c.pending {
				close(ch)
				delete(c.pending, id)
			}
			c.mu.Unlock()
			return
		}
		b = bytes.TrimSuffix(b, []byte{0})
		if len(bytes.TrimSpace(b)) == 0 {
			continue
		}
		var m rawMsg
		if err := json.Unmarshal(b, &m); err != nil {
			continue
		}
		if m.ID != 0 {
			c.mu.Lock()
			ch := c.pending[m.ID]
			delete(c.pending, m.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		c.dispatch(m)
	}
}

func (c *rawConn) dispatch(m rawMsg) {
	c.mu.Lock()
	var targets []chan jsonv2.Value
	for _, ch := range c.subs[subKey{m.SessionID, m.Method}] {
		targets = append(targets, ch)
	}
	c.mu.Unlock()
	for _, ch := range targets {
		select {
		case ch <- jsonv2.Value(m.Params):
		default: // a slow subscriber loses events rather than stalling the read loop
		}
	}
	if c.onEvent != nil {
		c.onEvent(m.SessionID, m.Method, m.Params)
	}
}

// call sends one command and decodes its result into res (nil to ignore).
// An empty sessionID addresses the browser itself.
func (c *rawConn) call(ctx context.Context, sessionID, method string, params, res any) error {
	var raw json.RawMessage = []byte("{}")
	if params != nil {
		b, err := jsonv2.Marshal(params, chromedp.DefaultMarshalOptions)
		if err != nil {
			return fmt.Errorf("cdp: encode %s: %w", method, err)
		}
		raw = b
	}
	id := c.nextID.Add(1)
	ch := make(chan rawMsg, 1)
	c.mu.Lock()
	if c.readErr != nil {
		c.mu.Unlock()
		return c.closedErr()
	}
	c.pending[id] = ch
	c.sent[method]++
	c.mu.Unlock()

	msg, err := json.Marshal(rawMsg{ID: id, SessionID: sessionID, Method: method, Params: raw})
	if err != nil {
		return err
	}
	c.wmu.Lock()
	_, err = c.w.Write(append(msg, 0))
	c.wmu.Unlock()
	if err != nil {
		c.forget(id)
		return fmt.Errorf("cdp: write %s: %w", method, err)
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return c.closedErr()
		}
		if m.Error != nil {
			return fmt.Errorf("%s: %w", method, m.Error)
		}
		if res == nil || len(m.Result) == 0 {
			return nil
		}
		return jsonv2.Unmarshal(m.Result, res, chromedp.DefaultUnmarshalOptions)
	case <-ctx.Done():
		c.forget(id)
		return ctx.Err()
	}
}

// sentMethods returns how often each command was sent.
func (c *rawConn) sentMethods() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int, len(c.sent))
	for k, v := range c.sent {
		out[k] = v
	}
	return out
}

func (c *rawConn) forget(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *rawConn) closedErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.readErr == nil || errors.Is(c.readErr, io.EOF) {
		return errors.New("cdp: browser closed the connection")
	}
	return fmt.Errorf("cdp: connection lost: %w", c.readErr)
}

func (c *rawConn) subscribe(session, method string) (<-chan jsonv2.Value, func()) {
	ch := make(chan jsonv2.Value, 256)
	c.mu.Lock()
	c.subSeq++
	n := c.subSeq
	k := subKey{session, method}
	if c.subs[k] == nil {
		c.subs[k] = map[int]chan jsonv2.Value{}
	}
	c.subs[k][n] = ch
	c.mu.Unlock()
	return ch, func() {
		c.mu.Lock()
		delete(c.subs[k], n)
		c.mu.Unlock()
	}
}

func (c *rawConn) close() error { return c.c.Close() }

// session returns a cdp.Session bound to one CDP session ID ("" = browser).
func (c *rawConn) session(id string) rawSession { return rawSession{c, id} }

// rawSession implements cdp.Session, so typed cdproto commands and the
// emulation steps run on it unchanged.
type rawSession struct {
	c  *rawConn
	id string
}

var _ cdp.Session = rawSession{}

func (s rawSession) Call(ctx context.Context, method string, params, result any) error {
	return s.c.call(ctx, s.id, method, params, result)
}

func (s rawSession) Subscribe(method string) (<-chan jsonv2.Value, func()) {
	return s.c.subscribe(s.id, method)
}
