package vkauth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/samosvalishe/free-turn-proxy/internal/logx"
	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk/internal/browserprofile"
	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk/internal/captcha"
	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk/internal/personanet"
	"github.com/samosvalishe/free-turn-proxy/internal/randx"

	tlsclient "github.com/bogdanfinn/tls-client"
)

type Config struct {
	Credentials     []VKCredentials
	Dialer          net.Dialer
	ManualOnly      bool
	AdvancedCaptcha bool
	AutoRejected    *atomic.Bool
	PowCapture      PowCaptureFunc
	StreamsPerCache int
	StreamsAlive    func() int32
	AutoSolver      AutoSolveFunc
	ManualSolver    ManualSolveFunc
	BrowserSolver   ManualSolveFunc
	BeforeFetch     func(context.Context) error
	OnAuthWait      func()
	OnAuthSuccess   func()
	Platform        browserprofile.Platform
	FingerprintSeed string
	StatePaths      []string
	Log             logx.Logger
}

type Client struct {
	credentials     []VKCredentials
	dialer          net.Dialer
	manualOnly      bool
	platform        browserprofile.Platform
	streamsFn       func() int32
	autoSolver      AutoSolveFunc
	manualSolve     ManualSolveFunc
	browserSolve    ManualSolveFunc
	advancedCaptcha bool
	powCapture      PowCaptureFunc
	beforeFetch     func(context.Context) error
	onAuthWait      func()
	onAuthSuccess   func()
	log             logx.Logger

	store *Store

	lockout           atomic.Int64
	captchaPause      atomic.Int64
	rateLimitStreak   atomic.Uint32
	cooldown          cooldownStore
	networkPauseUntil atomic.Int64
	servicePauseUntil atomic.Int64
	autoRejected      *atomic.Bool

	personaMu sync.RWMutex
	identity  browserprofile.Identity
	persona   browserprofile.Profile
	gens      genStore

	fetchMu            sync.Mutex
	lastFetchTime      time.Time
	captchaAttempt     int
	advancedManualUsed bool
	tokenChain         tokenChainFn
	minFetchIntervalFn func() time.Duration
}

type tokenChainFn func(ctx context.Context, link string, streamID int, creds VKCredentials, jar tlsclient.CookieJar) (string, string, []string, error)

func New(cfg Config) *Client {
	c := &Client{
		credentials:     cfg.Credentials,
		dialer:          cfg.Dialer,
		manualOnly:      cfg.ManualOnly,
		platform:        cfg.Platform,
		streamsFn:       cfg.StreamsAlive,
		autoSolver:      cfg.AutoSolver,
		manualSolve:     cfg.ManualSolver,
		browserSolve:    cfg.BrowserSolver,
		advancedCaptcha: cfg.AdvancedCaptcha,
		autoRejected:    cfg.AutoRejected,
		powCapture:      cfg.PowCapture,
		beforeFetch:     cfg.BeforeFetch,
		onAuthWait:      cfg.OnAuthWait,
		onAuthSuccess:   cfg.OnAuthSuccess,
		log:             cfg.Log,
		store:           NewStore(cfg.StreamsPerCache),
	}
	if c.autoRejected == nil {
		c.autoRejected = &atomic.Bool{}
	}
	if len(c.credentials) == 0 {
		c.credentials = DefaultCredentials
	}
	if c.log == nil {
		c.log = logx.Nop()
	}
	if c.streamsFn == nil {
		c.streamsFn = func() int32 { return 1 }
	}
	c.tokenChain = c.getTokenChain
	c.minFetchIntervalFn = func() time.Duration {
		return 3*time.Second + time.Duration(randx.Intn(3000))*time.Millisecond
	}
	seed := cfg.FingerprintSeed
	if seed == "" {
		seed = randx.Hex(16)
	}
	c.gens = genStore{paths: cfg.StatePaths}
	c.identity = browserprofile.Identity{Seed: seed, Gen: c.gens.load(seed)}
	c.persona = browserprofile.For(c.platform, c.identity)
	if c.identity.Gen > 0 {
		c.log.Debugf("[VK Auth] Persona gen=%d restored | User-Agent: %s", c.identity.Gen, c.persona.UserAgent)
	}
	c.cooldown = newCooldownStore(cfg.StatePaths)
	if until := c.cooldown.load(); until > 0 {
		c.captchaPause.Store(until)
		c.lockout.Store(until)
		c.log.Warnf("[VK Auth] CAPTCHA_RATE_LIMIT pause_until=%d; restored local pause", until)
	}
	return c
}

