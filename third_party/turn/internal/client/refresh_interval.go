// SPDX-License-Identifier: MIT
package client

import "time"

// Earlier maintenance must never run later than the upstream half-lifetime.
func allocationRefreshInterval(lifetime, requested time.Duration) time.Duration {
	interval := lifetime / 2
	if requested > 0 && requested < interval {
		return requested
	}
	return interval
}
