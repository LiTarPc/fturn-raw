package backend

import (
	"context"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"os"
)

type wailsWindow struct{ ctx context.Context }

func (w wailsWindow) Show()           { runtime.WindowUnminimise(w.ctx); runtime.WindowShow(w.ctx) }
func (w wailsWindow) Hide()           { runtime.WindowHide(w.ctx) }
func (w wailsWindow) Minimized() bool { return runtime.WindowIsMinimised(w.ctx) }
func (w wailsWindow) Quit()           { runtime.Quit(w.ctx) }
func (a *App) Startup(ctx context.Context) {
	defer close(a.startupDone)
	a.appendVersionLog("startup")
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()
	a.desktop.host = wailsWindow{ctx}
	a.op.Lock()
	_, err := a.routes(context.Background(), "Recover", 0, "full")
	a.op.Unlock()
	if err != nil {
		a.appendErrorLog("Восстановление маршрутов: " + err.Error())
		a.setState(0, "error", err.Error())
	}
	if a.settings.loadErr != nil {
		a.appendErrorLog(a.settings.loadErr.Error())
	}
	if a.settings.get().StartWithWindows && a.settings.loadErr == nil {
		if err := a.settings.startup.SetEnabled(true); err != nil {
			a.appendErrorLog("Обновление пути автозапуска: " + err.Error())
		}
	}
	a.desktop.start()
}
func (a *App) DomReady(ctx context.Context) {
	select {
	case <-a.startupDone:
	case <-ctx.Done():
		return
	}
	s := a.settings.get()
	for _, arg := range os.Args[1:] {
		if arg == "-startup" && !a.showRequested.Load() && s.MinimizeToTray && a.desktop != nil {
			a.desktop.hideWhenReady()
		}
	}
	if s.AutoConnect && a.storageErr == nil && a.settings.loadErr == nil && a.GetSnapshot().State != "error" {
		if err := a.Connect(); err != nil {
			a.appendErrorLog("Авто-подключение: " + err.Error())
			a.desktop.show()
			a.publish()
		}
	}
	a.domReady.Store(true)
	if a.showRequested.Load() && a.desktop != nil {
		a.desktop.show()
	}
}

// RequestShow is safe while the first instance is still starting up.
func (a *App) RequestShow() {
	a.showRequested.Store(true)
	if a.domReady.Load() && a.desktop != nil {
		a.desktop.show()
	}
}
func (a *App) OnBeforeClose(ctx context.Context) bool {
	if a.desktop != nil && a.desktop.beforeClose() {
		return true
	}
	_ = a.Disconnect()
	return false
}
func (a *App) Shutdown(ctx context.Context) {
	a.domReady.Store(false)
	if a.desktop != nil {
		a.desktop.stop()
	}
}
func (a *App) Exit() {
	<-a.startupDone
	if a.desktop != nil {
		a.desktop.exit()
	}
}
