package main

import (
	"embed"
	"errors"
	"fturnrawui/backend"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailswindows "github.com/wailsapp/wails/v2/pkg/options/windows"
	"golang.org/x/sys/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	m, err := windows.CreateMutex(nil, false, windows.StringToUTF16Ptr(`Local\FturnRawWindowsUI`))
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if m != 0 {
			windows.CloseHandle(m)
		}
		return
	}
	if err != nil {
		panic(err)
	}
	defer windows.CloseHandle(m)
	app := backend.NewApp()
	err = wails.Run(&options.App{Title: "fturn Raw", Width: 450, Height: 720, MinWidth: 410, MinHeight: 580, AssetServer: &assetserver.Options{Assets: assets}, BackgroundColour: &options.RGBA{R: 255, G: 255, B: 255, A: 255}, OnStartup: app.Startup, OnDomReady: app.DomReady, OnShutdown: app.Shutdown, OnBeforeClose: app.OnBeforeClose, Bind: []interface{}{app}, Windows: &wailswindows.Options{WebviewIsTransparent: false, WindowIsTranslucent: false}})
	if err != nil {
		panic(err)
	}
}