func (c *Client) currentPersona() browserprofile.Profile {
	c.personaMu.RLock()
	defer c.personaMu.RUnlock()
	return c.persona
}

func (c *Client) burnPersona(streamID int) {
	c.personaMu.Lock()
	c.identity.Gen++
	c.persona = browserprofile.For(c.platform, c.identity)
	ua, gen, seed := c.persona.UserAgent, c.identity.Gen, c.identity.Seed
	c.personaMu.Unlock()
	c.log.Infof("[STREAM %d] [VK Auth] Persona burned, gen=%d | User-Agent: %s", streamID, gen, ua)
	if !c.gens.save(seed, gen) && len(c.gens.paths) > 0 {
		c.log.Warnf("[STREAM %d] [VK Auth] Persona gen not persisted (%v) - burned fingerprint returns after restart", streamID, c.gens.paths)
	}
}

// GetCredentials возвращает учетные данные TURN, используя кеш или запрашивая их у VK.
func (c *Client) GetCredentials(ctx context.Context, link string, streamID int) (string, string, []string, error) {
	cache := c.store.Get(streamID)
	cacheID := c.store.CacheID(streamID)

	cache.mutex.RLock()
	if cache.creds.Link == link && time.Now().Before(cache.creds.ExpiresAt) && len(cache.creds.ServerAddrs) > 0 {
		expires := time.Until(cache.creds.ExpiresAt)
		u, p := cache.creds.Username, cache.creds.Password
		addrs := orderAddrs(cache.creds.ServerAddrs, streamID)
		cache.mutex.RUnlock()
		c.log.Debugf("[STREAM %d] [VK Auth] Using cached credentials (cache=%d, expires in %v, server=%s)", streamID, cacheID, expires, addrs[0])
		return u, p, addrs, nil
	}
	cache.mutex.RUnlock()

	cache.mutex.Lock()
	defer cache.mutex.Unlock()

	if cache.creds.Link == link && time.Now().Before(cache.creds.ExpiresAt) && len(cache.creds.ServerAddrs) > 0 {
		return cache.creds.Username, cache.creds.Password, orderAddrs(cache.creds.ServerAddrs, streamID), nil
	}

	user, pass, addrs, err := c.fetchSerialized(ctx, link, streamID)
	if err != nil {
		return "", "", nil, err
	}

	cache.creds = TurnCredentials{
		Username:    user,
		Password:    pass,
		ServerAddrs: addrs,
		ExpiresAt:   time.Now().Add(CredentialLifetime - CacheSafetyMargin).Round(0),
		Link:        link,
	}
	if c.onAuthSuccess != nil {
		c.onAuthSuccess()
	}
	return user, pass, orderAddrs(addrs, streamID), nil
}

func orderAddrs(addrs []string, streamID int) []string {
	n := len(addrs)
	if n <= 1 {
		return append([]string(nil), addrs...)
	}
	k := streamID % n
	out := make([]string, 0, n)
	out = append(out, addrs[k:]...)
	out = append(out, addrs[:k]...)
	return out
}

