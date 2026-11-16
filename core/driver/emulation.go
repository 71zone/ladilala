package driver

import (
	"context"
	"strings"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"

	"github.com/8bu/ladilala/core/profile"
)

// EmulationPlan returns the CDP steps that give a stock browser (SystemChrome)
// the profile's identity: user agent with client hints, navigator.platform,
// timezone, locale, hardware threads, and screen size.
//
// CDP emulation is per target, so run the plan on every tab and popup the
// profile opens, not only the first. Not applied because CDP has no command
// for them: DeviceMemoryGB, GPUVendor/GPURenderer, and the canvas, audio,
// webgl, fonts, clientRects and webrtc modes.
func EmulationPlan(p profile.Profile) []chromedp.Action[chromedp.Void] {
	return asActions(EmulationSteps(p))
}

// EmulationStep is one emulation command that runs on any CDP session: a
// chromedp target or the raw session of a StockSession.
type EmulationStep func(ctx context.Context, s cdp.Session) error

// EmulationSteps is EmulationPlan without the chromedp types.
func EmulationSteps(p profile.Profile) []EmulationStep {
	fp := p.Fingerprint
	var plan []EmulationStep
	if fp.UserAgent != "" {
		plan = append(plan, emulate(emulation.SetUserAgentOverride, userAgentParams(fp)))
	}
	if fp.Timezone != "" {
		plan = append(plan, emulate(emulation.SetTimezoneOverride, emulation.SetTimezoneOverrideParams{TimezoneID: fp.Timezone}))
	}
	if fp.Language != "" {
		plan = append(plan, emulate(emulation.SetLocaleOverride, emulation.SetLocaleOverrideParams{Locale: strings.ReplaceAll(fp.Language, "-", "_")}))
	}
	if fp.HardwareThreads > 0 {
		plan = append(plan, emulate(emulation.SetHardwareConcurrencyOverride, emulation.SetHardwareConcurrencyOverrideParams{HardwareConcurrency: int64(fp.HardwareThreads)}))
	}
	return append(plan, screenSteps(p)...)
}

// ScreenPlan returns only the screen-size step. fingerprint-chromium has no
// flag for the screen, so apply this to its targets. For that engine the
// fields still unset after it are DeviceMemoryGB and GPUVendor/GPURenderer:
// the engine derives the GPU from the seed and nothing sets the memory.
func ScreenPlan(p profile.Profile) []chromedp.Action[chromedp.Void] {
	return asActions(screenSteps(p))
}

func screenSteps(p profile.Profile) []EmulationStep {
	fp := p.Fingerprint
	if fp.ScreenWidth <= 0 || fp.ScreenHeight <= 0 {
		return nil
	}
	// Width and height stay 0 so the real window keeps its own viewport and
	// only window.screen changes.
	return []EmulationStep{emulate(emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{
		ScreenWidth:  int64(fp.ScreenWidth),
		ScreenHeight: int64(fp.ScreenHeight),
	})}
}

func asActions(steps []EmulationStep) []chromedp.Action[chromedp.Void] {
	var out []chromedp.Action[chromedp.Void]
	for _, st := range steps {
		out = append(out, chromedp.Func(func(ctx context.Context, t *chromedp.Target) error { return st(ctx, t) }))
	}
	return out
}

func emulate[P any](cmd cdp.Command[P, cdp.Empty], params P) EmulationStep {
	return func(ctx context.Context, s cdp.Session) error {
		_, err := cdp.Call(ctx, s, cmd, params)
		return err
	}
}

func userAgentParams(fp profile.Fingerprint) emulation.SetUserAgentOverrideParams {
	p := emulation.SetUserAgentOverrideParams{
		UserAgent:      fp.UserAgent,
		AcceptLanguage: acceptLanguages(fp.Language),
		Platform:       navigatorPlatform(fp.Platform),
	}
	major, _, _ := strings.Cut(fp.BrowserVersion, ".")
	if major == "" {
		return p
	}
	// The grease brand changes shape per major in real Chrome; any value in
	// the same format passes, and sites only check that one exists.
	grease := "Not.A/Brand"
	brands := []*emulation.UserAgentBrandVersion{{Brand: "Chromium", Version: major}}
	full := []*emulation.UserAgentBrandVersion{{Brand: "Chromium", Version: fp.BrowserVersion}}
	switch fp.Brand {
	case "Chrome":
		brands = append(brands, &emulation.UserAgentBrandVersion{Brand: "Google Chrome", Version: major})
		full = append(full, &emulation.UserAgentBrandVersion{Brand: "Google Chrome", Version: fp.BrowserVersion})
	case "Edge":
		brands = append(brands, &emulation.UserAgentBrandVersion{Brand: "Microsoft Edge", Version: major})
		full = append(full, &emulation.UserAgentBrandVersion{Brand: "Microsoft Edge", Version: fp.BrowserVersion})
	}
	brands = append(brands, &emulation.UserAgentBrandVersion{Brand: grease, Version: "99"})
	full = append(full, &emulation.UserAgentBrandVersion{Brand: grease, Version: "99.0.0.0"})

	arch := "x86"
	if strings.Contains(fp.GPURenderer, "Apple") {
		arch = "arm"
	}
	p.UserAgentMetadata = &emulation.UserAgentMetadata{
		Brands:          brands,
		FullVersionList: full,
		Platform:        uaPlatform(fp.Platform),
		PlatformVersion: fp.PlatformVersion,
		Architecture:    arch,
		Bitness:         "64",
		FormFactors:     []string{"Desktop"},
	}
	return p
}

// navigatorPlatform is what navigator.platform reports on each OS.
func navigatorPlatform(platform string) string {
	switch platform {
	case profile.PlatformMacOS:
		return "MacIntel"
	case profile.PlatformLinux:
		return "Linux x86_64"
	default:
		return "Win32"
	}
}

// uaPlatform is the Sec-CH-UA-Platform value.
func uaPlatform(platform string) string {
	switch platform {
	case profile.PlatformMacOS:
		return "macOS"
	case profile.PlatformLinux:
		return "Linux"
	default:
		return "Windows"
	}
}
