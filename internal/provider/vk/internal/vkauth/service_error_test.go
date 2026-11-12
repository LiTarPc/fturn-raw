package vkauth

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/samosvalishe/free-turn-proxy/internal/provider"
	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk/internal/browserprofile"
	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk/internal/captcha"
)

func TestServiceErrorAfterManualCaptchaPausesAllCredentialGroups(t *testing.T) {
	logger := &outcomeLogger{}
	c := New(Config{
		ManualOnly: true, Log: logger, StreamsPerCache: 7,
		Credentials: []VKCredentials{{ClientID: "a"}, {ClientID: "b"}},
	})
	c.minFetchIntervalFn = func() time.Duration { return 0 }
	manualCalls, requests, chains := 0, 0, 0
	c.manualSolve = func(context.Context, *captcha.Error, net.Dialer, browserprofile.Profile) (string, error) {
		manualCalls++
		return "PRIVATE_RESULT", nil
	}
	client := &authResponseClient{respond: func(*fhttp.Request) *fhttp.Response {
		requests++
		if requests == 1 {
			return authTestResponse(200, `{"error":{"error_code":14,"error_msg":"Captcha needed","redirect_uri":"https://id.vk.ru/captcha?session_token=PRIVATE_SESSION"}}`)
		}
		return authTestResponse(200, `{"error":{"error_code":10,"error_msg":"Internal server error","request_params":[{"key":"success_token","value":"PRIVATE_RESULT"}]}}`)
	}}
	c.tokenChain = func(ctx context.Context, link string, streamID int, creds VKCredentials, _ tlsclient.CookieJar) (string, string, []string, error) {
		chains++
		_, err := c.fetchCallToken(ctx, client, c.currentPersona(), streamID, link, "name", "PRIVATE_ACCESS", creds)
		return "", "", nil, err
	}
	persona := c.currentPersona()
	for _, streamID := range []int{1, 8, 15} {
		_, _, _, err := c.GetCredentials(context.Background(), "test", streamID)
		if !errors.Is(err, ErrVKServiceUnavailable) || !errors.Is(err, provider.ErrBackoffActive) {
			t.Fatalf("stream=%d err=%v", streamID, err)
		}
		if errors.Is(err, ErrCaptchaWaitRequired) || errors.Is(err, provider.ErrFatalNoStreams) {
			t.Fatalf("service failure treated as captcha failure: %v", err)
		}
	}
	if chains != 1 || requests != 2 || manualCalls != 1 {
		t.Fatalf("chains=%d requests=%d manual=%d", chains, requests, manualCalls)
	}
	if c.currentPersona().VisitorID != persona.VisitorID || c.captchaPause.Load() != 0 || c.lockout.Load() != 0 {
		t.Fatal("service failure changed captcha state or persona")
	}
	if remaining := time.Until(time.Unix(c.BackoffUntilUnix(), 0)); remaining < 28*time.Second || remaining > 30*time.Second {
		t.Fatalf("unexpected service pause: %s", remaining)
	}
	logs := strings.Join(logger.lines, "\n")
	for _, want := range []string{"outcome=service_unavailable", "VK_SERVICE_UNAVAILABLE", "credential fallback skipped"} {
		if !strings.Contains(logs, want) {
			t.Errorf("missing %q in logs: %s", want, logs)
		}
	}
	if strings.Contains(logs, "PRIVATE_") || strings.Contains(logs, "outcome=rejected") {
		t.Fatalf("misleading or unsafe log: %s", logs)
	}

	// Cached working groups remain usable during the pause.
	cache := c.store.Get(22)
	cache.creds = TurnCredentials{Username: "cached", Password: "pass", ServerAddrs: []string{"relay:3478"}, Link: "test", ExpiresAt: time.Now().Add(time.Minute)}
	user, _, _, err := c.GetCredentials(context.Background(), "test", 22)
	if err != nil || user != "cached" || chains != 1 {
		t.Fatalf("cached credentials blocked: user=%q chains=%d err=%v", user, chains, err)
	}

	// Once the pause expires, authorization can proceed normally.
	c.servicePauseUntil.Store(time.Now().Add(-time.Second).Unix())
	c.tokenChain = func(context.Context, string, int, VKCredentials, tlsclient.CookieJar) (string, string, []string, error) {
		chains++
		return "recovered", "pass", []string{"relay:3478"}, nil
	}
	user, _, _, err = c.GetCredentials(context.Background(), "test", 1)
	if err != nil || user != "recovered" || chains != 2 {
		t.Fatalf("no recovery after pause: user=%q chains=%d err=%v", user, chains, err)
	}
}

func TestTokenChainStopsOnServiceErrorBeforeRequestingCallToken(t *testing.T) {
	c := New(Config{})
	client := &authResponseClient{respond: func(req *fhttp.Request) *fhttp.Response {
		switch req.URL.Path {
		case "/call/join/test":
			return authTestResponse(200, "<html>join</html>")
		case "/":
			return authTestResponse(200, `{"data":{"access_token":"test"}}`)
		case "/method/calls.getCallPreview":
			return authTestResponse(200, `{"error":{"error_code":10,"error_msg":"Internal server error"}}`)
		default:
			t.Errorf("request after service failure: %s", req.URL.Path)
			return authTestResponse(200, `{"response":{}}`)
		}
	}}
	_, _, _, err := c.getTokenChainWithClient(context.Background(), "test", 1, VKCredentials{}, c.currentPersona(), client)
	if !errors.Is(err, ErrVKServiceUnavailable) || len(client.calls) != 3 {
		t.Fatalf("calls=%v err=%v", client.calls, err)
	}
}
