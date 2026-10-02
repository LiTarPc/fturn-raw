package backend

import "time"

// connectionClock is guarded by its owner's mutex. Reconnection pauses active time.
type connectionClock struct {
	activeSince time.Time
	accumulated time.Duration
}

func (c *connectionClock) transition(state string, now time.Time) {
	if !c.activeSince.IsZero() && state != "connected" {
		c.accumulated += now.Sub(c.activeSince)
		c.activeSince = time.Time{}
	}
	switch state {
	case "connected":
		if c.activeSince.IsZero() {
			c.activeSince = now
		}
	case "connecting", "stopping", "idle", "error":
		c.accumulated = 0
		c.activeSince = time.Time{}
	}
}
func (c *connectionClock) seconds(now time.Time) int64 {
	elapsed := c.accumulated
	if !c.activeSince.IsZero() {
		elapsed += now.Sub(c.activeSince)
	}
	return int64(elapsed / time.Second)
}
