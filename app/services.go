package main

import "github.com/wailsapp/wails/v3/pkg/application"

// uiServices lists the Go services the UI calls, besides the Shell. Each
// service is a struct in package main, so the UI calls it as
// main.<StructName>.<Method>. Add one line per service, keep the list sorted.
func uiServices(b *Backend) []application.Service {
	profiles := newProfileService(b)
	engines := newEngineService(b)
	return []application.Service{
		application.NewService(newAppUpdateService(b, engines)),
		application.NewService(newDataService(b)),
		application.NewService(engines),
		application.NewService(newMacroService(b)),
		application.NewService(profiles),
		application.NewService(newProxyService(b, profiles.isRunning)),
		application.NewService(newRunService(b)),
		application.NewService(newSettingsService(b)),
	}
}
