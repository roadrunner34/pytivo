// Spike S0-3: can Wails v2 plus a third-party tray library give TivoDesk a
// tray icon, close-to-tray, and a single instance? Throwaway code.
package main

import (
	"context"
	"embed"

	"github.com/energye/systray"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/windows/icon.ico
var trayIcon []byte

// showWindow brings the main window back from the tray or from minimized.
func showWindow(ctx context.Context) {
	runtime.WindowUnminimise(ctx)
	runtime.WindowShow(ctx)
}

// startTray puts an icon in the notification area and returns a function
// that removes it. The tray runs its own message loop next to Wails'.
func startTray(ctx context.Context) (stop func()) {
	start, stop := systray.RunWithExternalLoop(func() {
		systray.SetIcon(trayIcon)
		systray.SetTooltip("TivoDesk tray spike")
		systray.SetOnClick(func(systray.IMenu) { showWindow(ctx) })
		systray.SetOnDClick(func(systray.IMenu) { showWindow(ctx) })
		systray.SetOnRClick(func(menu systray.IMenu) { menu.ShowMenu() })

		systray.AddMenuItem("Open TivoDesk", "Show the window").Click(func() { showWindow(ctx) })
		systray.AddSeparator()
		systray.AddMenuItem("Quit", "Exit TivoDesk").Click(func() { runtime.Quit(ctx) })
	}, nil)
	start()
	return stop
}

func main() {
	app := NewApp()
	var stopTray func()

	err := wails.Run(&options.App{
		Title:  "TivoDesk tray spike",
		Width:  900,
		Height: 600,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},

		// Closing the window hides it; the app keeps running in the tray.
		HideWindowOnClose: true,

		// A second launch hands off to the first instance and exits.
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "tivodesk-tray-spike-5f0f3c1e",
			OnSecondInstanceLaunch: func(options.SecondInstanceData) {
				showWindow(app.ctx)
			},
		},

		OnStartup: func(ctx context.Context) {
			app.startup(ctx)
			stopTray = startTray(ctx)
		},
		OnShutdown: func(context.Context) {
			if stopTray != nil {
				stopTray()
			}
		},
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
