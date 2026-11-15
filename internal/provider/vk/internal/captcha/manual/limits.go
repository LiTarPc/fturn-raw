package manual

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk/internal/captcha"
)

// limitTransport shares one stop signal between both proxy routes.
type limitTransport struct {
	rt     http.RoundTripper
	errors chan<- error
	mu     sync.Mutex
	limit  error
}

func (t *limitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	stopped := t.limit
	t.mu.Unlock()
	if stopped != nil {
		return nil, stopped
	}
	resp, err := t.rt.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	var refusal error
	source := captcha.SafeURL(req.URL.String())
	if resp.StatusCode == http.StatusTooManyRequests {
		refusal = captcha.NewRateLimitError(source+" HTTP 429", resp.Header.Get("Retry-After"))
	}
	if refusal != nil || strings.Contains(req.URL.Path, "captchaNotRobot.") {
		body, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(body))
		if readErr != nil || closeErr != nil {
			_ = resp.Body.Close()
			if readErr != nil {
				return nil, readErr
			}
			return nil, closeErr
		}
		payload := body
		if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
			if reader, gzipErr := gzip.NewReader(bytes.NewReader(body)); gzipErr == nil {
				decoded, decodeErr := io.ReadAll(reader)
				_ = reader.Close()
				if decodeErr == nil {
					payload = decoded
				}
			}
		}
		if refusal != nil {
			refusal = captcha.WithResponseBody(refusal, payload)
		}
		var raw map[string]any
		if json.Unmarshal(payload, &raw) == nil && captcha.LimitedResponse(raw) {
			refusal = captcha.WithResponse(captcha.NewRateLimitError(source+" API limit", resp.Header.Get("Retry-After")), raw)
		}
	}
	if refusal != nil {
		t.mu.Lock()
		if t.limit == nil {
			t.limit = refusal
			select {
			case t.errors <- refusal:
			default:
			}
		}
		t.mu.Unlock()
	}
	return resp, nil
}
