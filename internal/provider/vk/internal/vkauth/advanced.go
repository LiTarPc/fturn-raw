package vkauth

import (
	"context"
	"errors"
	"time"

	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk/internal/browserprofile"
	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk/internal/captcha"
)

// Advanced verification never rotates identity on a solver failure. It permits
// one automatic attempt and one fresh manual challenge, then explicit user wait.
func (c *Client) solveAdvancedCaptcha(ctx context.Context, httpClient tlsclient.HttpClient, profile browserprofile.Profile, streamID int, link, name, access string, e *captcha.Error) (string, error) {
	attempt := c.captchaAttempt
	manual := c.manualOnly || c.autoRejected.Load() || attempt > 0
	if attempt > 1 || (manual && c.advancedManualUsed) {
		return "", c.waitAdvanced()
	}
	c.captchaAttempt++
	var token string
	var err error
	mode := "auto"
	if manual {
		mode = "manual"
		c.advancedManualUsed = true
		if c.manualSolve == nil {
			return "", c.waitAdvanced()
		}
		manualCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		token, err = c.manualSolve(manualCtx, e, c.dialer, profile)
		cancel()
	} else {
		solve := c.autoSolver
		if solve == nil {
			solve = c.defaultAutoSolve
		}
		token, err = solve(ctx, e, streamID, httpClient, profile)
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err == nil && token == "" {
		err = errors.New("verification returned no result")
	}
	if errors.Is(err, captcha.ErrRateLimited) {
		return "", c.rateLimited(streamID, err)
	}
	if err != nil {
		if !manual {
			if errors.Is(err, captcha.ErrBotRejected) {
				c.autoRejected.Store(true)
			}
			c.log.Warnf("[STREAM %d] [Captcha] event=ADVANCED_RESULT mode=auto outcome=manual_required", streamID)
			if c.manualSolve == nil {
				return "", c.waitAdvanced()
			}
			// The automatic stage may have consumed a challenge, including a rejected
			// check or a profile/input mismatch. Refresh once in the same auth session.
			return "", errManualFreshChallenge
		}
		c.log.Warnf("[STREAM %d] [Captcha] event=ADVANCED_RESULT mode=manual outcome=waiting_user", streamID)
		return "", c.waitAdvanced()
	}
	c.log.Infof("[STREAM %d] [Captcha] event=ADVANCED_RESULT mode=%s outcome=token_received", streamID, mode)
	if e.CaptchaAttempt == "" || e.CaptchaAttempt == "0" {
		e.CaptchaAttempt = "1"
	}
	return buildCaptchaRetryData(link, name, access, e, token), nil
}
func (c *Client) waitAdvanced() error {
	if c.onAuthWait != nil {
		c.onAuthWait()
	} else {
		c.engageLockout(time.Minute)
	}
	return ErrCaptchaWaitRequired
}
