package main

import (
	"context"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"tivodesk/internal/config"
	"tivodesk/internal/core"
	"tivodesk/internal/events"
	"tivodesk/internal/logx"
)

// App is the bridge between the window and the engine. Its exported methods
// are callable from the UI. It holds no logic of its own: each method
// passes straight through to the engine, or to a native dialog.
type App struct {
	ctx        context.Context
	engine     core.API
	stopEvents func()
}

// NewApp returns the bridge for an engine.
func NewApp(engine core.API) *App { return &App{engine: engine} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	// Forward engine events to the UI under the same topic names.
	a.stopEvents = a.engine.Events().Subscribe(func(e events.Event) {
		runtime.EventsEmit(ctx, e.Topic, e.Data)
	})
}

func (a *App) shutdown() {
	if a.stopEvents != nil {
		a.stopEvents()
	}
}

// GetInfo describes the running app.
func (a *App) GetInfo() core.Info { return a.engine.Info() }

// GetConfig returns the current settings.
func (a *App) GetConfig() *config.Config { return a.engine.Config() }

// SaveConfig stores new settings and returns any that will not work as
// written.
func (a *App) SaveConfig(c *config.Config) ([]config.Problem, error) {
	problems, err := a.engine.SaveConfig(c)
	if problems == nil {
		problems = []config.Problem{}
	}
	return problems, err
}

// GetProblems returns the current settings that will not work as written.
func (a *App) GetProblems() []config.Problem {
	if problems := a.engine.Problems(); problems != nil {
		return problems
	}
	return []config.Problem{}
}

// HasMAK reports whether a Media Access Key is stored.
func (a *App) HasMAK() bool { return a.engine.HasMAK() }

// SetMAK stores the Media Access Key.
func (a *App) SetMAK(mak string) error { return a.engine.SetMAK(mak) }

// ClearMAK removes the stored Media Access Key.
func (a *App) ClearMAK() error { return a.engine.ClearMAK() }

// PreviewImport reports what importing a pyTivo.conf would do.
func (a *App) PreviewImport(path string) (*core.ImportPreview, error) {
	return a.engine.PreviewImport(path)
}

// ApplyImport replaces the settings with those in a pyTivo.conf.
func (a *App) ApplyImport(path string) (*core.ImportPreview, error) {
	return a.engine.ApplyImport(path)
}

// GetLogs returns the recent log entries, oldest first.
func (a *App) GetLogs() []logx.Entry { return a.engine.Logs() }

// ChooseImportFile asks the user for a pyTivo.conf. It returns an empty
// string if they cancel.
func (a *App) ChooseImportFile() (string, error) {
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Choose your pyTivo.conf",
		Filters: []runtime.FileFilter{
			{DisplayName: "pyTivo settings (*.conf)", Pattern: "*.conf"},
			{DisplayName: "All files", Pattern: "*.*"},
		},
	})
}

// ChooseFolder asks the user for a folder. It returns an empty string if
// they cancel.
func (a *App) ChooseFolder(title string) (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: title})
}

// ChooseFile asks the user for a program file, such as ffmpeg.exe.
func (a *App) ChooseFile(title string) (string, error) {
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: title,
		Filters: []runtime.FileFilter{
			{DisplayName: "Programs (*.exe)", Pattern: "*.exe"},
			{DisplayName: "All files", Pattern: "*.*"},
		},
	})
}
