package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestSingleInstanceHelper(t *testing.T) {
	name := os.Getenv("FTURN_INSTANCE_TEST")
	if name == "" {
		return
	}
	guard, err := acquireInstance(name)
	if err != nil {
		os.Exit(2)
	}
	if guard != nil {
		guard.close()
		os.Exit(3)
	}
	os.Exit(0)
}
func TestSecondProcessSignalsExistingInstance(t *testing.T) {
	name := fmt.Sprintf("Local\\FturnRawTest-%d-%d", os.Getpid(), time.Now().UnixNano())
	first, err := acquireInstance(name)
	if err != nil || first == nil {
		t.Fatalf("first: %v", err)
	}
	defer first.close()
	runSecond := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSingleInstanceHelper$")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		cmd.Env = append(os.Environ(), "FTURN_INSTANCE_TEST="+name)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("second: %v %s", err, out)
		}
	}
	// A second launch during startup must not lose its show request.
	runSecond()
	shown := make(chan struct{}, 1)
	first.listen(func() { shown <- struct{}{} })
	for i := 0; i < 2; i++ {
		if i > 0 {
			runSecond()
		}
		select {
		case <-shown:
		case <-time.After(3 * time.Second):
			t.Fatal("show request missing")
		}
	}
	first.close()
	replacement, err := acquireInstance(name)
	if err != nil || replacement == nil {
		t.Fatalf("mutex not released: %v", err)
	}
	replacement.close()
}
