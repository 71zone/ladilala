package driver

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/8bu/ladilala/core/profile"
)

func fpProfile() profile.Profile {
	return profile.Profile{
		Proxy: profile.Proxy{Scheme: profile.ProxySOCKS5, Host: "p.example", Port: 1080},
		Fingerprint: profile.Fingerprint{
			Seed: 1<<32 | 1234, Platform: "windows", PlatformVersion: "10.0.0",
			Brand: "Edge", BrowserVersion: "150.0.7871.186", HardwareThreads: 8,
			Language: "en-US", Timezone: "America/New_York",
			Canvas: profile.ModeNoise, Audio: profile.ModeReal, Fonts: profile.ModeOff,
			ClientRects: profile.ModeNoise, WebGL: profile.ModeReal, WebRTC: profile.ModeOff,
		},
	}
}

func TestFingerprintChromiumArgs(t *testing.T) {
	args, err := FingerprintChromium{}.Args(fpProfile(), "127.0.0.1:5555")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--fingerprint=1234", // low 32 bits of the seed
		"--fingerprint-platform=windows",
		"--fingerprint-platform-version=10.0.0",
		"--fingerprint-brand=Edge",
		"--fingerprint-brand-version=150.0.7871.186",
		"--fingerprint-hardware-concurrency=8",
		"--timezone=America/New_York",
		"--lang=en-US",
		"--accept-lang=en-US,en",
		"--disable-spoofing=audio,font,gpu",
		"--force-webrtc-ip-handling-policy=disable_non_proxied_udp",
		"--disable-non-proxied-udp",
		"--proxy-server=http://127.0.0.1:5555",
	}
	if !slices.Equal(args, want) {
		t.Fatalf("args\n got %v\nwant %v", args, want)
	}
}

func TestFingerprintChromiumRejects(t *testing.T) {
	p := fpProfile()
	if _, err := (FingerprintChromium{}).Args(p, ""); !errors.Is(err, ErrProxyRequired) {
		t.Fatalf("proxy with no relay: %v", err)
	}
	p.Proxy = profile.Proxy{}
	p.Fingerprint.Seed = 0
	if _, err := (FingerprintChromium{}).Args(p, ""); !errors.Is(err, ErrNoFingerprintSeed) {
		t.Fatalf("zero seed: %v", err)
	}
	p.Fingerprint.Seed = 7
	p.Fingerprint.Platform = "beos"
	if _, err := (FingerprintChromium{}).Args(p, ""); err == nil {
		t.Fatal("unknown platform accepted")
	}
}

func TestSystemChromeArgs(t *testing.T) {
	p := fpProfile()
	args, err := SystemChrome{}.Args(p, "http://127.0.0.1:9")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"--lang=en-US", "--proxy-server=http://127.0.0.1:9"} {
		if !slices.Contains(args, w) {
			t.Errorf("missing %s in %v", w, args)
		}
	}
	for _, a := range args {
		if strings.HasPrefix(a, "--fingerprint") {
			t.Errorf("stock Chrome must not get %s", a)
		}
	}
}

func TestProxyFlagRejectsCredentials(t *testing.T) {
	for _, bad := range []string{"user:pw@127.0.0.1:1", "127.0.0.1"} {
		if _, err := proxyFlag(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestEmulationPlan(t *testing.T) {
	p := fpProfile()
	p.Fingerprint.UserAgent = "Mozilla/5.0 test Chrome/150.0.0.0"
	p.Fingerprint.ScreenWidth, p.Fingerprint.ScreenHeight = 1920, 1080
	if n := len(EmulationPlan(p)); n != 5 { // UA, timezone, locale, threads, screen
		t.Fatalf("plan has %d steps, want 5", n)
	}
	if n := len(ScreenPlan(profile.Profile{})); n != 0 {
		t.Fatalf("empty screen plan has %d steps", n)
	}
	ua := userAgentParams(p.Fingerprint)
	if ua.Platform != "Win32" || ua.UserAgentMetadata.Platform != "Windows" ||
		ua.UserAgentMetadata.Brands[1].Brand != "Microsoft Edge" || ua.UserAgentMetadata.Brands[0].Version != "150" {
		t.Fatalf("bad UA params %+v %+v", ua, ua.UserAgentMetadata)
	}
}
