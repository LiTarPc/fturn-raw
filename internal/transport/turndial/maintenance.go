package turndial

import (
	"strings"
	"sync"
	"time"
)

// MaintenanceState contains only counters and monotonic ages, never TURN
// usernames, nonces, addresses or STUN payloads.
type MaintenanceState struct {
	mu                                                                    sync.Mutex
	refreshAttempt, refreshOK, refreshFail, bindAttempt, bindOK, bindFail uint64
	lastRefresh, lastBind                                                 time.Time
}
type MaintenanceSnapshot struct {
	RefreshAttempt, RefreshOK, RefreshFail, BindAttempt, BindOK, BindFail uint64
	RefreshAgeMS, BindAgeMS                                               int64
}

func (s *MaintenanceState) note(message string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case strings.HasPrefix(message, "Send refresh request"):
		s.refreshAttempt++
	case strings.HasPrefix(message, "Updated lifetime:"):
		s.refreshOK++
		s.lastRefresh = time.Now()
	case strings.Contains(message, "Failed to refresh allocation"):
		s.refreshFail++
	case strings.HasPrefix(message, "ChannelBind request:"):
		s.bindAttempt++
	case strings.Contains(message, permOKMarker):
		s.bindOK++
		s.lastBind = time.Now()
	case strings.Contains(message, permFailMarker), strings.Contains(message, permSavedBind400Marker):
		s.bindFail++
	}
}
func (s *MaintenanceState) Snapshot() MaintenanceSnapshot {
	out := MaintenanceSnapshot{RefreshAgeMS: -1, BindAgeMS: -1}
	if s == nil {
		return out
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out.RefreshAttempt = s.refreshAttempt
	out.RefreshOK = s.refreshOK
	out.RefreshFail = s.refreshFail
	out.BindAttempt = s.bindAttempt
	out.BindOK = s.bindOK
	out.BindFail = s.bindFail
	if !s.lastRefresh.IsZero() {
		out.RefreshAgeMS = time.Since(s.lastRefresh).Milliseconds()
	}
	if !s.lastBind.IsZero() {
		out.BindAgeMS = time.Since(s.lastBind).Milliseconds()
	}
	return out
}
