//go:build !windows

package driver

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"time"
)

var versionRe = regexp.MustCompile(`\d+\.\d+\.\d+\.\d+`)

// binaryVersion runs "<binary> --version" and extracts the four-part version.
func binaryVersion(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("driver: %s --version: %w", path, err)
	}
	v := versionRe.FindString(string(out))
	if v == "" {
		return "", fmt.Errorf("driver: no version in %q", out)
	}
	return v, nil
}
