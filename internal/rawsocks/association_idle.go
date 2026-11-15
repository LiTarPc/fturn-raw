package rawsocks

import (
	"context"
	"sync"
	"time"
)

// A separate association clock: expiring a destination flow must not close an
// association carrying traffic to another destination. Both directions count.
type associationActivity struct {
	mu     sync.Mutex
	last   time.Time
	reason string
}

func (a *associationActivity) touch() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.reason == "" {
		a.last = time.Now()
	}
}
func (a *associationActivity) end(reason string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.reason != "" {
		return false
	}
	a.reason = reason
	return true
}
func (a *associationActivity) snapshot() (time.Time, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.last, a.reason
}
func (a *associationActivity) watch(ctx context.Context, idle time.Duration, closeBoth func()) {
	tick := time.NewTicker(min(time.Second, max(time.Millisecond, idle/4)))
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			a.mu.Lock()
			expired := a.reason == "" && time.Since(a.last) >= idle
			if expired {
				a.reason = "udp_idle"
			}
			a.mu.Unlock()
			if expired {
				closeBoth()
				return
			}
		}
	}
}
