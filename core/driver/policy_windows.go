package driver

import "golang.org/x/sys/windows/registry"

// debuggingBlockedByPolicy reports whether the RemoteDebuggingAllowed policy
// is set to 0 for the browser, machine-wide or per user.
func debuggingBlockedByPolicy(brand string) bool {
	var key string
	switch brand {
	case BrandEdge:
		key = `SOFTWARE\Policies\Microsoft\Edge`
	case BrandChromium:
		key = `SOFTWARE\Policies\Chromium`
	default:
		key = `SOFTWARE\Policies\Google\Chrome`
	}
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		k, err := registry.OpenKey(root, key, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		v, _, err := k.GetIntegerValue("RemoteDebuggingAllowed")
		k.Close()
		if err == nil && v == 0 {
			return true
		}
	}
	return false
}
