package vkauth

import (
	"encoding/json"
	"errors"
	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk/internal/captcha"
	"github.com/samosvalishe/free-turn-proxy/internal/statedir"
	"path/filepath"
	"sync"
	"time"
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
		if json.Unmarshal(b, &v) == nil && v.Until > now && v.Until <= now+86400 {
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
func (c *Client) captchaLimited(streamID int) error {
	until := time.Now().Add(captchaLimitPause).Unix()
	until = max(until, c.cooldown.load())
	c.captchaPause.Store(until)
	c.lockout.Store(max(until, c.lockout.Load()))
	if !c.cooldown.save(until) {
		c.log.Warnf("[VK Auth] Could not persist captcha pause")
	}
	c.log.Warnf("[VK Auth] CAPTCHA_RATE_LIMIT pause_until=%d; requests paused, manual fallback skipped", until)
	if c.streamsFn() == 0 {
		return errors.Join(ErrFatalCaptchaNoStreams, captcha.ErrRateLimited)
	}
	return errors.Join(ErrCaptchaWaitRequired, captcha.ErrRateLimited)
}
