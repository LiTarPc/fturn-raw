package backend

import (
	"os"
	"testing"
	"time"
)

// This opt-in smoke test creates only a tray icon. It never starts the Raw engine or changes routes.
func TestNativeTraySmoke(t *testing.T) {
	if os.Getenv("FTURN_TRAY_SMOKE") != "1" {
		t.Skip("native tray smoke is opt-in")
	}
	ready := make(chan struct{})
	stopped := make(chan struct{})
	tray := &nativeTray{}
	go func() { tray.Run(func() { close(ready) }, func() {}, func() {}, func() {}); close(stopped) }()
	select {
	case <-ready:
	case <-time.After(8 * time.Second):
		t.Fatal("tray did not initialize")
	}
	tray.Update("fturn Raw · проверка трея", "Подключить", true)
	tray.Update("fturn Raw · проверка трея", "Отключить", false)
	tray.Quit()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("native tray event loop did not stop")
	}
}
