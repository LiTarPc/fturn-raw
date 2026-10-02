package backend

import "testing"

type fakeWindow struct {
	hidden, shown, quits int
	minimized            bool
}

func (w *fakeWindow) Show()           { w.shown++; w.minimized = false }
func (w *fakeWindow) Hide()           { w.hidden++ }
func (w *fakeWindow) Minimized() bool { return w.minimized }
func (w *fakeWindow) Minimize()       { w.minimized = true }
func (w *fakeWindow) Quit()           { w.quits++ }
func TestTrayPolicyKeepsWindowReachableAndExplicitExitWorks(t *testing.T) {
	w := &fakeWindow{}
	settings := AppSettings{MinimizeToTray: true}
	d := &desktopController{host: w, settings: func() AppSettings { return settings }}
	if d.beforeClose() || w.hidden != 0 {
		t.Fatal("window hidden without tray")
	}
	w.minimized = true
	d.checkMinimize()
	if w.hidden != 0 {
		t.Fatal("minimized window hidden without tray")
	}
	d.ready.Store(true)
	if !d.beforeClose() || w.hidden != 1 {
		t.Fatal("close should hide with active tray")
	}
	d.show()
	if w.shown != 1 || d.hidden.Load() {
		t.Fatal("tray restore failed")
	}
	w.minimized = true
	d.checkMinimize()
	d.checkMinimize()
	if w.hidden != 2 {
		t.Fatal("minimize should hide exactly once")
	}
	settings.MinimizeToTray = false
	if d.beforeClose() {
		t.Fatal("tray switch disabled should allow real close")
	}
	settings.MinimizeToTray = true
	d.exit()
	d.exit()
	if w.quits != 1 || d.beforeClose() {
		t.Fatal("explicit exit must bypass hide policy once")
	}
}
