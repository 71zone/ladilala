package driver

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/8bu/ladilala/core/profile"
)

// ErrCanvasLeak means the canvas read returned the real (unspoofed) image, or
// reads of the same page disagreed, which is how the first-read leak shows.
var ErrCanvasLeak = errors.New("driver: canvas read is not a stable spoofed image")

// MaxVerifyAttempts bounds relaunches in LaunchVerified.
const MaxVerifyAttempts = 4

// SettleDelay is how long LaunchVerified waits after the browser starts before
// the first canvas read. Measured on fingerprint-chromium 148 and 144: reads
// right after launch leak (2-7 of 20), reads after 1 s did not (0 of 40).
const SettleDelay = time.Second

// stableReads is how many identical consecutive reads VerifySpoof requires.
// Observed leaks (docs/canvas-hang.md) always changed value on a later read.
const stableReads = 3

// VerifySpoof reads the canvas stableReads times and returns its hash. It
// returns ErrCanvasHang if any read times out, and ErrCanvasLeak if the reads
// differ or the hash is one of the known real hashes (the unspoofed image of
// this machine, from RealCanvasHash).
func VerifySpoof(s *Session, real ...string) (string, error) {
	var first string
	for i := range stableReads {
		h, err := ReadCanvas(s)
		if err != nil {
			return "", err
		}
		if i == 0 {
			first = h
		} else if h != first {
			return "", fmt.Errorf("%w: %s then %s", ErrCanvasLeak, first, h)
		}
	}
	if slices.Contains(real, first) {
		return "", fmt.Errorf("%w: %s is the real canvas", ErrCanvasLeak, first)
	}
	return first, nil
}

// VerifyResult describes how LaunchVerified got its session.
type VerifyResult struct {
	Attempts int
	Hangs    int // attempts lost to a canvas read timeout
	Leaks    int // attempts lost to an unstable or real canvas read
	Hash     string
}

// LaunchVerified launches p and checks the canvas with VerifySpoof. A browser
// that hangs or leaks is closed and relaunched, at most MaxVerifyAttempts
// times in total. It never returns a session that failed verification.
func (l *Launcher) LaunchVerified(ctx context.Context, engine BrowserEngine, install Install, p profile.Profile, proxyAddr string, real ...string) (*Session, VerifyResult, error) {
	var res VerifyResult
	var last error
	for res.Attempts < MaxVerifyAttempts {
		res.Attempts++
		s, err := l.Launch(ctx, engine, install, p, proxyAddr)
		if err != nil {
			return nil, res, err
		}
		select {
		case <-time.After(SettleDelay):
		case <-ctx.Done():
			_ = s.Close()
			return nil, res, ctx.Err()
		}
		h, err := VerifySpoof(s, real...)
		if err == nil {
			res.Hash = h
			return s, res, nil
		}
		_ = s.Close()
		last = err
		switch {
		case errors.Is(err, ErrCanvasHang):
			res.Hangs++
		case errors.Is(err, ErrCanvasLeak):
			res.Leaks++
		default:
			return nil, res, err
		}
		if ctx.Err() != nil {
			return nil, res, ctx.Err()
		}
	}
	return nil, res, fmt.Errorf("driver: canvas not verified after %d launches: %w", res.Attempts, last)
}
