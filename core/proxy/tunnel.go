package proxy

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// tunnel shares one idle deadline between both ends: traffic in either
// direction keeps the pair alive, so a one-way download is not cut off.
type tunnel struct {
	a, b net.Conn
	idle time.Duration
}

func newTunnel(a, b net.Conn, idle time.Duration) *tunnel {
	t := &tunnel{a: a, b: b, idle: idle}
	t.touch()
	return t
}

func (t *tunnel) touch() {
	d := time.Now().Add(t.idle)
	t.a.SetDeadline(d)
	t.b.SetDeadline(d)
}

var bufPool = sync.Pool{New: func() any { b := make([]byte, 32<<10); return &b }}

func (r *Relay) copyCounted(t *tunnel, dst net.Conn, src io.Reader, ctr *atomic.Int64) error {
	bp := bufPool.Get().(*[]byte)
	defer bufPool.Put(bp)
	buf := *bp
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			t.touch()
			w, werr := dst.Write(buf[:n])
			ctr.Add(int64(w))
			if werr != nil {
				return werr
			}
			t.touch()
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return nil
			}
			return rerr
		}
	}
}

// pipe copies both directions until each side finishes. A clean EOF becomes a
// half-close so in-flight data in the other direction still arrives; an error
// (reset, idle timeout) closes both ends.
func (r *Relay) pipe(client net.Conn, clientR io.Reader, up net.Conn) {
	t := newTunnel(client, up, r.idle)
	var wg sync.WaitGroup
	wg.Add(2)
	run := func(dst net.Conn, src io.Reader, ctr *atomic.Int64, other net.Conn) {
		defer wg.Done()
		if err := r.copyCounted(t, dst, src, ctr); err != nil {
			dst.Close()
			other.Close()
			return
		}
		closeWrite(dst)
	}
	go run(up, clientR, &r.up, client)
	go run(client, up, &r.down, up)
	wg.Wait()
}

func closeWrite(c net.Conn) error {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return c.Close()
}
