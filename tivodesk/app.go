package main

import (
	"context"
	"errors"
	"os"
	"os/exec"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"tivodesk/internal/config"
	"tivodesk/internal/core"
	"tivodesk/internal/events"
	"tivodesk/internal/logx"
	"tivodesk/internal/store"
	"tivodesk/internal/tivo"
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

// GetTiVos returns the TiVos found on the network.
func (a *App) GetTiVos() []tivo.Box { return a.engine.TiVos() }

// GetNowPlaying returns the recordings held for a TiVo and how fresh
// they are. It does not contact the TiVo.
func (a *App) GetNowPlaying(tsn string) core.NowPlaying { return a.engine.NowPlaying(tsn) }

// RefreshNowPlaying starts reading a TiVo's list in the background.
func (a *App) RefreshNowPlaying(tsn string) error { return a.engine.RefreshNowPlaying(tsn) }

// GetTransfers returns every download, oldest first.
func (a *App) GetTransfers() []store.Job {
	if jobs := a.engine.Transfers(); jobs != nil {
		return jobs
	}
	return []store.Job{}
}

// QueueDownloads adds recordings from a TiVo's list to the download queue
// and returns how many were added.
func (a *App) QueueDownloads(tsn string, ids []string) (int, error) {
	return a.engine.QueueDownloads(tsn, ids, "")
}

// CancelTransfer stops a download that is waiting or under way.
func (a *App) CancelTransfer(id int64) error { return a.engine.CancelTransfer(id) }

// RetryTransfer puts a failed or cancelled download back in the queue.
func (a *App) RetryTransfer(id int64) error { return a.engine.RetryTransfer(id) }

// ClearTransfers removes finished downloads from the list, not from disk.
func (a *App) ClearTransfers() int { return a.engine.ClearTransfers() }

// ShowTransfer opens the folder holding a finished download, with the file
// selected. It takes a transfer rather than a path so that the UI cannot
// be made to open anything else.
func (a *App) ShowTransfer(id int64) error {
	for _, j := range a.engine.Transfers() {
		if j.ID != id {
			continue
		}
		if _, err := os.Stat(j.Path); j.Path == "" || err != nil {
			return errors.New("the file is no longer there")
		}
		// Explorer exits with a failure code even when it works, so its
		// result says nothing.
		return exec.Command("explorer.exe", "/select,", j.Path).Start()
	}
	return errors.New("that transfer is no longer in the list")
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
