package vkauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/samosvalishe/free-turn-proxy/internal/captchaipc"
	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk/internal/browserprofile"
	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk/internal/captcha"
)

const advancedFixtureInput = "fixture-challenge-20261107"

func advancedFixturePow(t *testing.T, input string) string {
	t.Helper()
	nonce := 0
	var hash string
	for {
		sum := sha256.Sum256([]byte(input + strconv.Itoa(nonce)))
		hash = hex.EncodeToString(sum[:])
		if strings.HasPrefix(hash, "0") {
			break
		}
		nonce++
	}
	telemetry := json.RawMessage(`{"ua":{"ok":true,"result":{"userAgent":"Fixture Android WebView","userAgentData":{"brands":[{"brand":"Chromium","version":"146"}],"platform":"Android","mobile":true}}}}`)
	raw, err := json.Marshal(map[string]any{"hash": hash, "nonce": nonce, "telemetry": telemetry, "duration_ms": 1})
	if err != nil {
		t.Fatal(err)
	}
	return "v2." + base64.StdEncoding.EncodeToString(raw)
}
func advancedFixtureHTML() string {
	return `<html><head><script>window.captchaPowResult="v2."+""; (function(){}("` + advancedFixtureInput + `",1,"")); window.vk={debug:"11111111-2222-4333-8444-555555555555"}; window.init={"hosts":{"api":"api.vk.ru"},"data":{"show_captcha_type":"checkbox"}};</script></head><body></body></html>`
}

type advancedFixtureClient struct {
	tlsclient.HttpClient
	t                                           *testing.T
	proof                                       string
	legacy                                      bool
	checkStatus                                 int
	checkBOT                                    bool
	submitCode                                  int
	calls, checks, submissions, anonymous, turn int
	onBeforeSubmit                              func()
}

