//go:build !windows

package driver

// debuggingBlockedByPolicy is always false outside Windows: the product only
// ships on Windows, and macOS/Linux managed policy surfaces as a failed
// Preflight instead.
func debuggingBlockedByPolicy(brand string) bool { return false }
