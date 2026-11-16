package profile

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestNewFingerprintDeterministic(t *testing.T) {
	for _, p := range []string{"", PlatformWindows, PlatformMacOS, PlatformLinux} {
		opts := Options{Platform: p, Language: "de-DE", Timezone: "Europe/Berlin", BrowserVersion: "142.0.7444.175"}
		a := mustJSON(t, NewFingerprint(12345, opts))
		b := mustJSON(t, NewFingerprint(12345, opts))
		if a != b {
			t.Errorf("platform %q: same seed gave different JSON:\n%s\n%s", p, a, b)
		}
	}
}

func TestNewFingerprintSeedsDiffer(t *testing.T) {
	seen := map[string]bool{}
	for seed := uint64(0); seed < 200; seed++ {
		fp := NewFingerprint(seed, Options{})
		fp.Seed = 0
		seen[mustJSON(t, fp)] = true
	}
	// 10 windows classes with several thread/screen/version choices each.
	if len(seen) < 50 {
		t.Fatalf("only %d distinct devices across 200 seeds", len(seen))
	}
	a, b := NewFingerprint(1, Options{}), NewFingerprint(2, Options{})
	if mustJSON(t, a) == mustJSON(t, b) {
		t.Fatal("seeds 1 and 2 are identical")
	}
}

func TestGeneratedPresetsValidate(t *testing.T) {
	for _, platform := range []string{"", PlatformWindows, PlatformMacOS, PlatformLinux} {
		for seed := uint64(0); seed < 2000; seed++ {
			fp := NewFingerprint(seed*2654435761+7, Options{Platform: platform})
			if issues := Validate(fp); len(issues) != 0 {
				t.Fatalf("platform %q seed %d: %v\n%s", platform, fp.Seed, issues, mustJSON(t, fp))
			}
		}
	}
}

func TestNewFingerprintShape(t *testing.T) {
	fp := NewFingerprint(99, Options{BrowserVersion: "136"})
	if fp.Platform != PlatformWindows || fp.Brand != "Chrome" || fp.BrowserVersion != "136.0.0.0" {
		t.Fatalf("defaults wrong: %+v", fp)
	}
	wantUA := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36"
	if fp.UserAgent != wantUA {
		t.Fatalf("UA = %q", fp.UserAgent)
	}
	if fp.Canvas != ModeNoise || fp.Audio != ModeNoise || fp.WebGL != ModeNoise ||
		fp.Fonts != ModeNoise || fp.ClientRects != ModeNoise || fp.WebRTC != ModeOff {
		t.Fatalf("default modes wrong: %+v", fp)
	}
	if fp.Language != "en-US" || fp.Timezone != "America/New_York" {
		t.Fatalf("default geo wrong: %s %s", fp.Language, fp.Timezone)
	}
	if NewFingerprint(1, Options{BrowserVersion: "nonsense"}).BrowserVersion != DefaultBrowserVersion {
		t.Fatal("bad version must fall back to default")
	}
	if NewFingerprint(1, Options{Platform: "beos"}).Platform != PlatformWindows {
		t.Fatal("unknown platform must fall back to windows")
	}
}

func TestShuffleKeepsOverrides(t *testing.T) {
	base := NewFingerprint(1, Options{Platform: PlatformMacOS, Language: "fr-FR", Timezone: "Europe/Paris", BrowserVersion: "142.0.7444.175"})
	base.WebRTC = ModeReal
	base.Brand = "Edge"
	base.UserAgent = userAgent(base.Platform, "Edge", "142")

	sh := Shuffle(base, 2)
	if sh.Seed != 2 || sh.Language != "fr-FR" || sh.Timezone != "Europe/Paris" ||
		sh.Platform != PlatformMacOS || sh.BrowserVersion != "142.0.7444.175" ||
		sh.Brand != "Edge" || sh.WebRTC != ModeReal {
		t.Fatalf("overrides lost: %+v", sh)
	}
	if !strings.HasSuffix(sh.UserAgent, " Edg/142.0.0.0") {
		t.Fatalf("UA lost Edge token: %q", sh.UserAgent)
	}
	if issues := Validate(sh); len(issues) != 0 {
		t.Fatalf("shuffled fingerprint invalid: %v", issues)
	}
	// A different seed must be able to move the device.
	moved := false
	for s := uint64(3); s < 40 && !moved; s++ {
		o := Shuffle(base, s)
		moved = o.GPURenderer != sh.GPURenderer || o.HardwareThreads != sh.HardwareThreads
	}
	if !moved {
		t.Fatal("shuffle never changes the device")
	}
}

