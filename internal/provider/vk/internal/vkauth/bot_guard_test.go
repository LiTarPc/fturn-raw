package vkauth

import (
	"context"
	"errors"
	"fmt"
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

func TestBotRefusalHandsFreshChallengeToUserWithoutRotatingIdentity(t *testing.T) {
	logger := &outcomeLogger{}
	c := New(Config{Log: logger, StreamsPerCache: 7, FingerprintSeed: "bot-regression", Credentials: []VKCredentials{{ClientID: "first"}, {ClientID: "second"}}})
	c.minFetchIntervalFn = func() time.Duration { return 0 }
	before := c.currentPersona()
	automatic, manual, requests, chains := 0, 0, 0, 0
	c.autoSolver = func(context.Context, *captcha.Error, int, tlsclient.HttpClient, browserprofile.Profile) (string, error) {
		automatic++
		return "", fmt.Errorf("%w: status=BOT", captcha.ErrBotRejected)
	}
	c.manualSolve = func(_ context.Context, e *captcha.Error, _ net.Dialer, p browserprofile.Profile) (string, error) {
		manual++
		if e.SessionToken == "PRIVATE_CONSUMED" {
			t.Error("browser received consumed automatic challenge")
		}
		if p.VisitorID != before.VisitorID {
			t.Error("identity changed before manual verification")
		}
		return "PRIVATE_SUCCESS", nil
	}
	client := &authResponseClient{respond: func(req *fhttp.Request) *fhttp.Response {
		requests++
		switch requests {
		case 1:
			return authTestResponse(200, `{"error":{"error_code":14,"error_msg":"Captcha needed","redirect_uri":"https://id.vk.ru/captcha?session_token=PRIVATE_CONSUMED"}}`)
		case 2, 4, 6:
			return authTestResponse(200, `{"error":{"error_code":14,"error_msg":"Captcha needed","redirect_uri":"https://id.vk.ru/captcha?session_token=PRIVATE_FRESH"}}`)
		default:
			return authTestResponse(200, `{"response":{"token":"PRIVATE_CALL"}}`)
		}
	}}
	c.tokenChain = func(ctx context.Context, link string, id int, creds VKCredentials, _ tlsclient.CookieJar) (string, string, []string, error) {
		chains++
		if creds.ClientID != "first" {
			t.Error("client_id fallback after BOT")
		}
		_, err := c.fetchCallToken(ctx, client, c.currentPersona(), id, link, "name", "PRIVATE_ACCESS", creds)
		return "user", "pass", []string{"relay:3478"}, err
	}
	for _, id := range []int{1, 8, 15} {
		if _, _, _, err := c.GetCredentials(context.Background(), "test", id); err != nil {
			t.Fatalf("stream=%d: %v", id, err)
		}
	}
	if automatic != 1 || manual != 3 || requests != 7 || chains != 3 {
		t.Fatalf("automatic=%d manual=%d requests=%d chains=%d", automatic, manual, requests, chains)
	}
	if c.currentPersona().VisitorID != before.VisitorID || c.identity.Gen != 0 {
		t.Fatal("BOT changed identity")
	}
	if !c.autoRejected.Load() || c.BackoffUntilUnix() != 0 {
		t.Fatal("incorrect automatic-check or backoff state")
	}
	if _, _, _, err := c.GetCredentials(context.Background(), "test", 1); err != nil || requests != 7 {
		t.Fatalf("cache not reused: requests=%d err=%v", requests, err)
	}
	logs := strings.Join(logger.lines, "\n")
	if strings.Contains(logs, "PRIVATE_") || strings.Contains(logs, "Persona burned") || strings.Contains(logs, "persona burned") {
		t.Fatal("secret or persona rotation in logs")
	}
	if !strings.Contains(logs, "CAPTCHA_BOT_REJECTED") {
		t.Fatal("missing explicit BOT refusal diagnostic")
	}
}

func TestBotManualFailurePausesAllGroupsWithoutMoreAutoAttempts(t *testing.T) {
	c := New(Config{StreamsPerCache: 7, Credentials: []VKCredentials{{ClientID: "first"}, {ClientID: "second"}}})
	c.minFetchIntervalFn = func() time.Duration { return 0 }
	automatic, manual, requests, chains := 0, 0, 0, 0
	c.autoSolver = func(context.Context, *captcha.Error, int, tlsclient.HttpClient, browserprofile.Profile) (string, error) {
		automatic++
		return "", captcha.ErrBotRejected
	}
	c.manualSolve = func(context.Context, *captcha.Error, net.Dialer, browserprofile.Profile) (string, error) {
		manual++
		return "", context.DeadlineExceeded
	}
	client := &authResponseClient{respond: func(*fhttp.Request) *fhttp.Response {
		requests++
		return authTestResponse(200, `{"error":{"error_code":14,"error_msg":"Captcha needed","redirect_uri":"https://id.vk.ru/captcha?session_token=PRIVATE_SESSION"}}`)
	}}
	c.tokenChain = func(ctx context.Context, link string, id int, creds VKCredentials, _ tlsclient.CookieJar) (string, string, []string, error) {
		chains++
		_, err := c.fetchCallToken(ctx, client, c.currentPersona(), id, link, "name", "PRIVATE_ACCESS", creds)
		return "", "", nil, err
	}
	before := c.currentPersona()
	for _, id := range []int{1, 8, 15} {
		_, _, _, err := c.GetCredentials(context.Background(), "test", id)
		if !errors.Is(err, ErrCaptchaWaitRequired) || !errors.Is(err, provider.ErrBackoffActive) {
			t.Fatalf("stream=%d err=%v", id, err)
		}
	}
	if automatic != 1 || manual != 1 || requests != 2 || chains != 1 {
		t.Fatalf("auto=%d manual=%d requests=%d chains=%d", automatic, manual, requests, chains)
	}
	if remaining := time.Until(time.Unix(c.BackoffUntilUnix(), 0)); remaining < 58*time.Second || remaining > time.Minute {
		t.Fatalf("pause=%s", remaining)
	}
	if c.networkPauseUntil.Load() != 0 || c.currentPersona().VisitorID != before.VisitorID {
		t.Fatal("manual timeout confused with network failure or identity rotation")
	}
	// Working cached groups must remain usable.
	cache := c.store.Get(22)
	cache.creds = TurnCredentials{Username: "cached", Password: "pass", ServerAddrs: []string{"relay:3478"}, Link: "test", ExpiresAt: time.Now().Add(time.Minute)}
	if user, _, _, err := c.GetCredentials(context.Background(), "test", 22); err != nil || user != "cached" {
		t.Fatalf("cached group blocked: %v", err)
	}
}

func TestBotWithoutManualIntegrationDoesNotRestartAuthorization(t *testing.T) {
	c := New(Config{StreamsPerCache: 7})
	c.minFetchIntervalFn = func() time.Duration { return 0 }
	automatic, chains := 0, 0
	c.autoSolver = func(context.Context, *captcha.Error, int, tlsclient.HttpClient, browserprofile.Profile) (string, error) {
		automatic++
		return "", captcha.ErrBotRejected
	}
	c.tokenChain = func(ctx context.Context, _ string, id int, _ VKCredentials, _ tlsclient.CookieJar) (string, string, []string, error) {
		chains++
		_, err := c.solveCaptcha(ctx, nil, c.currentPersona(), id, "test", "name", "access", testCaptchaErr())
		return "", "", nil, err
	}
	for _, id := range []int{1, 8, 15} {
		c.lockout.Store(0)
		_, _, _, err := c.GetCredentials(context.Background(), "test", id)
		if !errors.Is(err, ErrCaptchaWaitRequired) {
			t.Fatalf("stream=%d err=%v", id, err)
		}
	}
	if automatic != 1 || chains != 1 || c.identity.Gen != 0 {
		t.Fatalf("auto=%d chains=%d gen=%d", automatic, chains, c.identity.Gen)
	}
}

func TestRepeatedManualChallengeAfterBotIsBounded(t *testing.T) {
	c := New(Config{})
	automatic, manual, requests := 0, 0, 0
	c.autoSolver = func(context.Context, *captcha.Error, int, tlsclient.HttpClient, browserprofile.Profile) (string, error) {
		automatic++
		return "", captcha.ErrBotRejected
	}
	c.manualSolve = func(context.Context, *captcha.Error, net.Dialer, browserprofile.Profile) (string, error) {
		manual++
		return "PRIVATE_RESULT", nil
	}
	client := &authResponseClient{respond: func(*fhttp.Request) *fhttp.Response {
		requests++
		if requests > 3 {
			t.Fatal("unbounded challenge loop")
		}
		return authTestResponse(200, `{"error":{"error_code":14,"error_msg":"Captcha needed","redirect_uri":"https://id.vk.ru/captcha?session_token=PRIVATE_SESSION"}}`)
	}}
	_, err := c.fetchCallToken(context.Background(), client, c.currentPersona(), 1, "test", "name", "access", VKCredentials{})
	if !errors.Is(err, ErrCaptchaWaitRequired) || automatic != 1 || manual != 1 || requests != 3 || c.identity.Gen != 0 {
		t.Fatalf("auto=%d manual=%d requests=%d gen=%d err=%v", automatic, manual, requests, c.identity.Gen, err)
	}
}
