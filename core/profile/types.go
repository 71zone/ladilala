// Package profile defines the browser profile model shared by the driver,
// proxy relay, fingerprint generator, and storage layers.
package profile

// ProxyScheme is the protocol used to reach the upstream proxy.
type ProxyScheme string

const (
	ProxyNone   ProxyScheme = ""
	ProxyHTTP   ProxyScheme = "http"
	ProxyHTTPS  ProxyScheme = "https"
	ProxySOCKS5 ProxyScheme = "socks5"
)

// Proxy is the upstream proxy of one profile. The password is never stored in
// this struct on disk; storage resolves it from the OS credential store.
type Proxy struct {
	Scheme   ProxyScheme `json:"scheme"`
	Host     string      `json:"host"`
	Port     int         `json:"port"`
	Username string      `json:"username,omitempty"`
	Password string      `json:"-"`
}

// Mode controls how one fingerprint surface is treated.
type Mode string

const (
	ModeNoise Mode = "noise" // perturb deterministically from the seed
	ModeReal  Mode = "real"  // pass the real value through
	ModeOff   Mode = "off"   // block or neutralise the surface
)

// Fingerprint is one coherent device identity. A preset generator fills every
// field from Seed so the attributes agree with each other.
type Fingerprint struct {
	Seed uint64 `json:"seed"`

	Platform        string `json:"platform"`        // "windows", "macos", "linux"
	PlatformVersion string `json:"platformVersion"` // e.g. "10.0.0"
	Brand           string `json:"brand"`           // e.g. "Chrome"
	BrowserVersion  string `json:"browserVersion"`  // full version, e.g. "150.0.7871.186"
	UserAgent       string `json:"userAgent"`

	ScreenWidth     int    `json:"screenWidth"`
	ScreenHeight    int    `json:"screenHeight"`
	HardwareThreads int    `json:"hardwareThreads"`
	DeviceMemoryGB  int    `json:"deviceMemoryGb"`
	GPUVendor       string `json:"gpuVendor"`
	GPURenderer     string `json:"gpuRenderer"`

	Language string `json:"language"` // BCP 47, e.g. "en-US"
	Timezone string `json:"timezone"` // IANA, e.g. "America/New_York"

	Canvas      Mode `json:"canvas"`
	Audio       Mode `json:"audio"`
	WebGL       Mode `json:"webgl"`
	Fonts       Mode `json:"fonts"`
	ClientRects Mode `json:"clientRects"`
	WebRTC      Mode `json:"webrtc"` // ModeOff = no IP leak
}

// Profile is one isolated browser identity.
type Profile struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Group       string      `json:"group,omitempty"`
	Tags        []string    `json:"tags,omitempty"`
	Notes       string      `json:"notes,omitempty"`
	Proxy       Proxy       `json:"proxy"`
	Fingerprint Fingerprint `json:"fingerprint"`
	// DataDir is the absolute Chrome user-data-dir. It must never be the
	// default Chrome directory (Chrome 136+ ignores debug switches there).
	DataDir string `json:"dataDir"`
}
