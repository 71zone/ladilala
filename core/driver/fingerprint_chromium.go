package driver

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/8bu/ladilala/core/profile"
)

// FingerprintChromium is the adryfish/fingerprint-chromium build of
// ungoogled-chromium. It spoofs the fingerprint inside the engine, driven by
// command-line flags documented in its README.
//
// A stock Chromium ignores these flags without warning, so Detect only looks
// in SearchDirs (the directories where the app unpacks its own engine
// download) and never in system locations.
type FingerprintChromium struct {
	SearchDirs []string
}

func (FingerprintChromium) Name() string { return "fingerprint-chromium" }

func (e FingerprintChromium) Detect() ([]Install, error) {
	var found []Install
	for _, dir := range e.SearchDirs {
		paths, err := scanDir(dir, binaryNames(hostOS), 4)
		if err != nil {
			return nil, err
		}
		for _, p := range paths {
			found = append(found, Install{Path: p, Brand: BrandChromium})
		}
	}
	return dedupe(found), nil
}

func (FingerprintChromium) Version(i Install) (string, error) { return binaryVersion(i.Path) }

// Args maps the fingerprint to README-documented flags. Fields the engine
// cannot set by flag (screen size, device memory, GPU strings) are left to
// ScreenPlan and to the engine's own seed-derived values.
func (FingerprintChromium) Args(p profile.Profile, proxyAddr string) ([]string, error) {
	fp := p.Fingerprint
	if fp.Seed == 0 {
		return nil, ErrNoFingerprintSeed
	}
	if err := requireProxy(p, proxyAddr); err != nil {
		return nil, err
	}

	// The engine takes a 32-bit seed.
	args := []string{"--fingerprint=" + strconv.FormatUint(uint64(uint32(fp.Seed)), 10)}

	if fp.Platform != "" {
		switch fp.Platform {
		case profile.PlatformWindows, profile.PlatformMacOS, profile.PlatformLinux:
			args = append(args, "--fingerprint-platform="+fp.Platform)
		default:
			return nil, fmt.Errorf("driver: unsupported fingerprint platform %q", fp.Platform)
		}
	}
	args = appendIf(args, "--fingerprint-platform-version=", fp.PlatformVersion)
	args = appendIf(args, "--fingerprint-brand=", fp.Brand)
	args = appendIf(args, "--fingerprint-brand-version=", fp.BrowserVersion)
	if fp.HardwareThreads > 0 {
		args = append(args, "--fingerprint-hardware-concurrency="+strconv.Itoa(fp.HardwareThreads))
	}
	args = appendIf(args, "--timezone=", fp.Timezone)
	args = appendIf(args, "--lang=", fp.Language)
	args = appendIf(args, "--accept-lang=", acceptLanguages(fp.Language))

	// Real and Off both stop the engine from perturbing the surface: it has
	// no switch that blocks a surface, only one that disables its spoofing.
	var off []string
	for _, s := range []struct {
		value string
		mode  profile.Mode
	}{
		{"canvas", fp.Canvas},
		{"audio", fp.Audio},
		{"font", fp.Fonts},
		{"clientrects", fp.ClientRects},
		{"gpu", fp.WebGL},
	} {
		if s.mode == profile.ModeReal || s.mode == profile.ModeOff {
			off = append(off, s.value)
		}
	}
	if len(off) > 0 {
		args = append(args, "--disable-spoofing="+strings.Join(off, ","))
	}

	if fp.WebRTC == profile.ModeOff {
		// Chromium's own switch backs the policy; --disable-non-proxied-udp is
		// the engine's documented equivalent.
		args = append(args,
			"--force-webrtc-ip-handling-policy=disable_non_proxied_udp",
			"--disable-non-proxied-udp",
		)
	}

	proxy, err := proxyFlag(proxyAddr)
	if err != nil {
		return nil, err
	}
	return append(args, proxy...), nil
}

func appendIf(args []string, prefix, value string) []string {
	if value == "" {
		return args
	}
	return append(args, prefix+value)
}

// acceptLanguages turns "en-US" into "en-US,en", the list Chrome sends by
// default for a regional locale.
func acceptLanguages(lang string) string {
	base, _, ok := strings.Cut(lang, "-")
	if !ok || base == "" {
		return lang
	}
	return lang + "," + base
}
