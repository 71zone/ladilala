package profile

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	uaChromeRe  = regexp.MustCompile(`Chrome/(\d+)\.`)
	languageRe  = regexp.MustCompile(`^[a-z]{2,3}(-[A-Z][a-z]{3})?(-[A-Z]{2}|-[0-9]{3})?$`)
	timezoneRe  = regexp.MustCompile(`^(UTC|[A-Za-z]+(/[A-Za-z0-9_+-]+)+)$`)
	knownBrands = []string{"Chrome", "Chromium", "Edge"}
)

// Validate returns one message per incoherence found; an empty result means
// the fingerprint is a plausible single device.
func Validate(fp Fingerprint) []string {
	var issues []string
	add := func(format string, a ...any) { issues = append(issues, fmt.Sprintf(format, a...)) }

	validatePlatformGPU(fp, add)
	validateBrowser(fp, add)

	if fp.ScreenWidth <= 0 || fp.ScreenHeight <= 0 {
		add("screen %dx%d is not positive", fp.ScreenWidth, fp.ScreenHeight)
	}
	if !containsInt(validDeviceMemory, fp.DeviceMemoryGB) {
		add("deviceMemoryGb %d is not a value Chrome reports (1, 2, 4, 8)", fp.DeviceMemoryGB)
	}
	if fp.HardwareThreads < 1 {
		add("hardwareThreads %d is not positive", fp.HardwareThreads)
	}
	if !languageRe.MatchString(fp.Language) {
		add("language %q is not a BCP 47 tag", fp.Language)
	}
	if !timezoneRe.MatchString(fp.Timezone) {
		add("timezone %q is not an IANA name", fp.Timezone)
	}
	for _, nm := range []struct {
		name string
		mode Mode
	}{
		{"canvas", fp.Canvas}, {"audio", fp.Audio}, {"webgl", fp.WebGL},
		{"fonts", fp.Fonts}, {"clientRects", fp.ClientRects}, {"webrtc", fp.WebRTC},
	} {
		switch nm.mode {
		case ModeNoise, ModeReal, ModeOff:
		default:
			add("%s mode %q is not noise, real or off", nm.name, nm.mode)
		}
	}
	return issues
}

func validatePlatformGPU(fp Fingerprint, add func(string, ...any)) {
	if len(classesFor(fp.Platform)) == 0 {
		add("platform %q is not windows, macos or linux", fp.Platform)
		return
	}
	if !containsStr(platformVersions[fp.Platform], fp.PlatformVersion) {
		add("platformVersion %q does not exist on %s", fp.PlatformVersion, fp.Platform)
	}

	// Cross-OS API tells are the most common incoherence, so name them
	// explicitly before falling back to the generic table lookup.
	r := fp.GPURenderer
	switch {
	case strings.Contains(r, "Direct3D") && fp.Platform != PlatformWindows:
		add("GPU renderer uses Direct3D, which only exists on windows, not %s", fp.Platform)
		return
	case strings.Contains(r, "Metal") && fp.Platform != PlatformMacOS:
		add("GPU renderer uses Metal, which only exists on macos, not %s", fp.Platform)
		return
	case strings.Contains(r, "OpenGL") && fp.Platform != PlatformLinux:
		add("GPU renderer uses OpenGL, which Chrome only reports on linux, not %s", fp.Platform)
		return
	}

	class, ok := findClass(fp.Platform, r)
	if !ok {
		add("GPU renderer %q is not a known %s device", r, fp.Platform)
		return
	}
	if fp.GPUVendor != class.gpuVendor {
		add("GPU vendor %q does not match renderer (want %q)", fp.GPUVendor, class.gpuVendor)
	}
	if !containsInt(class.threads, fp.HardwareThreads) {
		add("%d hardware threads is not shipped with %s (allowed %v)", fp.HardwareThreads, r, class.threads)
	}
	if !containsInt(class.memory, fp.DeviceMemoryGB) {
		kind := "discrete"
		if class.integrated {
			kind = "integrated"
		}
		add("%d GB memory is not shipped with %s GPU %s (allowed %v)", fp.DeviceMemoryGB, kind, r, class.memory)
	}
	if !containsScreen(class.screens, fp.ScreenWidth, fp.ScreenHeight) {
		add("screen %dx%d is not shipped with %s (allowed %v)", fp.ScreenWidth, fp.ScreenHeight, r, class.screens)
	}
}

func validateBrowser(fp Fingerprint, add func(string, ...any)) {
	if !containsStr(knownBrands, fp.Brand) {
		add("brand %q is not Chrome, Chromium or Edge", fp.Brand)
	}
	major := majorOf(fp.BrowserVersion)
	if major == "" {
		add("browserVersion %q is not a four-part version", fp.BrowserVersion)
		return
	}
	m := uaChromeRe.FindStringSubmatch(fp.UserAgent)
	if m == nil {
		add("userAgent has no Chrome/<major> token")
		return
	}
	if m[1] != major {
		add("userAgent major %s differs from browserVersion major %s", m[1], major)
		return
	}
	if want := userAgent(fp.Platform, fp.Brand, major); fp.UserAgent != want {
		add("userAgent does not match the reduced Chrome format for %s/%s (want %q)", fp.Platform, fp.Brand, want)
	}
}

func containsScreen(s [][2]int, w, h int) bool {
	for _, x := range s {
		if x[0] == w && x[1] == h {
			return true
		}
	}
	return false
}
