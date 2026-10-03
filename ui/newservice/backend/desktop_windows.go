package backend

import (
	"fmt"
	"sync/atomic"
	"time"
)

// WindowHost separates desktop policy from Wails. TrayDriver separates it from Win32.
type windowHost interface {
	Show()
	Hide()
	Minimized() bool
	Quit()
}
type trayDriver interface {
	Run(ready func(), show func(), toggle func(), exit func())
	Update(tooltip, action string, enabled bool)
	Quit()
}
type desktopController struct {
	host        windowHost
	tray        trayDriver
	snapshot    func() Snapshot
	settings    func() AppSettings
	toggle      func()
	log         func(string)
	ready       atomic.Bool
	hidden      atomic.Bool
	exiting     atomic.Bool
	stopped     atomic.Bool
	hideOnReady atomic.Bool
}

func (d *desktopController) start() {
	go d.tray.Run(func() {
		if d.stopped.Load() {
			d.tray.Quit()
			return
		}
		d.ready.Store(true)
		if d.hideOnReady.Load() {
			d.beforeClose()
		}
	}, d.show, d.toggle, d.exit)
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		ticks := 0
		for range ticker.C {
			if d.stopped.Load() {
				return
			}
			ticks++
			if !d.ready.Load() {
				if ticks == 20 {
					d.log("Трей недоступен: окно останется доступным через панель задач.")
				}
				continue
			}
			d.checkMinimize()
			if ticks%4 == 0 {
				s := d.snapshot()
				action := "Подключить"
				if connectionRunning(s.State) {
					action = "Отключить"
				}
				d.tray.Update(trayTooltip(s), action, s.State != "stopping")
			}
		}
	}()
}
func connectionRunning(state string) bool {
	return state == "connected" || state == "connecting" || state == "reconnecting" || state == "stopping"
}
func (d *desktopController) canHide() bool {
	return d.ready.Load() && d.settings().MinimizeToTray && !d.exiting.Load()
}
func (d *desktopController) beforeClose() bool {
	if !d.canHide() {
		return false
	}
	d.hidden.Store(true)
	d.host.Hide()
	return true
}
func (d *desktopController) checkMinimize() {
	if d.canHide() && !d.hidden.Load() && d.host.Minimized() {
		d.hidden.Store(true)
		d.host.Hide()
	}
}
func (d *desktopController) show() { d.hidden.Store(false); d.host.Show() }
func (d *desktopController) exit() {
	if d.exiting.CompareAndSwap(false, true) {
		d.host.Quit()
	}
}
func (d *desktopController) stop() {
	d.stopped.Store(true)
	if d.ready.Swap(false) {
		d.tray.Quit()
	}
}

func (d *desktopController) hideWhenReady() {
	d.hideOnReady.Store(true)
	if d.ready.Load() {
		d.beforeClose()
	}
}

func trayTooltip(s Snapshot) string {
	label := map[string]string{"idle": "Отключён", "connected": "Подключён", "connecting": "Подключение", "reconnecting": "Переподключение", "error": "Ошибка", "stopping": "Отключение"}[s.State]
	seconds := s.ElapsedSeconds
	text := fmt.Sprintf("fturn Raw · %s · %02d:%02d:%02d", label, seconds/3600, (seconds/60)%60, seconds%60)
	if connectionRunning(s.State) {
		text += fmt.Sprintf(" · Потоки: %d/%d", s.Ready, s.Profile.Streams)
	}
	return text
}
