//go:build !windows

package driver

// registryInstalls has no registry to read outside Windows.
func registryInstalls() []Install { return nil }
