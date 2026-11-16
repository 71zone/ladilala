package profile

// Platform identifiers used by Fingerprint.Platform.
const (
	PlatformWindows = "windows"
	PlatformMacOS   = "macos"
	PlatformLinux   = "linux"
)

// deviceClass is one real-world hardware class. Every value list holds only
// combinations that ship together in the wild, so any pick from it is a
// coherent device and Validate can check an arbitrary Fingerprint against it.
type deviceClass struct {
	platform   string
	gpuVendor  string // WEBGL_debug_renderer_info UNMASKED_VENDOR_WEBGL
	gpuName    string // UNMASKED_RENDERER_WEBGL, unique per platform
	integrated bool
	threads    []int
	// Chrome reports navigator.deviceMemory rounded down to a power of two
	// and capped at 8, so a 16/32 GB machine reads 8 and a 4 GB one reads 4.
	memory  []int
	screens [][2]int
}

// platformVersions are the values Chrome reports through UA-CH
// platformVersion. Windows 11 reports 13.0.0 or newer, Windows 10 reports
// 10.0.0, and macOS keeps "10_15_7" in the UA string whatever its version.
var platformVersions = map[string][]string{
	PlatformWindows: {"10.0.0", "13.0.0", "15.0.0"},
	PlatformMacOS:   {"13.6.0", "14.5.0", "15.2.0"},
	PlatformLinux:   {"6.5.0", "6.8.0"},
}

// validDeviceMemory is the set of navigator.deviceMemory values Chrome emits
// for whole-GB machines (0.25 and 0.5 are not realistic for desktop hardware).
var validDeviceMemory = []int{1, 2, 4, 8}

func d3d(vendor, model string) string {
	return "ANGLE (" + vendor + ", " + model + " Direct3D11 vs_5_0 ps_5_0, D3D11)"
}

var (
	laptop1080 = [][2]int{{1920, 1080}}
	desk1080   = [][2]int{{1920, 1080}, {2560, 1440}}
)

var deviceClasses = []deviceClass{
	// Windows, Intel integrated.
	{PlatformWindows, "Google Inc. (Intel)", d3d("Intel", "Intel(R) UHD Graphics 620 (0x00005917)"), true,
		[]int{4, 8}, []int{4, 8}, [][2]int{{1366, 768}, {1920, 1080}}},
	{PlatformWindows, "Google Inc. (Intel)", d3d("Intel", "Intel(R) UHD Graphics 630 (0x00003E92)"), true,
		[]int{6, 8, 12}, []int{8}, desk1080},
	{PlatformWindows, "Google Inc. (Intel)", d3d("Intel", "Intel(R) Iris(R) Xe Graphics (0x00009A49)"), true,
		[]int{8, 12}, []int{8}, [][2]int{{1920, 1080}, {1920, 1200}, {2560, 1440}}},
	{PlatformWindows, "Google Inc. (Intel)", d3d("Intel", "Intel(R) UHD Graphics 770 (0x00004688)"), true,
		[]int{12, 16, 20}, []int{8}, desk1080},
	// Windows, AMD integrated.
	{PlatformWindows, "Google Inc. (AMD)", d3d("AMD", "AMD Radeon(TM) Graphics (0x00001638)"), true,
		[]int{8, 12, 16}, []int{8}, laptop1080},
	// Windows, discrete.
	{PlatformWindows, "Google Inc. (NVIDIA)", d3d("NVIDIA", "NVIDIA GeForce GTX 1060 (0x00001C03)"), false,
		[]int{6, 8, 12}, []int{8}, desk1080},
	{PlatformWindows, "Google Inc. (NVIDIA)", d3d("NVIDIA", "NVIDIA GeForce GTX 1650 (0x00001F82)"), false,
		[]int{8, 12, 16}, []int{8}, laptop1080},
	{PlatformWindows, "Google Inc. (NVIDIA)", d3d("NVIDIA", "NVIDIA GeForce RTX 3060 (0x00002504)"), false,
		[]int{12, 16, 20}, []int{8}, desk1080},
	{PlatformWindows, "Google Inc. (NVIDIA)", d3d("NVIDIA", "NVIDIA GeForce RTX 4060 (0x00002882)"), false,
		[]int{12, 16, 20, 24}, []int{8}, desk1080},
	{PlatformWindows, "Google Inc. (AMD)", d3d("AMD", "AMD Radeon RX 580 Series (0x000067DF)"), false,
		[]int{6, 8, 12}, []int{8}, desk1080},

	// macOS, Apple silicon. Chrome still reports an Intel Mac UA.
	{PlatformMacOS, "Google Inc. (Apple)", "ANGLE (Apple, ANGLE Metal Renderer: Apple M1, Unspecified Version)", true,
		[]int{8}, []int{8}, [][2]int{{1440, 900}, {1680, 1050}}},
	{PlatformMacOS, "Google Inc. (Apple)", "ANGLE (Apple, ANGLE Metal Renderer: Apple M2, Unspecified Version)", true,
		[]int{8}, []int{8}, [][2]int{{1470, 956}, {1440, 900}}},
	{PlatformMacOS, "Google Inc. (Apple)", "ANGLE (Apple, ANGLE Metal Renderer: Apple M3 Pro, Unspecified Version)", true,
		[]int{11, 12}, []int{8}, [][2]int{{1512, 982}, {1728, 1117}}},

	// Linux, Mesa/NVIDIA proprietary drivers.
	{PlatformLinux, "Google Inc. (Intel)", "ANGLE (Intel, Mesa Intel(R) UHD Graphics 630 (CFL GT2), OpenGL 4.6)", true,
		[]int{8, 12}, []int{8}, desk1080},
	{PlatformLinux, "Google Inc. (NVIDIA Corporation)", "ANGLE (NVIDIA Corporation, NVIDIA GeForce GTX 1660/PCIe/SSE2, OpenGL 4.5.0 NVIDIA 535.129.03)", false,
		[]int{12, 16}, []int{8}, desk1080},
}

func classesFor(platform string) []deviceClass {
	var out []deviceClass
	for _, c := range deviceClasses {
		if c.platform == platform {
			out = append(out, c)
		}
	}
	return out
}

func findClass(platform, renderer string) (deviceClass, bool) {
	for _, c := range deviceClasses {
		if c.platform == platform && c.gpuName == renderer {
			return c, true
		}
	}
	return deviceClass{}, false
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func containsStr(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
