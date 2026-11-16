package profile

import (
	"math/rand/v2"
	"regexp"
	"strings"
)

// DefaultBrowserVersion matches the newest fingerprint-chromium release the
// app targets. Callers should pass the version of the installed binary so the
// spoofed UA agrees with the engine's real feature set.
const DefaultBrowserVersion = "150.0.7871.186"

const (
	defaultLanguage = "en-US"
	defaultTimezone = "America/New_York"
)

// Options tunes NewFingerprint. Zero values mean "pick the default".
type Options struct {
	Platform       string // "windows" (default), "macos", "linux"
	Language       string // BCP 47; default en-US
	Timezone       string // IANA; default America/New_York
	BrowserVersion string // "150.0.7871.186" or just the major "150"
}

var (
	fullVersionRe  = regexp.MustCompile(`^(\d+)\.\d+\.\d+\.\d+$`)
	majorVersionRe = regexp.MustCompile(`^\d+$`)
)

// normalizeVersion accepts a full four-part version or a bare major and
// returns the full form, or the default when the input is unusable.
func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	switch {
	case fullVersionRe.MatchString(v):
		return v
	case majorVersionRe.MatchString(v):
		return v + ".0.0.0"
	default:
		return DefaultBrowserVersion
	}
}

func majorOf(version string) string {
	m := fullVersionRe.FindStringSubmatch(version)
	if m == nil {
		return ""
	}
	return m[1]
}

// userAgent builds the UA in Chrome's reduced format: the minor/build/patch
// parts are always zero and the OS token is frozen per platform.
func userAgent(platform, brand, major string) string {
	var os string
	switch platform {
	case PlatformMacOS:
		os = "Macintosh; Intel Mac OS X 10_15_7"
	case PlatformLinux:
		os = "X11; Linux x86_64"
	default:
		os = "Windows NT 10.0; Win64; x64"
	}
	ua := "Mozilla/5.0 (" + os + ") AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + major + ".0.0.0 Safari/537.36"
	if brand == "Edge" {
		ua += " Edg/" + major + ".0.0.0"
	}
	return ua
}

// NewFingerprint derives a complete, coherent device from seed. The same seed
// and options always yield the same value. An unknown platform falls back to
// Windows, the production target.
func NewFingerprint(seed uint64, opts Options) Fingerprint {
	platform := strings.ToLower(strings.TrimSpace(opts.Platform))
	if len(classesFor(platform)) == 0 {
		platform = PlatformWindows
	}
	// The second PCG word is a fixed odd constant so seeds 0 and 1 do not
	// produce correlated streams.
	r := rand.New(rand.NewPCG(seed, 0x9e3779b97f4a7c15))

	class := pickOne(r, classesFor(platform))
	version := normalizeVersion(opts.BrowserVersion)
	screen := pickOne(r, class.screens)

	fp := Fingerprint{
		Seed:            seed,
		Platform:        platform,
		PlatformVersion: pickOne(r, platformVersions[platform]),
		Brand:           "Chrome",
		BrowserVersion:  version,
		ScreenWidth:     screen[0],
		ScreenHeight:    screen[1],
		HardwareThreads: pickOne(r, class.threads),
		DeviceMemoryGB:  pickOne(r, class.memory),
		GPUVendor:       class.gpuVendor,
		GPURenderer:     class.gpuName,
		Language:        defaultLanguage,
		Timezone:        defaultTimezone,
		Canvas:          ModeNoise,
		Audio:           ModeNoise,
		WebGL:           ModeNoise,
		Fonts:           ModeNoise,
		ClientRects:     ModeNoise,
		WebRTC:          ModeOff,
	}
	fp.UserAgent = userAgent(platform, fp.Brand, majorOf(version))
	return ApplyGeo(fp, opts.Timezone, opts.Language)
}

func pickOne[T any](r *rand.Rand, s []T) T { return s[r.IntN(len(s))] }

// Shuffle re-rolls the device from newSeed while keeping what the user chose
// on purpose: platform, language, timezone, browser version/brand and the
// per-surface modes.
func Shuffle(fp Fingerprint, newSeed uint64) Fingerprint {
	out := NewFingerprint(newSeed, Options{
		Platform:       fp.Platform,
		Language:       fp.Language,
		Timezone:       fp.Timezone,
		BrowserVersion: fp.BrowserVersion,
	})
	if fp.Brand != "" {
		out.Brand = fp.Brand
		out.UserAgent = userAgent(out.Platform, out.Brand, majorOf(out.BrowserVersion))
	}
	keepMode := func(dst *Mode, src Mode) {
		if src != "" {
			*dst = src
		}
	}
	keepMode(&out.Canvas, fp.Canvas)
	keepMode(&out.Audio, fp.Audio)
	keepMode(&out.WebGL, fp.WebGL)
	keepMode(&out.Fonts, fp.Fonts)
	keepMode(&out.ClientRects, fp.ClientRects)
	keepMode(&out.WebRTC, fp.WebRTC)
	return out
}

// ApplyGeo aligns language and timezone with the proxy exit location. Empty
// arguments leave the existing value untouched.
func ApplyGeo(fp Fingerprint, tz, lang string) Fingerprint {
	if tz = strings.TrimSpace(tz); tz != "" {
		fp.Timezone = tz
	}
	if lang = strings.TrimSpace(lang); lang != "" {
		fp.Language = lang
	}
	return fp
}
