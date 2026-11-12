package vkauth

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/samosvalishe/free-turn-proxy/internal/captchaipc"
	"github.com/samosvalishe/free-turn-proxy/internal/provider"
	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk/internal/browserprofile"
	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk/internal/captcha"
)

func TestBrowserFailureWaitsForExplicitResumeAndKeepsCachedGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	broker, err := captchaipc.New(ctx, captchaipc.Config{Token: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Generation: "browser_test_generation"})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	calls := 0
	c := New(Config{StreamsPerCache: 1, StreamsAlive: func() int32 { return 0 }, BeforeFetch: broker.Wait, OnAuthWait: broker.Pause,
		BrowserSolver: func(context.Context, *captcha.Error, net.Dialer, browserprofile.Profile) (string, error) {
			calls++
			return "", context.DeadlineExceeded
		},
	})
	c.minFetchIntervalFn = func() time.Duration { return 0 }
	c.tokenChain = func(ctx context.Context, _ string, _ int, _ VKCredentials, _ tlsclient.CookieJar) (string, string, []string, error) {
		if calls > 0 {
			return "fresh", "pass", []string{"relay:3478"}, nil
		}
		_, e := c.solveBrowserCaptcha(ctx, browserprofile.Profile{}, 1, "link", "name", "token", &captcha.Error{RedirectURI: "https://vk.ru/captcha"})
		return "", "", nil, e
	}
	if _, _, _, err = c.GetCredentials(ctx, "link", 1); !errors.Is(err, provider.ErrBackoffActive) || errors.Is(err, provider.ErrFatalNoStreams) {
		t.Fatalf("browser failure killed all-stream wait: %v", err)
	}
	cache := c.store.Get(2)
	cache.creds = TurnCredentials{Username: "cached", Password: "pass", ServerAddrs: []string{"relay:3478"}, Link: "link", ExpiresAt: time.Now().Add(time.Minute)}
	if u, _, _, e := c.GetCredentials(ctx, "link", 2); e != nil || u != "cached" {
		t.Fatalf("cached group blocked: %v", e)
	}
	resumed := make(chan error, 1)
	go func() { _, _, _, e := c.GetCredentials(ctx, "link", 1); resumed <- e }()
	select {
	case <-resumed:
		t.Fatal("new authorization resumed without user")
	case <-time.After(30 * time.Millisecond):
	}
	broker.Resume()
	select {
	case e := <-resumed:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("resume failed")
	}
	if calls != 1 {
		t.Fatal("repeated browser challenge while waiting")
	}
}
func TestBrowserRejectedResultDoesNotCreateAnotherChallenge(t *testing.T) {
	calls, waiting, oldAuto := 0, 0, 0
	c := New(Config{BrowserSolver: func(context.Context, *captcha.Error, net.Dialer, browserprofile.Profile) (string, error) {
		calls++
		return "browser_token", nil
	}, OnAuthWait: func() { waiting++ }, AutoSolver: func(context.Context, *captcha.Error, int, tlsclient.HttpClient, browserprofile.Profile) (string, error) {
		oldAuto++
		return "", nil
	}})
	e := &captcha.Error{SessionToken: "session", RedirectURI: "https://vk.ru/captcha"}
	if _, err := c.solveCaptcha(context.Background(), nil, browserprofile.Profile{}, 1, "link", "name", "token", e); err != nil {
		t.Fatal(err)
	}
	if _, err := c.solveCaptcha(context.Background(), nil, browserprofile.Profile{}, 1, "link", "name", "token", e); !errors.Is(err, provider.ErrBackoffActive) {
		t.Fatal("provider rejection repeated browser")
	}
	if calls != 1 || waiting != 1 || oldAuto != 0 {
		t.Fatalf("unexpected retries: browser=%d wait=%d oldauto=%d", calls, waiting, oldAuto)
	}
}

func TestInteractiveRateLimitDoesNotKillZeroStreamProcess(t *testing.T) {
	paused := 0
	c := New(Config{StreamsAlive: func() int32 { return 0 }, OnAuthWait: func() { paused++ }})
	err := c.rateLimited(1, captcha.NewRateLimitError("test", "60"))
	if errors.Is(err, provider.ErrFatalNoStreams) || !errors.Is(err, provider.ErrBackoffActive) || paused != 1 {
		t.Fatalf("wrong interactive limit state: %v paused=%d", err, paused)
	}
	if c.BackoffUntilUnix() <= time.Now().Unix() {
		t.Fatal("provider retry time lost")
	}
}

func TestFreshAuthorizationSuccessHookDoesNotRunForCachedGroups(t *testing.T) {
	successes, fetches := 0, 0
	c := New(Config{StreamsPerCache: 1, OnAuthSuccess: func() { successes++ }})
	c.minFetchIntervalFn = func() time.Duration { return 0 }
	c.tokenChain = func(context.Context, string, int, VKCredentials, tlsclient.CookieJar) (string, string, []string, error) {
		fetches++
		return "fresh", "pass", []string{"relay:3478"}, nil
	}
	ctx := context.Background()
	if _, _, _, err := c.GetCredentials(ctx, "link", 1); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := c.GetCredentials(ctx, "link", 1); err != nil {
		t.Fatal(err)
	}
	if successes != 1 || fetches != 1 {
		t.Fatalf("fresh=%d success=%d", fetches, successes)
	}
	c.tokenChain = func(context.Context, string, int, VKCredentials, tlsclient.CookieJar) (string, string, []string, error) {
		return "", "", nil, ErrCaptchaWaitRequired
	}
	if _, _, _, err := c.GetCredentials(ctx, "link", 2); !errors.Is(err, provider.ErrBackoffActive) {
		t.Fatal(err)
	}
	if successes != 1 {
		t.Fatal("failed fetch signalled success")
	}
}
