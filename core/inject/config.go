// Package inject builds the JavaScript that gives a stock Chrome or Edge the
// identity of a profile.Fingerprint. One script serves pages, iframes and
// workers; it detects its own scope.
//
// Not covered, because no script can do it: font enumeration and metrics
// (needs an engine patch), rendering-layer noise, GPU parameters beyond the
// table in webglParams, and anything a detector reads before the script runs.
package inject

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/8bu/ladilala/core/profile"
)

//go:embed injector.js
var injectorJS string

const placeholder = "__LADILALA_CFG__"

// Config is the JSON the injector reads. It is derived only from a
// Fingerprint, so one profile always gets the same script.
type Config struct {
	Seed              [2]uint32 `json:"seed"`
	UserAgent         string    `json:"userAgent"`
	NavigatorPlatform string    `json:"navigatorPlatform"`
	Languages         []string  `json:"languages"`
	HardwareThreads   int       `json:"hardwareThreads"`
	DeviceMemory      int       `json:"deviceMemory"`
	ScreenWidth       int       `json:"screenWidth"`
	ScreenHeight      int       `json:"screenHeight"`
	Taskbar           int       `json:"taskbar"`
	GPUVendor         string    `json:"gpuVendor"`
	GPURenderer       string    `json:"gpuRenderer"`
	Canvas            bool      `json:"canvas"`
	Audio             bool      `json:"audio"`
	WebGL             bool      `json:"webgl"`
	ClientRects       bool      `json:"clientRects"`
	WebGLParams       [][3]any  `json:"webglParams"`
}

// NewConfig derives the injector config from fp. Surfaces whose mode is
// profile.ModeReal are left untouched in the page; every other mode patches.
func NewConfig(fp profile.Fingerprint) Config {
	c := Config{
		Seed:              [2]uint32{uint32(fp.Seed), uint32(fp.Seed >> 32)},
		UserAgent:         fp.UserAgent,
		NavigatorPlatform: navigatorPlatform(fp.Platform),
		Languages:         languages(fp.Language),
		HardwareThreads:   fp.HardwareThreads,
		DeviceMemory:      fp.DeviceMemoryGB,
		ScreenWidth:       fp.ScreenWidth,
		ScreenHeight:      fp.ScreenHeight,
		GPUVendor:         fp.GPUVendor,
		GPURenderer:       fp.GPURenderer,
		Canvas:            fp.Canvas != profile.ModeReal,
		Audio:             fp.Audio != profile.ModeReal,
		WebGL:             fp.WebGL != profile.ModeReal,
		ClientRects:       fp.ClientRects != profile.ModeReal,
	}
	if fp.Platform == profile.PlatformWindows || fp.Platform == "" {
		c.Taskbar = 40
	} else if fp.Platform == profile.PlatformMacOS {
		c.Taskbar = 25 // menu bar
	}
	c.WebGLParams = webglParams(fp)
	return c
}

// Script returns the injector with cfg embedded.
func Script(cfg Config) (string, error) {
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("inject: encode config: %w", err)
	}
	return strings.Replace(injectorJS, placeholder, string(b), 1), nil
}

// ScriptFor is NewConfig plus Script.
func ScriptFor(fp profile.Fingerprint) (string, error) { return Script(NewConfig(fp)) }

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

// languages mimics Chrome: the profile language, then its base tag.
func languages(lang string) []string {
	if lang == "" {
		return []string{"en-US", "en"}
	}
	base, _, ok := strings.Cut(lang, "-")
	if !ok || base == lang {
		return []string{lang}
	}
	return []string{lang, base}
}

// WebGL parameter enums.
const (
	maxTextureSize          = 0x0D33
	maxViewportDims         = 0x0D3A
	maxRenderbufferSize     = 0x84E8
	maxCubeMapTextureSize   = 0x851C
	maxVertexAttribs        = 0x8869
	maxVertexUniformVectors = 0x8DFB
	maxVaryingVectors       = 0x8DFC
	maxFragUniformVectors   = 0x8DFD
	maxVertexTexUnits       = 0x8B4C
	maxTextureImageUnits    = 0x8872
	maxCombinedTexUnits     = 0x8B4D
	aliasedLineWidthRange   = 0x846E
	aliasedPointSizeRange   = 0x846D
)

// webglParams is the parameter table of the claimed GPU class, as ANGLE
// reports it in Chrome. The Windows D3D11 and macOS Metal rows are the values
// seen on common hardware; the Linux row is approximate [unverified]. Only
// parameters that differ between classes are listed.
func webglParams(fp profile.Fingerprint) [][3]any {
	type row = [3]any
	n := func(k int, v int) row { return row{k, "n", v} }
	i2 := func(k, a, b int) row { return row{k, "i32", []int{a, b}} }
	f2 := func(k int, a, b float64) row { return row{k, "f32", []float64{a, b}} }
	switch fp.Platform {
	case profile.PlatformMacOS:
		return []row{n(maxTextureSize, 16384), i2(maxViewportDims, 16384, 16384), n(maxRenderbufferSize, 16384),
			n(maxCubeMapTextureSize, 16384), n(maxVertexAttribs, 16), n(maxVertexUniformVectors, 1024),
			n(maxVaryingVectors, 31), n(maxFragUniformVectors, 1024), n(maxVertexTexUnits, 16),
			n(maxTextureImageUnits, 16), n(maxCombinedTexUnits, 32), f2(aliasedLineWidthRange, 1, 1), f2(aliasedPointSizeRange, 1, 511)}
	case profile.PlatformLinux:
		return []row{n(maxTextureSize, 16384), i2(maxViewportDims, 16384, 16384), n(maxRenderbufferSize, 16384),
			n(maxCubeMapTextureSize, 16384), n(maxVertexAttribs, 16), n(maxVertexUniformVectors, 1024),
			n(maxVaryingVectors, 30), n(maxFragUniformVectors, 1024), n(maxVertexTexUnits, 16),
			n(maxTextureImageUnits, 16), n(maxCombinedTexUnits, 32), f2(aliasedLineWidthRange, 1, 1), f2(aliasedPointSizeRange, 1, 255)}
	default: // Windows, ANGLE Direct3D11
		return []row{n(maxTextureSize, 16384), i2(maxViewportDims, 32767, 32767), n(maxRenderbufferSize, 16384),
			n(maxCubeMapTextureSize, 16384), n(maxVertexAttribs, 16), n(maxVertexUniformVectors, 4095),
			n(maxVaryingVectors, 30), n(maxFragUniformVectors, 1024), n(maxVertexTexUnits, 16),
			n(maxTextureImageUnits, 16), n(maxCombinedTexUnits, 32), f2(aliasedLineWidthRange, 1, 1), f2(aliasedPointSizeRange, 1, 1024)}
	}
}
