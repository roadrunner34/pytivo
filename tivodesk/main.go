// TivoDesk is a desktop app that serves media to TiVos and downloads
// recordings from them. This file is the desktop shell: it starts the
// engine and puts a window and a tray icon in front of it.
package main

import (
	"context"
	"embed"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/energye/systray"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"tivodesk/internal/config"
	"tivodesk/internal/core"
	"tivodesk/internal/discovery"
	"tivodesk/internal/logx"
	"tivodesk/internal/secrets"
	"tivodesk/internal/tivo"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/windows/icon.ico
var trayIcon []byte

// maxLogFile is the size at which the log file is started afresh on launch.
const maxLogFile = 5 << 20

// openLogFile opens the log file next to the configuration. Logging to a
// file is a convenience, so any failure just means there is no file.
func openLogFile(configPath string) *os.File {
	path := filepath.Join(filepath.Dir(configPath), "tivodesk.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if st, err := os.Stat(path); err == nil && st.Size() > maxLogFile {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return nil
	}
	return f
}

// startTray puts an icon in the notification area and returns a function
// that removes it.
func startTray(ctx context.Context) (stop func()) {
	show := func() {
		runtime.WindowUnminimise(ctx)
		runtime.WindowShow(ctx)
	}
	start, stop := systray.RunWithExternalLoop(func() {
		systray.SetIcon(trayIcon)
		systray.SetTooltip("TivoDesk")
		systray.SetOnClick(func(systray.IMenu) { show() })
		systray.SetOnDClick(func(systray.IMenu) { show() })
		systray.SetOnRClick(func(menu systray.IMenu) { menu.ShowMenu() })
		systray.AddMenuItem("Open TivoDesk", "Show the window").Click(show)
		systray.AddSeparator()
		systray.AddMenuItem("Quit", "Stop TivoDesk").Click(func() { runtime.Quit(ctx) })
	}, nil)
	start()
	return stop
}

// addExtraTiVo lists a TiVo given as "Name=host:port". It is a development
// aid; a malformed value is ignored.
func addExtraTiVo(engine *core.Engine, spec string) {
	name, addr, ok := strings.Cut(spec, "=")
	if !ok {
		return
	}
	host, portText, err := net.SplitHostPort(addr)
	port, convErr := strconv.Atoi(portText)
	if err != nil || convErr != nil {
		return
	}
	engine.Registry().Seen(tivo.Box{
		TSN: "FAKE" + portText, Name: name, Address: host, Port: port, Platform: "tcd/Pretend",
	})
}

// fatal reports a startup failure in a dialog, because a windowed program
// has no console to print to.
func fatal(title, message string) {
	err := wails.Run(&options.App{
		Title: title, Width: 1, Height: 1, StartHidden: true,
		AssetServer: &assetserver.Options{Assets: assets},
		OnStartup: func(ctx context.Context) {
			runtime.MessageDialog(ctx, runtime.MessageDialogOptions{
				Type: runtime.ErrorDialog, Title: title, Message: message,
			})
			runtime.Quit(ctx)
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, title+": "+message)
	}
	os.Exit(1)
}

func main() {
	// Two environment variables exist for development and testing, so a
	// trial run cannot touch real settings or real keys:
	//   TIVODESK_CONFIG   path of the settings file to use
	//   TIVODESK_SECRETS  "memory" keeps keys in memory instead of in
	//                     Windows Credential Manager
	//   TIVODESK_DISCOVERY  "off" stops the search for real TiVos, so that
	//                     a test key is never sent to one
	//   TIVODESK_EXTRA_TIVO  "Name=host:port" lists a TiVo that discovery
	//                     cannot find, such as tivodesk-cli's pretend one
	configPath := os.Getenv("TIVODESK_CONFIG")
	if configPath == "" {
		var err error
		if configPath, err = config.DefaultPath(); err != nil {
			fatal("TivoDesk cannot start", "Could not find the settings folder: "+err.Error())
		}
	}
	store := secrets.System()
	if os.Getenv("TIVODESK_SECRETS") == "memory" {
		store = secrets.NewMemory()
	}

	var logOut io.Writer // stays nil, not a nil *os.File, when there is no file
	if f := openLogFile(configPath); f != nil {
		defer f.Close()
		logOut = f
	}
	log := logx.New(2000, logOut)

	opts := core.Options{ConfigPath: configPath, Secrets: store, Log: log, Discover: discovery.Browse}
	if os.Getenv("TIVODESK_DISCOVERY") == "off" {
		opts.Discover = nil
	}
	engine, err := core.New(opts)
	if err != nil {
		fatal("TivoDesk cannot start", fmt.Sprintf(
			"The settings or the download queue could not be opened:\n\n%v\n\nFix or remove the file named above and start TivoDesk again.", err))
	}
	defer engine.Close()
	addExtraTiVo(engine, os.Getenv("TIVODESK_EXTRA_TIVO"))

	app := NewApp(engine)
	var stopTray func()

	err = wails.Run(&options.App{
		Title:            "TivoDesk",
		Width:            1100,
		Height:           720,
		MinWidth:         760,
		MinHeight:        480,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 16, G: 20, B: 28, A: 1},

		// Closing the window hides it; TivoDesk keeps serving from the tray.
		HideWindowOnClose: true,

		// A second launch brings the first one forward and exits.
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "tivodesk-0d6f5f0e-6b0a-4a55-9d57-3f6f0f6f2c11",
			OnSecondInstanceLaunch: func(options.SecondInstanceData) {
				if app.ctx != nil {
					runtime.WindowUnminimise(app.ctx)
					runtime.WindowShow(app.ctx)
				}
			},
		},

		OnStartup: func(ctx context.Context) {
			app.startup(ctx)
			stopTray = startTray(ctx)
		},
		OnShutdown: func(context.Context) {
			app.shutdown()
			if stopTray != nil {
				stopTray()
			}
		},
		Bind: []interface{}{app},
	})
	if err != nil {
		engine.Logger().Error("the window could not be created", "err", err)
	}
}
