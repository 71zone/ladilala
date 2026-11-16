package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/8bu/ladilala/core/engine"
	"github.com/8bu/ladilala/core/store"
)

// Backend holds what every UI service shares: the SQLite store, the run
// engine and the stock browser launcher. One Backend lives for the whole
// process. Services receive it in their constructors.
type Backend struct {
	// App is set after application.New. Use Emit instead of touching it.
	App      *application.App
	DataDir  string
	Store    *store.Store
	Engine   *engine.Service
	Launcher *engine.StockLauncher
	// Settings is settings.json in DataDir, shared by the Settings, Engine and
	// AppUpdate services.
	Settings *settingsStore
	// BeforeQuit is set by AppUpdateService; main passes it to Wails as ShouldQuit.
	BeforeQuit func() bool
}

// openBackend opens the store under dataDir/db and builds the run engine.
// Secrets use DPAPI on Windows and the insecure dev file store elsewhere.
func openBackend(ctx context.Context, dataDir string) (*Backend, error) {
	dbDir := filepath.Join(dataDir, "db")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		return nil, err
	}
	st, err := store.Open(ctx, dbDir, store.DefaultSecrets(dbDir))
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	cfg, err := openSettings(dataDir)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	l, err := engine.NewStockLauncher()
	if err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("browser launcher: %w", err)
	}
	return &Backend{DataDir: dataDir, Store: st, Engine: engine.NewService(st, l), Launcher: l, Settings: cfg}, nil
}

// Emit sends an event to the UI. It does nothing before the app exists.
func (b *Backend) Emit(name string, data any) {
	if b.App != nil {
		b.App.Event.Emit(name, data)
	}
}

// Close stops the engine, kills the browsers and closes the store.
func (b *Backend) Close(ctx context.Context) error {
	var first error
	for _, err := range []error{b.Engine.Close(ctx), b.Launcher.Close(), b.Store.Close()} {
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}
