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
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()
	a.desktop.host = wailsWindow{ctx}
	a.op.Lock()
	_, err := a.routes(context.Background(), "Remove", 0, "full")
	a.op.Unlock()
	if err != nil {
		a.appendLog("Восстановление маршрутов: " + err.Error())
		a.setState(0, "error", err.Error())
	}
	if a.settings.loadErr != nil {
		a.appendLog(a.settings.loadErr.Error())
	}
	if a.settings.get().StartWithWindows && a.settings.loadErr == nil {
		if err := a.settings.startup.SetEnabled(true); err != nil {
			a.appendLog("Обновление пути автозапуска: " + err.Error())
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
		if arg == "-startup" && s.MinimizeToTray && a.desktop != nil {
			a.desktop.hideWhenReady()
		}
	}
	if s.AutoConnect && a.storageErr == nil && a.settings.loadErr == nil && a.GetSnapshot().State != "error" {
		if err := a.Connect(); err != nil {
			a.appendLog("Авто-подключение: " + err.Error())
			a.desktop.show()
			a.publish()
		}
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
