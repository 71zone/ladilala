//go:build !windows

package store

import "path/filepath"

// SecretsBackend names the secret store DefaultSecrets returns. On macOS and
// Linux this is the development file store: NOT encrypted.
const SecretsBackend = "insecure-dev-file"

// DefaultSecrets returns the secret store for the data folder dir. The
// non-Windows build is for development: it writes secrets as plain base64
// to secrets.insecure.json. Do not ship it.
func DefaultSecrets(dir string) Secrets {
	return NewInsecureFileSecrets(filepath.Join(dir, "secrets.insecure.json"))
}
