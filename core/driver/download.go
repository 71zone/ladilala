package driver

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// UnsafeZipError means a zip entry would land outside the target folder (zip
// slip), or a link in the zip points outside it.
type UnsafeZipError struct {
	Name string
	Link bool
}

func (e *UnsafeZipError) Error() string {
	if e.Link {
		return fmt.Sprintf("driver: zip link %q points outside the folder", e.Name)
	}
	return fmt.Sprintf("driver: zip entry %q is outside the folder", e.Name)
}

// Unzip extracts zipPath into dest. It refuses entries that would land
// outside dest (zip slip) and symlinks that point outside it, both as
// *UnsafeZipError. A damaged entry fails with zip.ErrChecksum.
func Unzip(ctx context.Context, zipPath, dest string) (files int, size int64, err error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return 0, 0, err
	}
	defer zr.Close()
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return 0, 0, err
	}
	root := filepath.Clean(dest) + string(os.PathSeparator)
	inside := func(p string) bool { return strings.HasPrefix(filepath.Clean(p)+string(os.PathSeparator), root) }
	for _, f := range zr.File {
		if err := ctx.Err(); err != nil {
			return files, size, err
		}
		target := filepath.Join(dest, filepath.FromSlash(f.Name))
		if !inside(target) {
			return files, size, &UnsafeZipError{Name: f.Name}
		}
		mode := f.Mode()
		switch {
		case f.FileInfo().IsDir():
			if err := os.MkdirAll(target, 0o700); err != nil {
				return files, size, err
			}
		case mode&os.ModeSymlink != 0:
			rc, err := f.Open()
			if err != nil {
				return files, size, err
			}
			link, err := io.ReadAll(io.LimitReader(rc, 4096))
			rc.Close()
			if err != nil {
				return files, size, err
			}
			if !inside(filepath.Join(filepath.Dir(target), string(link))) || filepath.IsAbs(string(link)) {
				return files, size, &UnsafeZipError{Name: f.Name, Link: true}
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return files, size, err
			}
			if err := os.Symlink(string(link), target); err != nil {
				return files, size, err
			}
			files++
		default:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return files, size, err
			}
			n, err := extractZipFile(ctx, f, target, mode.Perm()|0o600)
			size += n
			if err != nil {
				return files, size, err
			}
			files++
		}
	}
	return files, size, nil
}

func extractZipFile(ctx context.Context, f *zip.File, target string, perm os.FileMode) (int64, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, &ctxReader{ctx, rc})
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return n, err
}

// FileSHA256 returns the hex SHA-256 of a file. It stops when ctx ends.
func FileSHA256(ctx context.Context, p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, &ctxReader{ctx, f}); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// DownloadHooks lets the caller watch and steer one download attempt.
type DownloadHooks struct {
	// Progress gets the bytes on disk so far and the total (0 when unknown),
	// after every chunk. It must be cheap; the caller throttles its own output.
	Progress func(done, total int64)
	// Gate runs after every chunk with the bytes written in this attempt. It
	// may block (pause, bandwidth limit); an error aborts the attempt.
	Gate func(ctx context.Context, written int64) error
}

// ErrPartialMismatch means the partial file does not fit the file on the
// server. DownloadFile has deleted it; a new attempt starts clean.
var ErrPartialMismatch = errors.New("driver: partial file does not fit the server file")

// DownloadFile fetches url into part. When part already has bytes it asks for
// the rest with a Range request and appends when the server agrees, or starts
// over when the server sends the whole file. It returns an error unless the
// whole body arrived.
func DownloadFile(ctx context.Context, client *http.Client, url, part string, h DownloadHooks) error {
	if client == nil {
		client = http.DefaultClient
	}
	var off int64
	if st, err := os.Stat(part); err == nil {
		off = st.Size()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if off > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", off))
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	switch resp.StatusCode {
	case http.StatusOK:
		off = 0
		flags |= os.O_TRUNC
	case http.StatusPartialContent:
		flags |= os.O_APPEND
	case http.StatusRequestedRangeNotSatisfiable:
		_ = os.Remove(part)
		return ErrPartialMismatch
	default:
		return fmt.Errorf("download: %s", resp.Status)
	}
	total := int64(0)
	if resp.ContentLength >= 0 {
		total = off + resp.ContentLength
	}
	f, err := os.OpenFile(part, flags, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	progress := func(done int64) {
		if h.Progress != nil {
			h.Progress(done, total)
		}
	}
	progress(off)
	written := int64(0)
	buf := make([]byte, 64*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			written += int64(n)
			progress(off + written)
			if h.Gate != nil {
				if err := h.Gate(ctx, written); err != nil {
					return err
				}
			}
		} else if h.Gate != nil {
			if err := h.Gate(ctx, written); err != nil {
				return err
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	if total > 0 && off+written != total {
		return io.ErrUnexpectedEOF
	}
	progress(off + written)
	return nil
}

// IsDiskFull reports whether err means the disk has no space left.
func IsDiskFull(err error) bool {
	if errors.Is(err, syscall.ENOSPC) {
		return true
	}
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "no space left") || strings.Contains(m, "not enough space")
}