func (c *advancedFixtureClient) GetCookies(*url.URL) []*fhttp.Cookie { return nil }
func (c *advancedFixtureClient) Do(req *fhttp.Request) (*fhttp.Response, error) {
	c.calls++
	if req.Context().Err() != nil {
		return nil, req.Context().Err()
	}
	var raw []byte
	if req.Body != nil {
		raw, _ = io.ReadAll(req.Body)
	}
	form, _ := url.ParseQuery(string(raw))
	body := `{"response":{}}`
	status := 200
	switch req.URL.Path {
	case "/call/join/test":
		body = "<html>join</html>"
	case "/":
		body = `{"data":{"access_token":"PRIVATE_ACCESS"}}`
	case "/method/calls.getCallPreview":
	case "/method/calls.getAnonymousToken":
		c.anonymous++
		if form.Get("success_token") == "" {
			body = `{"error":{"error_code":14,"error_msg":"Captcha needed","redirect_uri":"https://id.vk.ru/captcha?session_token=PRIVATE_SESSION"}}`
		} else {
			c.submissions++
			if c.onBeforeSubmit != nil {
				c.onBeforeSubmit()
			}
			if form.Get("success_token") != "PRIVATE_SUCCESS" {
				c.t.Error("proof used as final token")
			}
			if c.submitCode != 0 {
				body = `{"error":{"error_code":` + strconv.Itoa(c.submitCode) + `,"request_params":[{"key":"success_token","value":"PRIVATE_SUCCESS"}]}}`
			} else {
				body = `{"response":{"token":"PRIVATE_CALL"}}`
			}
		}
	case "/captcha":
		body = advancedFixtureHTML()
	case "/method/captchaNotRobot.settings":
		body = `{"response":{"sensors_delay":20}}`
	case "/method/captchaNotRobot.componentDone":
	case "/method/captchaNotRobot.check":
		c.checks++
		if !c.legacy && form.Get("hash") != c.proof {
			c.t.Error("browser proof was not submitted unchanged")
		}
		if !c.legacy && (req.Header.Get("User-Agent") != "Fixture Android WebView" || req.Header.Get("Sec-Ch-Ua-Mobile") != "?1") {
			c.t.Error("browser profile not applied to verification")
		}
		if c.checkStatus != 0 {
			status = c.checkStatus
			body = `{"error":{"error_code":29}}`
		} else if c.checkBOT {
			body = `{"response":{"status":"BOT"}}`
		} else {
			body = `{"response":{"status":"ok","success_token":"PRIVATE_SUCCESS"}}`
		}
	case "/method/captchaNotRobot.endSession", "/method/captchaNotRobot.leaveCaptcha":
	case "/fb.do":
		if form.Get("method") == "auth.anonymLogin" {
			body = `{"session_key":"PRIVATE_OK_SESSION"}`
		} else {
			c.turn++
			body = `{"turn_server":{"username":"PRIVATE_TURN_USER","credential":"PRIVATE_TURN_PASS","urls":["turn:relay.test:3478"]}}`
		}
	default:
		c.t.Errorf("unexpected fixture request path: %s", req.URL.Path)
	}
	response := authTestResponse(status, body)
	response.Request = req
	return response, nil
}
func advancedClientForFixture(t *testing.T, f *advancedFixtureClient) (*Client, *outcomeLogger, *atomic.Int32) {
	t.Helper()
	logger := &outcomeLogger{}
	authorized := &atomic.Int32{}
	c := New(Config{AdvancedCaptcha: true, Log: logger, StreamsPerCache: 7, Credentials: []VKCredentials{{ClientID: "fixture1"}, {ClientID: "fixture2"}}, OnAuthSuccess: func() { authorized.Add(1) }})
	c.minFetchIntervalFn = func() time.Duration { return 0 }
	c.powCapture = func(ctx context.Context, _ string, _ net.Dialer, _ browserprofile.Profile) (string, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > browserPowCaptureTimeout {
			t.Error("capture not bounded")
		}
		if authorized.Load() != 0 {
			t.Error("authorized on capture")
		}
		return f.proof, nil
	}
	c.tokenChain = func(ctx context.Context, link string, id int, creds VKCredentials, _ tlsclient.CookieJar) (string, string, []string, error) {
		return c.getTokenChainWithClient(ctx, link, id, creds, c.currentPersona(), f)
	}
	return c, logger, authorized
}
func assertAdvancedPrivate(t *testing.T, log *outcomeLogger, proof string) {
	t.Helper()
	out := strings.Join(log.lines, "\n")
	if strings.Contains(out, "PRIVATE_") || strings.Contains(out, proof) {
		t.Fatal("private fixture data leaked")
	}
}
func TestAdvancedFullCredentialChainAndConcurrentCacheUsers(t *testing.T) {
	f := &advancedFixtureClient{t: t, proof: advancedFixturePow(t, advancedFixtureInput)}
	c, log, authorized := advancedClientForFixture(t, f)
	capture := c.powCapture
	var captures atomic.Int32
	c.powCapture = func(ctx context.Context, u string, d net.Dialer, p browserprofile.Profile) (string, error) {
		captures.Add(1)
		return capture(ctx, u, d, p)
	}
	f.onBeforeSubmit = func() {
		if authorized.Load() != 0 {
			t.Error("authorized before provider submission")
		}
	}
	var wg sync.WaitGroup
	for id := 0; id < 7; id++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			user, pass, addrs, err := c.GetCredentials(ctx, "test", id)
			if err != nil || user != "PRIVATE_TURN_USER" || pass != "PRIVATE_TURN_PASS" || len(addrs) != 1 {
				t.Errorf("credential chain did not complete: %v", err)
			}
		}(id)
	}
	wg.Wait()
	if captures.Load() != 1 || authorized.Load() != 1 || f.checks != 1 || f.submissions != 1 || f.turn != 1 {
		t.Fatalf("capture=%d authorized=%d checks=%d submissions=%d turn=%d", captures.Load(), authorized.Load(), f.checks, f.submissions, f.turn)
	}
	assertAdvancedPrivate(t, log, f.proof)
}
func TestAdvancedCaptureUnavailableFallsBackToLegacy(t *testing.T) {
	for _, cause := range []error{context.DeadlineExceeded, errors.New("host unavailable")} {
		t.Run(cause.Error(), func(t *testing.T) {
			f := &advancedFixtureClient{t: t, proof: advancedFixturePow(t, advancedFixtureInput), legacy: true}
			c, log, authorized := advancedClientForFixture(t, f)
			c.powCapture = func(context.Context, string, net.Dialer, browserprofile.Profile) (string, error) { return "", cause }
			_, _, _, err := c.GetCredentials(context.Background(), "test", 0)
			if err != nil || f.checks != 1 || authorized.Load() != 1 {
				t.Fatalf("fallback err=%v checks=%d", err, f.checks)
			}
			assertAdvancedPrivate(t, log, f.proof)
		})
	}
}
func TestAdvancedForeignProofUsesOneFreshManualChallenge(t *testing.T) {
	f := &advancedFixtureClient{t: t, proof: advancedFixturePow(t, "other-fixture-input")}
	c, log, authorized := advancedClientForFixture(t, f)
	manual := 0
	c.manualSolve = func(_ context.Context, e *captcha.Error, _ net.Dialer, _ browserprofile.Profile) (string, error) {
		manual++
		return "PRIVATE_SUCCESS", nil
	}
	_, _, _, err := c.GetCredentials(context.Background(), "test", 0)
	if err != nil || manual != 1 || f.checks != 0 || f.anonymous != 3 || authorized.Load() != 1 || c.identity.Gen != 0 {
		t.Fatalf("foreign proof policy: err=%v manual=%d checks=%d anonymous=%d", err, manual, f.checks, f.anonymous)
	}
	assertAdvancedPrivate(t, log, f.proof)
}
func TestAdvancedRateLimitStopsWithoutManualOrIdentityRotation(t *testing.T) {
	for _, atCapture := range []bool{true, false} {
		t.Run(strconv.FormatBool(atCapture), func(t *testing.T) {
			f := &advancedFixtureClient{t: t, proof: advancedFixturePow(t, advancedFixtureInput), checkStatus: 429}
			c, log, authorized := advancedClientForFixture(t, f)
			manual := 0
			c.manualSolve = func(context.Context, *captcha.Error, net.Dialer, browserprofile.Profile) (string, error) {
				manual++
				return "", nil
			}
			if atCapture {
				c.powCapture = func(context.Context, string, net.Dialer, browserprofile.Profile) (string, error) {
					return "", captcha.NewRateLimitError("fixture", "3600")
				}
			}
			_, _, _, err := c.GetCredentials(context.Background(), "test", 0)
			if !errors.Is(err, captcha.ErrRateLimited) || manual != 0 || f.submissions != 0 || authorized.Load() != 0 || time.Until(time.Unix(c.BackoffUntilUnix(), 0)) < 59*time.Minute || c.identity.Gen != 0 {
				t.Fatalf("rate limit not preserved: %v", err)
			}
			assertAdvancedPrivate(t, log, f.proof)
		})
	}
}
func TestAdvancedService10AfterTokenDoesNotRestartAuthorization(t *testing.T) {
	f := &advancedFixtureClient{t: t, proof: advancedFixturePow(t, advancedFixtureInput), submitCode: 10}
	c, log, authorized := advancedClientForFixture(t, f)
	for _, id := range []int{0, 7, 14} {
		_, _, _, err := c.GetCredentials(context.Background(), "test", id)
		if !errors.Is(err, ErrVKServiceUnavailable) {
			t.Fatalf("error10: %v", err)
		}
	}
	if f.checks != 1 || f.submissions != 1 || f.turn != 0 || authorized.Load() != 0 || c.identity.Gen != 0 {
		t.Fatal("service error retried or authorized")
	}
	assertAdvancedPrivate(t, log, f.proof)
}
func TestAdvancedManualOnlyAndRepeatedManualChallengeAreBounded(t *testing.T) {
	c := New(Config{AdvancedCaptcha: true, ManualOnly: true})
	manual, capture, wait := 0, 0, 0
	c.onAuthWait = func() { wait++ }
	c.powCapture = func(context.Context, string, net.Dialer, browserprofile.Profile) (string, error) {
		capture++
		return "", nil
	}
	c.manualSolve = func(context.Context, *captcha.Error, net.Dialer, browserprofile.Profile) (string, error) {
		manual++
		return "PRIVATE_SUCCESS", nil
	}
	e := testCaptchaErr()
	if _, err := c.solveCaptcha(context.Background(), nil, c.currentPersona(), 0, "test", "name", "PRIVATE_ACCESS", e); err != nil {
		t.Fatal(err)
	}
	if _, err := c.solveCaptcha(context.Background(), nil, c.currentPersona(), 0, "test", "name", "PRIVATE_ACCESS", e); !errors.Is(err, ErrCaptchaWaitRequired) {
		t.Fatal("manual rejection not paused")
	}
	if manual != 1 || capture != 0 || wait != 1 || c.identity.Gen != 0 {
		t.Fatal("manual-only caused extra work")
	}
}
func TestAdvancedBotDisablesAutomaticChecksAndRequestsFreshManual(t *testing.T) {
	c := New(Config{AdvancedCaptcha: true})
	auto, manual, wait, requests := 0, 0, 0, 0
	c.onAuthWait = func() { wait++ }
	c.autoSolver = func(context.Context, *captcha.Error, int, tlsclient.HttpClient, browserprofile.Profile) (string, error) {
		auto++
		return "", captcha.ErrBotRejected
	}
	c.manualSolve = func(context.Context, *captcha.Error, net.Dialer, browserprofile.Profile) (string, error) {
		manual++
		return "", context.DeadlineExceeded
	}
	f := &authResponseClient{respond: func(*fhttp.Request) *fhttp.Response {
		requests++
		if requests > 2 {
			t.Error("repeated auth after manual failure")
		}
		return authTestResponse(200, `{"error":{"error_code":14,"error_msg":"Captcha needed","redirect_uri":"https://id.vk.ru/captcha?session_token=PRIVATE_SESSION"}}`)
	}}
	_, err := c.fetchCallToken(context.Background(), f, c.currentPersona(), 0, "test", "name", "access", VKCredentials{})
	if !errors.Is(err, ErrCaptchaWaitRequired) || auto != 1 || manual != 1 || requests != 2 || wait != 1 || !c.autoRejected.Load() || c.identity.Gen != 0 {
		t.Fatalf("BOT policy err=%v auto=%d manual=%d requests=%d", err, auto, manual, requests)
	}
}
func TestAdvancedStopDuringCaptureDoesNotFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := &advancedFixtureClient{t: t, proof: "unused"}
	c, _, authorized := advancedClientForFixture(t, f)
	c.powCapture = func(context.Context, string, net.Dialer, browserprofile.Profile) (string, error) {
		cancel()
		return "", ctx.Err()
	}
	_, err := c.defaultAutoSolve(ctx, testCaptchaErr(), 0, f, c.currentPersona())
	if !errors.Is(err, context.Canceled) || f.calls != 0 || authorized.Load() != 0 {
		t.Fatal("STOP triggered fallback")
	}
}

