package main

import (
	"errors"
	"sync"

	"golang.org/x/sys/windows"
)

// The mutex still guards all startup work. The auto-reset event queues a show
// request even if the first process has not yet created its Wails window.
type singleInstanceGuard struct {
	mutex, show, stop windows.Handle
	done              chan struct{}
	once              sync.Once
}

func acquireInstance(name string) (*singleInstanceGuard, error) {
	show, err := windows.CreateEvent(nil, 0, 0, windows.StringToUTF16Ptr(name+"Show"))
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil, err
	}
	mutex, err := windows.CreateMutex(nil, false, windows.StringToUTF16Ptr(name))
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		signalErr := windows.SetEvent(show)
		if mutex != 0 {
			_ = windows.CloseHandle(mutex)
		}
		_ = windows.CloseHandle(show)
		return nil, signalErr
	}
	if err != nil {
		_ = windows.CloseHandle(show)
		return nil, err
	}
	stop, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		_ = windows.CloseHandle(show)
		_ = windows.CloseHandle(mutex)
		return nil, err
	}
	return &singleInstanceGuard{mutex: mutex, show: show, stop: stop}, nil
}
func (g *singleInstanceGuard) listen(show func()) {
	g.done = make(chan struct{})
	go func() {
		defer close(g.done)
		for {
			result, err := windows.WaitForMultipleObjects([]windows.Handle{g.stop, g.show}, false, windows.INFINITE)
			if err != nil || result != windows.WAIT_OBJECT_0+1 {
				return
			}
			show()
		}
	}()
}
func (g *singleInstanceGuard) close() {
	g.once.Do(func() {
		_ = windows.SetEvent(g.stop)
		if g.done != nil {
			<-g.done
		}
		_ = windows.CloseHandle(g.stop)
		_ = windows.CloseHandle(g.show)
		_ = windows.CloseHandle(g.mutex)
	})
}
