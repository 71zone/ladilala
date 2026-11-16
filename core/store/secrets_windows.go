//go:build windows

package store

import (
	"errors"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SecretsBackend names the secret store DefaultSecrets returns.
const SecretsBackend = "windows-dpapi"

// NewDPAPISecrets seals every value with Windows DPAPI (CryptProtectData,
// current user scope) and keeps the sealed blobs in path. Another Windows
// user, or another machine, cannot open them.
func NewDPAPISecrets(path string) Secrets {
	return &fileSecrets{path: path, c: codec{seal: dpapiSeal, open: dpapiOpen}}
}

// DefaultSecrets returns the secret store for the data folder dir.
func DefaultSecrets(dir string) Secrets {
	return NewDPAPISecrets(filepath.Join(dir, "secrets.dpapi.json"))
}

func blob(b []byte) *windows.DataBlob {
	if len(b) == 0 {
		return &windows.DataBlob{}
	}
	return &windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

func takeBlob(b *windows.DataBlob) []byte {
	if b.Data == nil {
		return nil
	}
	out := append([]byte(nil), unsafe.Slice(b.Data, b.Size)...)
	_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(b.Data)))
	return out
}

func dpapiSeal(b []byte) ([]byte, error) {
	var out windows.DataBlob
	if err := windows.CryptProtectData(blob(b), nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	return takeBlob(&out), nil
}

func dpapiOpen(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("store: empty DPAPI blob")
	}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(blob(b), nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	return takeBlob(&out), nil
}
