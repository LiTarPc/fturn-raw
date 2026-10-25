package main

import (
	"embed"
	"fturnrawui/backend"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailswindows "github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	instance, err := acquireInstance(`Local\FturnRawWindowsUI`)
	if err != nil {
		panic(err)
	}
	if instance == nil {
		return
	}
	defer instance.close()
	app := backend.NewApp()
	instance.listen(app.RequestShow)
	err = wails.Run(&options.App{Title: "fturn Raw", Width: 450, Height: 720, MinWidth: 410, MinHeight: 580, AssetServer: &assetserver.Options{Assets: assets}, BackgroundColour: &options.RGBA{R: 252, G: 250, B: 246, A: 255}, OnStartup: app.Startup, OnDomReady: app.DomReady, OnShutdown: app.Shutdown, OnBeforeClose: app.OnBeforeClose, Bind: []interface{}{app}, Windows: &wailswindows.Options{WebviewIsTransparent: false, WindowIsTranslucent: false}})
	if err != nil {
		panic(err)
	}
}
