package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	webView2Key = `SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`
	// webView2Bootstrapper is Microsoft's stable link to the small online
	// installer. It installs the Evergreen runtime and needs no admin rights
	// when the user is not on a managed machine.
	webView2Bootstrapper = "https://go.microsoft.com/fwlink/p/?LinkId=2124703"
)

// regLocation is one registry place that can hold the runtime version.
type regLocation struct {
	root registry.Key
	path string
	acc  uint32
}

// webView2Locations are the places the Evergreen runtime records its version
// ("pv"): machine-wide 32-bit view, machine-wide 64-bit view, current user.
var webView2Locations = []regLocation{
	{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`, registry.QUERY_VALUE},
	{registry.LOCAL_MACHINE, webView2Key, registry.QUERY_VALUE | registry.WOW64_64KEY},
	{registry.CURRENT_USER, webView2Key, registry.QUERY_VALUE},
}

// webView2Version reads the installed Evergreen runtime version, or "".
func webView2Version() string { return versionAt(webView2Locations) }

// versionAt returns the first usable "pv" value in locs. Empty and "0.0.0.0"
// (the value Edge Update leaves after an uninstall) count as not installed.
func versionAt(locs []regLocation) string {
	for _, c := range locs {
		k, err := registry.OpenKey(c.root, c.path, c.acc)
		if err != nil {
			continue
		}
		v, _, err := k.GetStringValue("pv")
		k.Close()
		if err == nil && v != "" && v != "0.0.0.0" {
			return v
		}
	}
	return ""
}

const webView2Page = "https://developer.microsoft.com/microsoft-edge/webview2/"

// ensureWebView2 passes when the runtime is installed. Otherwise it explains
// the problem and offers to run Microsoft's bootstrapper.
func ensureWebView2() error {
	return ensureWebView2With(webView2Version, func() bool {
		return askDialog("Ladilala needs Microsoft Edge WebView2",
			"The WebView2 Runtime is not installed on this PC.\n\nInstall it now? Ladilala downloads Microsoft's small installer and runs it.")
	}, runBootstrapper)
}

// ensureWebView2With is ensureWebView2 with its three side effects passed in.
func ensureWebView2With(version func() string, ask func() bool, install func() error) error {
	if version() != "" {
		return nil
	}
	if !ask() {
		return errors.New("The WebView2 Runtime is missing. Install it from " + webView2Page + " and start Ladilala again.")
	}
	if err := install(); err != nil {
		return fmt.Errorf("The WebView2 installer failed: %v\n\nInstall the runtime from %s and start Ladilala again.", err, webView2Page)
	}
	if version() == "" {
		return errors.New("The WebView2 Runtime is still missing after the installer ran. Start Ladilala again after the installation finishes.")
	}
	return nil
}

func runBootstrapper() error {
	resp, err := http.Get(webView2Bootstrapper)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	exe := filepath.Join(os.TempDir(), "MicrosoftEdgeWebview2Setup.exe")
	f, err := os.Create(exe)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	f.Close()
	defer os.Remove(exe)
	return exec.Command(exe, "/silent", "/install").Run()
}

func askDialog(title, text string) bool {
	return messageBox(title, text, windows.MB_YESNO|windows.MB_ICONQUESTION) == 6 // IDYES
}

func fatalDialog(title, text string) {
	messageBox(title, text, windows.MB_OK|windows.MB_ICONERROR)
}

func messageBox(title, text string, flags uint32) int32 {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(text)
	r, _ := windows.MessageBox(0, m, t, flags|windows.MB_SETFOREGROUND)
	return r
}

// processStart is the process creation time, so cold start includes the Go
// runtime and WebView2 start, not only main.
func processStart() time.Time {
	var c, e, k, u windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &c, &e, &k, &u); err != nil {
		return time.Now()
	}
	return time.Unix(0, c.Nanoseconds())
}