func TestApplyGeo(t *testing.T) {
	fp := NewFingerprint(5, Options{})
	tests := []struct {
		name, tz, lang, wantTZ, wantLang string
	}{
		{"both", "Asia/Tokyo", "ja-JP", "Asia/Tokyo", "ja-JP"},
		{"tz only", "Europe/London", "", "Europe/London", "en-US"},
		{"lang only", "", "pt-BR", "America/New_York", "pt-BR"},
		{"neither", "", "", "America/New_York", "en-US"},
		{"trims", " Asia/Seoul ", " ko-KR ", "Asia/Seoul", "ko-KR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ApplyGeo(fp, tt.tz, tt.lang)
			if got.Timezone != tt.wantTZ || got.Language != tt.wantLang {
				t.Fatalf("got %s %s", got.Timezone, got.Language)
			}
			if fp.Timezone != "America/New_York" {
				t.Fatal("ApplyGeo mutated its input")
			}
		})
	}
}

func TestValidateRejectsIncoherent(t *testing.T) {
	win := NewFingerprint(3, Options{Platform: PlatformWindows, BrowserVersion: "150.0.7871.186"})
	mac := NewFingerprint(3, Options{Platform: PlatformMacOS, BrowserVersion: "150.0.7871.186"})

	tests := []struct {
		name   string
		base   Fingerprint
		mutate func(*Fingerprint)
		want   string
	}{
		{"macOS with Direct3D", mac, func(f *Fingerprint) { f.GPURenderer = win.GPURenderer }, "Direct3D"},
		{"windows with Metal", win, func(f *Fingerprint) { f.GPURenderer = mac.GPURenderer }, "Metal"},
		{"windows with OpenGL", win, func(f *Fingerprint) { f.GPURenderer = "ANGLE (Intel, Mesa, OpenGL 4.6)" }, "OpenGL"},
		{"UA major differs", win, func(f *Fingerprint) { f.BrowserVersion = "140.0.1.2" }, "major"},
		{"UA without Chrome token", win, func(f *Fingerprint) { f.UserAgent = "Mozilla/5.0" }, "Chrome/<major>"},
		{"UA wrong OS token", win, func(f *Fingerprint) { f.UserAgent = mac.UserAgent }, "reduced"},
		{"UA not reduced", win, func(f *Fingerprint) {
			f.UserAgent = strings.Replace(f.UserAgent, "Chrome/150.0.0.0", "Chrome/150.0.7871.186", 1)
		}, "reduced"},
		{"unknown GPU", win, func(f *Fingerprint) { f.GPURenderer = "ANGLE (Intel, Totally Fake GPU Direct3D11, D3D11)" }, "not a known"},
		{"vendor mismatch", win, func(f *Fingerprint) { f.GPUVendor = "Google Inc. (Apple)" }, "vendor"},
		{"integrated GPU with 32 GB", intelIGPU(win), func(f *Fingerprint) { f.DeviceMemoryGB = 32 }, "deviceMemoryGb 32"},
		{"integrated GPU with 2 GB", intelIGPU(win), func(f *Fingerprint) { f.DeviceMemoryGB = 2 }, "2 GB memory"},
		{"integrated GPU with 64 threads", intelIGPU(win), func(f *Fingerprint) { f.HardwareThreads = 64 }, "hardware threads"},
		{"screen outside class", intelIGPU(win), func(f *Fingerprint) { f.ScreenWidth, f.ScreenHeight = 3840, 2160 }, "screen"},
		{"platform version of another OS", win, func(f *Fingerprint) { f.PlatformVersion = "14.5.0" }, "platformVersion"},
		{"unknown platform", win, func(f *Fingerprint) { f.Platform = "beos" }, "platform"},
		{"unknown brand", win, func(f *Fingerprint) { f.Brand = "Netscape" }, "brand"},
		{"bad version", win, func(f *Fingerprint) { f.BrowserVersion = "150" }, "browserVersion"},
		{"bad language", win, func(f *Fingerprint) { f.Language = "english" }, "language"},
		{"bad timezone", win, func(f *Fingerprint) { f.Timezone = "Mars" }, "timezone"},
		{"bad mode", win, func(f *Fingerprint) { f.Canvas = "" }, "canvas mode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp := tt.base
			tt.mutate(&fp)
			issues := Validate(fp)
			if len(issues) == 0 {
				t.Fatalf("accepted incoherent fingerprint %s", mustJSON(t, fp))
			}
			if !strings.Contains(strings.Join(issues, "\n"), tt.want) {
				t.Fatalf("issues %v do not mention %q", issues, tt.want)
			}
		})
	}
}

// intelIGPU returns a valid fingerprint pinned to a laptop iGPU class.
func intelIGPU(base Fingerprint) Fingerprint {
	c, _ := findClass(PlatformWindows, d3d("Intel", "Intel(R) UHD Graphics 620 (0x00005917)"))
	base.GPUVendor, base.GPURenderer = c.gpuVendor, c.gpuName
	base.HardwareThreads, base.DeviceMemoryGB = 8, 8
	base.ScreenWidth, base.ScreenHeight = 1920, 1080
	return base
}

func TestIntelIGPUHelperIsValid(t *testing.T) {
	win := NewFingerprint(3, Options{})
	if issues := Validate(intelIGPU(win)); len(issues) != 0 {
		t.Fatal(issues)
	}
}