func (c *Client) HandleAuthError(streamID int) bool {
	cache := c.store.Get(streamID)
	cacheID := c.store.CacheID(streamID)
	now := time.Now().Unix()

	if now-cache.lastErrorTime.Load() > int64(ErrorWindow.Seconds()) {
		cache.errorCount.Store(0)
	}
	count := cache.errorCount.Add(1)
	cache.lastErrorTime.Store(now)

	c.log.Warnf("[STREAM %d] [VK Auth] Auth error (cache=%d, count=%d/%d)", streamID, cacheID, count, MaxCacheErrors)

	if count >= MaxCacheErrors {
		c.log.Warnf("[VK Auth] Multiple auth errors (%d), invalidating cache %d for stream %d", count, cacheID, streamID)
		cache.Invalidate()
		c.log.Warnf("[STREAM %d] [VK Auth] Credentials cache invalidated", streamID)
		return true
	}
	return false
}

func (c *Client) ResetErrors(streamID int) {
	c.store.Get(streamID).errorCount.Store(0)
}

func (c *Client) DropCredentials(streamID int) {
	if !c.store.Get(streamID).Invalidate() {
		return
	}
	c.log.Warnf("[STREAM %d] [VK Auth] Deallocate unconfirmed - credentials dropped (cache=%d)",
		streamID, c.store.CacheID(streamID))
}

func (c *Client) LockoutUntilUnix() int64 {
	return c.lockout.Load()
}

// BackoffUntilUnix возвращает ближайший общий запрет запросов к VK.
func (c *Client) BackoffUntilUnix() int64 {
	return max(c.LockoutUntilUnix(), c.networkPauseUntil.Load(), c.servicePauseUntil.Load())
}

func (*Client) Name() string { return "vk" }

func (*Client) IsAuthError(err error) bool { return IsAuthError(err) }

func (c *Client) engageLockout(d time.Duration) {
	c.lockout.Store(time.Now().Add(d).Unix())
}

