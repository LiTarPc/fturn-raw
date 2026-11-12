package vkauth

// Browser PoW path adapted from hackdiaz-dev/free-turn-proxy v4.1.0 (465486ba).
import (
	"context"
	"errors"
	"fmt"
	"time"

	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk/internal/browserprofile"
	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk/internal/captcha"
	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk/internal/captcha/manual"
)

const browserPowCaptureTimeout = 12 * time.Second

func (c *Client) defaultAutoSolve(ctx context.Context, e *captcha.Error, streamID int, client tlsclient.HttpClient, profile browserprofile.Profile) (string, error) {
	if e == nil || e.SessionToken == "" || e.RedirectURI == "" {
		return "", fmt.Errorf("missing fields for automatic captcha verification")
	}
	if c.advancedCaptcha {
		capture := c.powCapture
		if capture == nil {
			capture = manual.CapturePowViaProxy
		}
		captureCtx, cancel := context.WithTimeout(ctx, browserPowCaptureTimeout)
		proof, err := capture(captureCtx, e.RedirectURI, c.dialer, profile)
		if captureCtx.Err() != nil {
			proof = ""
			err = captureCtx.Err()
		}
		cancel()
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if errors.Is(err, captcha.ErrRateLimited) || errors.Is(err, captcha.ErrBotRejected) {
			return "", err
		}
		if err == nil && proof != "" {
			c.log.Infof("[STREAM %d] [Captcha] event=BROWSER_POW_CAPTURE outcome=captured", streamID)
			// Validation occurs against this challenge's page BEFORE any verification POST.
			return captcha.SolveWithBrowserPow(ctx, e, streamID, client, profile, c.log, proof)
		}
		c.log.Infof("[STREAM %d] [Captcha] event=BROWSER_POW_CAPTURE outcome=unavailable fallback=legacy", streamID)
	}
	return captcha.Solve(ctx, e, streamID, client, profile, c.log)
}
