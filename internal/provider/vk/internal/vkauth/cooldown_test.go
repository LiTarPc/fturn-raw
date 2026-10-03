package vkauth

import (
	"context"
	"errors"
	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk/internal/browserprofile"
	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk/internal/captcha"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCaptchaLimitPreservesPersonaSkipsManualAndPersists(t *testing.T) {
	for _, streams := range []int32{0, 2} {
		t.Run(string(rune('0'+streams)), func(t *testing.T) {
			paths := []string{filepath.Join(t.TempDir(), "vk_persona.json")}
			c := New(Config{StatePaths: paths, StreamsAlive: func() int32 { return streams }})
			before := c.currentPersona()
			c.autoSolver = func(context.Context, *captcha.Error, int, tlsclient.HttpClient, browserprofile.Profile) (string, error) {
				return "", captcha.ErrRateLimited
			}
			c.manualSolve = func(context.Context, *captcha.Error, net.Dialer, browserprofile.Profile) (string, error) {
				t.Fatal("manual invoked after limit")
				return "", nil
			}
			_, err := c.solveCaptcha(context.Background(), nil, before, 1, "lnk", "name", "t1", testCaptchaErr())
			want := ErrCaptchaWaitRequired
			if streams == 0 {
				want = ErrFatalCaptchaNoStreams
			}
			if !errors.Is(err, want) || !errors.Is(err, captcha.ErrRateLimited) {
				t.Fatalf("error = %v", err)
			}
			if c.currentPersona().VisitorID != before.VisitorID {
				t.Fatal("identity changed after explicit limit")
			}
			if d := time.Until(time.Unix(c.LockoutUntilUnix(), 0)); d < 4*time.Minute || d > 5*time.Minute {
				t.Fatalf("pause=%v", d)
			}
			restarted := New(Config{StatePaths: paths})
			restarted.tokenChain = func(context.Context, string, int, VKCredentials, tlsclient.CookieJar) (string, string, []string, error) {
				t.Fatal("request during persisted pause")
				return "", "", nil, nil
			}
			_, _, _, err = restarted.fetch(context.Background(), "lnk", 1)
			if !errors.Is(err, captcha.ErrRateLimited) {
				t.Fatalf("restart=%v", err)
			}
		})
	}
}
func TestCooldownRejectsInvalidExpiredAndExcessiveDates(t *testing.T) {
	p := filepath.Join(t.TempDir(), captchaPauseFile)
	s := cooldownStore{paths: []string{p}}
	for _, data := range []string{`{}`, `{"until":1}`, `{"until":9999999999999}`, `invalid`} {
		if err := os.WriteFile(p, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if s.load() != 0 {
			t.Fatalf("accepted %s", data)
		}
	}
	until := time.Now().Add(time.Minute).Unix()
	if !s.save(until) || !s.save(until-30) || s.load() != until {
		t.Fatal("pause shortened or lost")
	}
}

func TestFetchStopsCredentialChainOnExplicitLimit(t *testing.T) {
	calls := 0
	c := newTestClient(t, nil)
	c.cooldown = newCooldownStore([]string{filepath.Join(t.TempDir(), "vk_persona.json")})
	c.tokenChain = func(context.Context, string, int, VKCredentials, tlsclient.CookieJar) (string, string, []string, error) {
		calls++
		return "", "", nil, c.captchaLimited(1)
	}
	_, _, _, err := c.fetch(context.Background(), "test", 1)
	if !errors.Is(err, captcha.ErrRateLimited) || calls != 1 {
		t.Fatalf("err=%v credential attempts=%d", err, calls)
	}
	_, _, _, _ = c.fetch(context.Background(), "test", 2)
	if calls != 1 {
		t.Fatal("later worker retried during pause")
	}
}