// fetchSerialized сериализует fetch и держит min-интервал между запросами (анти
// rate-limit VK).
func (c *Client) fetchSerialized(ctx context.Context, link string, streamID int) (string, string, []string, error) {
	c.fetchMu.Lock()
	defer c.fetchMu.Unlock()
	if c.beforeFetch != nil {
		if err := c.beforeFetch(ctx); err != nil {
			return "", "", nil, err
		}
	}

	minInterval := c.minFetchIntervalFn()
	elapsed := time.Since(c.lastFetchTime)
	if !c.lastFetchTime.IsZero() && elapsed < minInterval {
		wait := minInterval - elapsed
		c.log.Debugf("[STREAM %d] [VK Auth] Throttling: waiting %v to prevent rate limit", streamID, wait.Truncate(time.Millisecond))
		select {
		case <-ctx.Done():
			return "", "", nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	user, pass, addrs, err := c.fetch(ctx, link, streamID)
	if ctx.Err() == nil {
		c.lastFetchTime = time.Now()
	}
	return user, pass, addrs, err
}

func (c *Client) fetch(ctx context.Context, link string, streamID int) (string, string, []string, error) {
	if until := max(c.captchaPause.Load(), c.cooldown.load()); time.Now().Unix() < until {
		c.captchaPause.Store(until)
		c.lockout.Store(max(until, c.lockout.Load()))
		if c.onAuthWait != nil {
			c.onAuthWait()
		}
		return "", "", nil, errors.Join(ErrCaptchaWaitRequired, captcha.ErrRateLimited)
	}
	if time.Now().Unix() < c.servicePauseUntil.Load() {
		return "", "", nil, ErrVKServiceUnavailable
	}
	if time.Now().Unix() < c.networkPauseUntil.Load() {
		return "", "", nil, fmt.Errorf("VK network temporarily unavailable")
	}
	if time.Now().Unix() < c.lockout.Load() {
		return "", "", nil, fmt.Errorf("%w: %w", ErrCaptchaWaitRequired, ErrLockoutActive)
	}

	// A rejected automatic check must not trigger another API authorization if
	// this integration has no way to ask the user to verify in a browser.
	if c.autoRejected.Load() && c.manualSolve == nil {
		c.engageLockout(time.Minute)
		return "", "", nil, errors.Join(ErrCaptchaWaitRequired, captcha.ErrBotRejected)
	}
	c.captchaAttempt = 0
	c.advancedManualUsed = false

	var lastErr error
	burns := 0
	outdatedRetried := make([]bool, len(c.credentials))
	jar := personanet.NewCookieJar()
	for i := 0; i < len(c.credentials); {
		creds := c.credentials[i]
		c.log.Debugf("[STREAM %d] [VK Auth] Trying credentials: client_id=%s", streamID, creds.ClientID)

		user, pass, addrs, err := c.tokenChain(ctx, link, streamID, creds, jar)
		if err == nil {
			c.rateLimitStreak.Store(0)
			c.log.Debugf("[STREAM %d] [VK Auth] Success with client_id=%s", streamID, creds.ClientID)
			return user, pass, addrs, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return "", "", nil, err
		}
		c.log.Warnf("[STREAM %d] [VK Auth] Failed with client_id=%s: %v", streamID, creds.ClientID, err)
		if errors.Is(err, ErrVKServiceUnavailable) {
			until := time.Now().Add(30 * time.Second).Unix()
			c.servicePauseUntil.Store(until)
			c.log.Warnf("[STREAM %d] [VK Auth] VK_SERVICE_UNAVAILABLE pause_until=%d; requests paused, credential fallback skipped", streamID, until)
			return "", "", nil, err
		}
		if errors.Is(err, captcha.ErrRateLimited) {
			// solveCaptcha may already have recorded this refusal.
			if errors.Is(err, ErrCaptchaWaitRequired) || errors.Is(err, ErrFatalCaptchaNoStreams) {
				return "", "", nil, err
			}
			return "", "", nil, c.rateLimited(streamID, err)
		}
		if errors.Is(err, ErrCaptchaWaitRequired) || errors.Is(err, ErrFatalCaptchaNoStreams) {
			return "", "", nil, err
		}
		if isNetworkTimeout(err) {
			c.networkPauseUntil.Store(time.Now().Add(30 * time.Second).Unix())
			c.log.Warnf("[STREAM %d] [VK Auth] Network timeout; pausing VK requests for 30 seconds", streamID)
			return "", "", nil, err
		}
		// The call token is short-lived. Restart the chain once to obtain a new
		// one before trying another client_id. This is not a captcha failure.
		if errors.Is(err, ErrAnonymTokenOutdated) && !outdatedRetried[i] {
			outdatedRetried[i] = true
			c.captchaAttempt = 0
			jar = personanet.NewCookieJar()
			c.log.Infof("[STREAM %d] [VK Auth] Call token expired; retrying with a fresh session", streamID)
			continue
		}

		// Личность сменилась - тот же client_id проходится заново с чистыми
		// куками, пока не кончатся режимы решения captcha.
		if errors.Is(err, ErrPersonaBurned) && burns < maxPersonaBurns {
			burns++
			jar = personanet.NewCookieJar()
			continue
		}
		if !errors.Is(err, ErrPersonaBurned) {
			// An unrelated failure after a solved captcha must not force the next
			// client_id straight into the manual captcha mode.
			c.captchaAttempt = 0
		}
		i++

		if errors.Is(err, ErrCaptchaWaitRequired) || errors.Is(err, ErrFatalCaptchaNoStreams) ||
			errors.Is(err, ErrInvalidJoinLink) || errors.Is(err, ErrAnonymousBlocked) ||
			errors.Is(err, ErrCallFull) || errors.Is(err, captcha.ErrUnavailable) {
			return "", "", nil, err
		}
	}
	return "", "", nil, fmt.Errorf("all VK credentials failed: %w", lastErr)
}

func vkDelayRandom(ctx context.Context, minMs, maxMs int) error {
	ms := minMs + randx.Intn(maxMs-minMs+1)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Duration(ms) * time.Millisecond):
		return nil
	}
}

func isNetworkTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return (errors.As(err, &netErr) && netErr.Timeout()) || strings.Contains(err.Error(), "i/o timeout")
}
