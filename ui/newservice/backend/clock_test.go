package backend

import (
	"testing"
	"time"
)

func TestActiveClockPausesOutagesAndResetsSessions(t *testing.T) {
	var clock connectionClock
	now := time.Now()
	clock.transition("connecting", now)
	if got := clock.seconds(now.Add(15 * time.Second)); got != 0 {
		t.Fatalf("setup counted: %d", got)
	}
	clock.transition("connected", now.Add(15*time.Second))
	clock.transition("connected", now.Add(20*time.Second)) // Additional ready stream must not reset it.
	if got := clock.seconds(now.Add(25 * time.Second)); got != 10 {
		t.Fatalf("active: %d", got)
	}
	clock.transition("reconnecting", now.Add(25*time.Second))
	if got := clock.seconds(now.Add(85 * time.Second)); got != 10 {
		t.Fatalf("outage counted: %d", got)
	}
	clock.transition("connected", now.Add(85*time.Second))
	if got := clock.seconds(now.Add(88 * time.Second)); got != 13 {
		t.Fatalf("resume: %d", got)
	}
	clock.transition("stopping", now.Add(88*time.Second))
	if got := clock.seconds(now.Add(90 * time.Second)); got != 0 {
		t.Fatalf("disconnect did not reset: %d", got)
	}
	clock.transition("connected", now.Add(92*time.Second))
	clock.transition("error", now.Add(95*time.Second))
	if got := clock.seconds(now.Add(97 * time.Second)); got != 0 {
		t.Fatalf("error did not reset: %d", got)
	}
}
