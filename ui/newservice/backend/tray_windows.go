package backend

import (
	"embed"
	"github.com/energye/systray"
	"runtime"
)

//go:embed assets/tray.ico
var trayAssets embed.FS

type nativeTray struct{ action *systray.MenuItem }

func (t *nativeTray) Run(ready func(), show func(), toggle func(), exit func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	systray.Run(func() {
		icon, _ := trayAssets.ReadFile("assets/tray.ico")
		systray.SetIcon(icon)
		systray.SetTooltip("fturn Raw")
		systray.SetOnClick(func(systray.IMenu) { go show() })
		systray.SetOnDClick(func(systray.IMenu) { go show() })
		systray.SetOnRClick(func(menu systray.IMenu) { _ = menu.ShowMenu() })
		open := systray.AddMenuItem("Открыть fturn Raw", "Показать окно")
		open.Click(func() { go show() })
		t.action = systray.AddMenuItem("Подключить", "Управление соединением")
		t.action.Click(func() { go toggle() })
		systray.AddSeparator()
		quit := systray.AddMenuItem("Выход", "Отключить Raw и закрыть приложение")
		quit.Click(func() { go exit() })
		ready()
	}, func() {})
}
func (t *nativeTray) Update(tooltip, action string, enabled bool) {
	systray.SetTooltip(tooltip)
	t.action.SetTitle(action)
	if enabled {
		t.action.Enable()
	} else {
		t.action.Disable()
	}
}
func (t *nativeTray) Quit() { systray.Quit() }
