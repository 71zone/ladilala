// Command app is the Ladilala desktop shell: a Wails v3 window that embeds the
// Vue UI and exposes the Shell service to it.
package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/updater"
)

//go:embed all:dist
var distFS embed.FS

//go:embed updater.pub
var updaterPublicKey []byte

// version is set at build time: -ldflags "-X main.version=1.0.0".
var version = "1.0.1" // x-release-please-version

const singleInstanceID = "app.ladilala.desktop"

// instanceName is LADILALA_INSTANCE. A named instance has its own
// single-instance lock and its own window title, so several can run side by
// side (each with its own LADILALA_DATA folder). Empty by default.
func instanceName() string { return strings.TrimSpace(os.Getenv("LADILALA_INSTANCE")) }

func main() {
	started := processStart()
	if err := ensureWebView2(); err != nil {
		fatalDialog("Ladilala cannot start", err.Error())
		os.Exit(1)
	}

	dataDir, err := userDataDir()
	if err != nil {
		log.Fatal(err)
	}
	dist, err := fs.Sub(distFS, "dist")
	if err != nil {
		log.Fatal(err)
	}

	shell := newShell(started, dataDir)
	backend, err := openBackend(context.Background(), dataDir)
	if err != nil {
		fatalDialog("Ladilala cannot start", err.Error())
		os.Exit(1)
	}
	var win *application.WebviewWindow
	app := application.New(application.Options{
		Name:        "Ladilala",
		Description: "Browser profile manager and web macro player",
		Services:    append([]application.Service{application.NewService(shell)}, uiServices(backend)...),
		Assets:      application.AssetOptions{Handler: spaHandler(dist)},
		ShouldQuit: func() bool {
			return backend.BeforeQuit == nil || backend.BeforeQuit()
		},
		Windows: application.WindowsOptions{
			WebviewUserDataPath: filepath.Join(dataDir, "webview2"),
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: instanceUniqueID(),
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				if win == nil {
					return
				}
				if win.IsMinimised() {
					win.UnMinimise()
				}
				win.Show()
				win.Focus()
			},
		},
	})
	shell.app = app
	backend.App = app

	// The feed URL comes from settings (empty means updates are not
	// configured) and LADILALA_UPDATE_URL overrides it; see feedProvider.
	if err := app.Updater.Init(updater.Config{
		CurrentVersion: version,
		Providers:      []updater.Provider{newFeedProvider(backend.Settings)},
		PublicKey:      updaterPublicKey,
	}); err != nil {
		log.Fatal(err)
	}

	state := loadWindowState(app, dataDir)
	opts := application.WebviewWindowOptions{
		Title:            windowTitle(),
		Width:            state.W,
		Height:           state.H,
		MinWidth:         900,
		MinHeight:        600,
		URL:              "/",
		BackgroundColour: application.NewRGB(24, 24, 27),
		Windows:          application.WindowsWindow{Theme: application.SystemDefault},
	}
	if state.Placed {
		opts.InitialPosition = application.WindowXY
		opts.X, opts.Y = state.X, state.Y
	}
	if state.Maximised {
		opts.StartState = application.WindowStateMaximised
	}
	win = app.Window.NewWithOptions(opts)
	state.track(win)
	// Closing the last window ends the app without asking ShouldQuit on
	// Windows, so the install-on-quit hand-off hooks the close itself.
	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if backend.BeforeQuit != nil && !backend.BeforeQuit() {
			e.Cancel()
		}
	})

	runErr := app.Run()
	_ = backend.Close(context.Background())
	if runErr != nil {
		log.Fatal(runErr)
	}
}

// spaHandler serves the embedded UI. A path without a file extension that has
// no file behind it gets index.html, so the Vue router can own /profiles and
// the other history-mode routes after a reload.
func spaHandler(root fs.FS) http.Handler {
	files := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" && path.Ext(name) == "" {
			if _, err := fs.Stat(root, name); err != nil {
				r = r.Clone(r.Context())
				r.URL.Path = "/"
			}
		}
		files.ServeHTTP(w, r)
	})
}

func instanceUniqueID() string {
	if n := instanceName(); n != "" {
		return singleInstanceID + "." + n
	}
	return singleInstanceID
}

func windowTitle() string {
	if n := instanceName(); n != "" {
		return "Ladilala [" + n + "]"
	}
	return "Ladilala"
}