func TestAdvancedRejectedAutoTokenAllowsOnlyOneManualThenPauses(t *testing.T) {
	c := New(Config{AdvancedCaptcha: true})
	auto, manual, wait, requests := 0, 0, 0, 0
	c.onAuthWait = func() { wait++ }
	c.autoSolver = func(context.Context, *captcha.Error, int, tlsclient.HttpClient, browserprofile.Profile) (string, error) {
		auto++
		return "PRIVATE_AUTO_TOKEN", nil
	}
	c.manualSolve = func(context.Context, *captcha.Error, net.Dialer, browserprofile.Profile) (string, error) {
		manual++
		return "PRIVATE_MANUAL_TOKEN", nil
	}
	f := &authResponseClient{respond: func(*fhttp.Request) *fhttp.Response {
		requests++
		if requests > 3 {
			t.Error("unbounded rejected token loop")
		}
		return authTestResponse(200, `{"error":{"error_code":14,"error_msg":"Captcha needed","redirect_uri":"https://id.vk.ru/captcha?session_token=PRIVATE_SESSION"}}`)
	}}
	_, err := c.fetchCallToken(context.Background(), f, c.currentPersona(), 0, "test", "name", "access", VKCredentials{})
	if !errors.Is(err, ErrCaptchaWaitRequired) || auto != 1 || manual != 1 || wait != 1 || requests != 3 || c.identity.Gen != 0 {
		t.Fatal("rejected token not bounded")
	}
}
func TestAdvancedManualFailureKeepsCachedGroupsAndNeedsExplicitResume(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	broker, err := captchaipc.New(ctx, captchaipc.Config{Token: strings.Repeat("a", 64), Generation: "advanced_wait_generation", Protocol: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	c := New(Config{AdvancedCaptcha: true, ManualOnly: true, StreamsPerCache: 1, BeforeFetch: broker.Wait, OnAuthWait: broker.Pause, StreamsAlive: func() int32 { return 0 }})
	c.minFetchIntervalFn = func() time.Duration { return 0 }
	var fetches atomic.Int32
	c.manualSolve = func(context.Context, *captcha.Error, net.Dialer, browserprofile.Profile) (string, error) {
		return "", context.DeadlineExceeded
	}
	c.tokenChain = func(ctx context.Context, _ string, id int, _ VKCredentials, _ tlsclient.CookieJar) (string, string, []string, error) {
		if fetches.Add(1) > 1 {
			return "fresh", "pass", []string{"relay.test:3478"}, nil
		}
		_, err := c.solveCaptcha(ctx, nil, c.currentPersona(), id, "test", "name", "access", testCaptchaErr())
		return "", "", nil, err
	}
	_, _, _, err = c.GetCredentials(ctx, "test", 1)
	if !errors.Is(err, ErrCaptchaWaitRequired) {
		t.Fatal(err)
	}
	cache := c.store.Get(2)
	cache.creds = TurnCredentials{Username: "cached", Password: "pass", ServerAddrs: []string{"relay.test:3478"}, Link: "test", ExpiresAt: time.Now().Add(time.Minute)}
	if user, _, _, e := c.GetCredentials(ctx, "test", 2); e != nil || user != "cached" {
		t.Fatal("cache blocked")
	}
	done := make(chan error, 1)
	go func() { _, _, _, e := c.GetCredentials(ctx, "test", 1); done <- e }()
	select {
	case <-done:
		t.Fatal("unattended auth repeated")
	case <-time.After(25 * time.Millisecond):
	}
	broker.Resume()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("resume blocked")
	}
	if fetches.Load() != 2 {
		t.Fatal("extra auth chains")
	}
}

func TestAdvancedBotGateIsSharedAcrossLinks(t *testing.T) {
	gate := &atomic.Bool{}
	auto := 0
	manual := 0
	for _, first := range []bool{true, false} {
		c := New(Config{AdvancedCaptcha: true, AutoRejected: gate})
		c.autoSolver = func(context.Context, *captcha.Error, int, tlsclient.HttpClient, browserprofile.Profile) (string, error) {
			auto++
			return "", captcha.ErrBotRejected
		}
		c.manualSolve = func(context.Context, *captcha.Error, net.Dialer, browserprofile.Profile) (string, error) {
			manual++
			return "PRIVATE_SUCCESS", nil
		}
		_, err := c.solveCaptcha(context.Background(), nil, c.currentPersona(), 1, "test", "name", "access", testCaptchaErr())
		if first && !errors.Is(err, errManualFreshChallenge) {
			t.Fatal("first BOT did not request fresh manual challenge")
		}
		if !first && err != nil {
			t.Fatal("second link could not use manual verification")
		}
	}
	if auto != 1 || manual != 1 || !gate.Load() {
		t.Fatal("another link retried automatic verification after BOT")
	}
}
