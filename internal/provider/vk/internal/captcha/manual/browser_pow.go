package manual

// Adapted from hackdiaz-dev/free-turn-proxy v4.1.0 (465486ba), GPL-3.0.
import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk/internal/browserprofile"
	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk/internal/captcha"
)

var proxyPermit = make(chan struct{}, 1)

func newPowCapturePath() (string, error) {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", errors.New("captcha capture entropy unavailable")
	}
	return "/local-captcha-pow/" + base64.RawURLEncoding.EncodeToString(secret[:]), nil
}
func CapturePowViaProxy(ctx context.Context, redirectURI string, dialer net.Dialer, profile browserprofile.Profile) (string, error) {
	return solveViaProxy(context.WithValue(ctx, quietPresenterKey{}, true), redirectURI, dialer, profile, openBrowser, true)
}
func CapturePowViaProxyWithPresenter(ctx context.Context, redirectURI string, dialer net.Dialer, profile browserprofile.Profile, present func(string)) (string, error) {
	if present == nil {
		present = func(string) {}
	}
	return solveViaProxy(context.WithValue(ctx, quietPresenterKey{}, true), redirectURI, dialer, profile, present, true)
}
func powCaptureHandler(ctx context.Context, expectedPath string, result chan<- string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if ctx.Err() != nil {
			http.Error(w, "expired capture", 410)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.URL.Path), []byte(expectedPath)) != 1 {
			http.NotFound(w, r)
			return
		}
		if r.Host != "localhost:8765" && r.Host != "127.0.0.1:8765" {
			http.Error(w, "forbidden", 403)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://localhost:8765" && origin != "http://127.0.0.1:8765" {
			http.Error(w, "forbidden", 403)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method", 405)
			return
		}
		if r.URL.RawQuery != "" {
			http.Error(w, "query not supported", 400)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if r.ParseForm() != nil {
			http.Error(w, "invalid form", 400)
			return
		}
		value := r.PostForm.Get("pow")
		decoded := captcha.DecodePowEnvelope(value)
		var envelope struct {
			Hash      string          `json:"hash"`
			Telemetry json.RawMessage `json:"telemetry"`
			Error     string          `json:"error"`
		}
		if value == "" || decoded == "" || json.Unmarshal([]byte(decoded), &envelope) != nil || len(envelope.Hash) != 64 || len(envelope.Telemetry) == 0 || envelope.Error != "" {
			http.Error(w, "invalid pow", 400)
			return
		}
		if ctx.Err() != nil {
			http.Error(w, "expired capture", 410)
			return
		}
		notifyKey(result, value)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "captured")
	}
}

// The capture page may compute PoW, but must not consume verification itself.
type captureReadOnlyTransport struct{ http.RoundTripper }

func (t captureReadOnlyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return &http.Response{StatusCode: http.StatusConflict, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("capture only")), Request: r}, nil
	}
	return t.RoundTripper.RoundTrip(r)
}
