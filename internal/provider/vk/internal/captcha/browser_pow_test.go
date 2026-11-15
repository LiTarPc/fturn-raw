package captcha

// Browser proof regression cases adapted from upstream v4.1.0 (465486ba).
import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestValidateBrowserPow(t *testing.T) {
	p := powParams{Input: "challenge-input", Difficulty: 2, Prefix: "v2."}
	value := makeBrowserPowEnvelope(t, p)

	got, err := validateBrowserPow(value, p)
	if err != nil {
		t.Fatalf("validateBrowserPow() error = %v", err)
	}
	if got != value {
		t.Fatalf("validateBrowserPow() = %q, want original envelope", got)
	}
}

func TestValidateBrowserPowRejectsInvalidDifficulty(t *testing.T) {
	for _, difficulty := range []int{-1, 0, sha256.Size*2 + 1} {
		t.Run(strconv.Itoa(difficulty), func(t *testing.T) {
			_, err := validateBrowserPow("v2.e30=", powParams{Prefix: "v2.", Difficulty: difficulty})
			if err == nil {
				t.Fatalf("validateBrowserPow() error = nil for difficulty %d", difficulty)
			}
		})
	}
}

func TestValidateBrowserPowRejectsMismatchedEnvelope(t *testing.T) {
	p := powParams{Input: "challenge-input", Difficulty: 2, Prefix: "v2."}
	value := makeBrowserPowEnvelope(t, p)

	tests := []struct {
		name   string
		value  string
		params powParams
	}{
		{name: "wrong prefix", value: "v1." + strings.TrimPrefix(value, p.Prefix), params: p},
		{name: "wrong challenge input", value: value, params: powParams{Input: "other-input", Difficulty: p.Difficulty, Prefix: p.Prefix}},
		{name: "missing telemetry", value: makeBrowserPowValue(t, p, json.RawMessage(`null`), ""), params: p},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := validateBrowserPow(tc.value, tc.params); err == nil {
				t.Fatal("validateBrowserPow() error = nil, want rejection")
			}
		})
	}
}

func makeBrowserPowEnvelope(t *testing.T, p powParams) string {
	t.Helper()
	telemetry := json.RawMessage(`{"ua":{"ok":true,"result":{"userAgent":"fixture"}}}`)
	telHash, err := telemetryHash(telemetry)
	if err != nil {
		t.Fatal(err)
	}
	return makeBrowserPowValue(t, p, telemetry, telHash)
}

func makeBrowserPowValue(t *testing.T, p powParams, telemetry json.RawMessage, telHash string) string {
	t.Helper()
	hash, nonce := solvePoW(context.Background(), p.Input, p.Difficulty)
	envelope, err := marshalJS(powResult{
		Hash: hash, Nonce: nonce, DurationMs: 12,
		Telemetry: telemetry, TelHash: telHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p.Prefix + base64.StdEncoding.EncodeToString(envelope)
}

func TestBrowserPowRejectsPayloadErrorsWithoutLeakingThem(t *testing.T) {
	p := powParams{Input: "challenge-input", Difficulty: 1, Prefix: "v2."}
	value := makeBrowserPowEnvelope(t, p)
	var v map[string]any
	if json.Unmarshal([]byte(DecodePowEnvelope(value)), &v) != nil {
		t.Fatal("fixture")
	}
	for _, change := range []string{"error", "tel_hash", "hash", "nonce"} {
		t.Run(change, func(t *testing.T) {
			var obj map[string]any
			_ = json.Unmarshal([]byte(DecodePowEnvelope(value)), &obj)
			if change == "nonce" {
				obj[change] = -1
			} else {
				obj[change] = "PRIVATE_PAYLOAD_CANARY"
			}
			raw, _ := json.Marshal(obj)
			_, err := validateBrowserPow(p.Prefix+base64.StdEncoding.EncodeToString(raw), p)
			if err == nil || strings.Contains(err.Error(), "PRIVATE_") {
				t.Fatal("invalid proof accepted or exposed")
			}
		})
	}
}
func TestBrowserPowAlignsActualUAAndClientHints(t *testing.T) {
	p := powParams{Input: "challenge-input", Difficulty: 1, Prefix: "v2."}
	telemetry := json.RawMessage(`{"ua":{"ok":true,"result":{"userAgent":"Fixture Android WebView","userAgentData":{"brands":[{"brand":"Chromium","version":"146"}],"platform":"Android","mobile":true}}}}`)
	value := makeBrowserPowValue(t, p, telemetry, "")
	if _, err := validateBrowserPow(value, p); err != nil {
		t.Fatal(err)
	}
	before := (&captchaSession{}).profile
	after := profileFromBrowserPow(before, value)
	if after.UserAgent != "Fixture Android WebView" || after.SecChUaPlatform != `"Android"` || after.SecChUaMobile != "?1" || !strings.Contains(after.SecChUa, `"Chromium";v="146"`) {
		t.Fatal("profile not aligned")
	}
}

func TestBrowserPowTelemetryNeverAppearsInDiagnostics(t *testing.T) {
	raw := map[string]any{"telemetry": map[string]any{"probe": "PRIVATE_TELEMETRY"}, "pow": "PRIVATE_POW", "tel_hash": "PRIVATE_TELHASH", "error_msg": "Echo PRIVATE_TELEMETRY PRIVATE_POW PRIVATE_TELHASH"}
	if strings.Contains(SafeResponse(raw), "PRIVATE_") {
		t.Fatal("proof metadata exposed")
	}
}

type privateProofFailureClient struct{ tlsclient.HttpClient }

func (privateProofFailureClient) Do(*fhttp.Request) (*fhttp.Response, error) {
	return nil, &url.Error{Op: "GET", URL: "https://id.vk.ru/captcha?session_token=PRIVATE_URL", Err: context.Canceled}
}
func TestBrowserPowTransportErrorDoesNotExposePrivateURL(t *testing.T) {
	s := &captchaSession{ctx: context.Background(), client: privateProofFailureClient{}, pageOrigin: "https://id.vk.ru"}
	_, _, err := s.doRawStatus("GET", "https://id.vk.ru/captcha?session_token=PRIVATE_URL", nil, nil)
	if err == nil || strings.Contains(fmt.Sprint(err), "PRIVATE_") || !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.Canceled) {
		t.Fatal("unsafe transport error or lost cancellation")
	}
}
