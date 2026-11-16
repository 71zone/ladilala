package driver

import "golang.org/x/sys/windows/registry"

// registryInstalls reads the App Paths entries that installers register for
// chrome.exe and msedge.exe. They find installs in non-standard folders that
// the Program Files probe misses. Chrome comes before Edge.
func registryInstalls() []Install {
	const base = `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\`
	var out []Install
	for _, app := range []struct{ exe, brand string }{{"chrome.exe", BrandChrome}, {"msedge.exe", BrandEdge}} {
		for _, loc := range []struct {
			root   registry.Key
			access uint32
		}{
			{registry.LOCAL_MACHINE, registry.QUERY_VALUE | registry.WOW64_64KEY},
			{registry.LOCAL_MACHINE, registry.QUERY_VALUE | registry.WOW64_32KEY},
			{registry.CURRENT_USER, registry.QUERY_VALUE},
		} {
			k, err := registry.OpenKey(loc.root, base+app.exe, loc.access)
			if err != nil {
				continue
			}
			v, _, err := k.GetStringValue("")
			k.Close()
			if err != nil || v == "" {
				continue
			}
			if exp, err := registry.ExpandString(v); err == nil {
				v = exp
			}
			out = append(out, Install{Path: v, Brand: app.brand})
		}
	}
	return out
}
