package vkauth

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk/internal/captcha"
	"github.com/samosvalishe/free-turn-proxy/internal/statedir"
)

// Local pacing policy; VK does not supply its actual captcha-limit expiry.
const captchaLimitPause = 5 * time.Minute
const captchaPauseFile = "vk_auth_pause.json"

var pauseMu sync.Mutex

type cooldownStore struct{ paths []string }

func newCooldownStore(personaPaths []string) cooldownStore {
	s := cooldownStore{}
	for _, p := range personaPaths {
		s.paths = append(s.paths, filepath.Join(filepath.Dir(p), captchaPauseFile))
	}
	return s
}
func (s cooldownStore) load() int64 {
	var until int64
	now := time.Now().Unix()
	for _, b := range statedir.ReadEach(s.paths) {
		if len(b) > 1024 {
			continue
		}
		var v struct {
			Until int64 `json:"until"`
		}
		if json.Unmarshal(b, &v) == nil && v.Until > now && v.Until <= now+10*365*86400 {
			until = max(until, v.Until)
		}
	}
	return until
}
func (s cooldownStore) save(until int64) bool {
	if len(s.paths) == 0 {
		return true
	}
	pauseMu.Lock()
	defer pauseMu.Unlock()
	until = max(until, s.load())
	data, _ := json.Marshal(struct {
		Until int64 `json:"until"`
	}{until})
	return statedir.WriteFirst(s.paths, data)
}
func (c *Client) rateLimited(streamID int, cause error) error {
	streak := c.rateLimitStreak.Add(1)
	pause := min(captchaLimitPause*time.Duration(1<<min(streak-1, 4)), time.Hour)
	until := time.Now().Add(pause).Unix()
	var providerLimit *captcha.RateLimitError
	if errors.As(cause, &providerLimit) && !providerLimit.RetryAt.IsZero() {
		until = max(until, providerLimit.RetryAt.Unix()+1)
	}
	until = max(until, c.captchaPause.Load())
	until = max(until, c.cooldown.load())
	c.captchaPause.Store(until)
	c.lockout.Store(max(until, c.lockout.Load()))
	if !c.cooldown.save(until) {
		c.log.Warnf("[VK Auth] Could not persist captcha pause")
	}
	c.log.Warnf("[STREAM %d] [VK Auth] CAPTCHA_RATE_LIMIT pause_until=%d streak=%d; requests paused, manual fallback skipped; cause=%v", streamID, until, streak, cause)
	if c.onAuthWait != nil {
		c.onAuthWait()
	} else if c.streamsFn() == 0 {
		return errors.Join(ErrFatalCaptchaNoStreams, captcha.ErrRateLimited, cause)
	}
	return errors.Join(ErrCaptchaWaitRequired, captcha.ErrRateLimited, cause)
}
