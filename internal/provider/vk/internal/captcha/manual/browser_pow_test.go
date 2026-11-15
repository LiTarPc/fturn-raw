package manual

import (
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk/internal/browserprofile"
)

func captureFixture() string {
	return "v2." + base64.StdEncoding.EncodeToString([]byte(`{"hash":"`+strings.Repeat("0", 64)+`","telemetry":{}}`))
}
func TestPowCaptureEndpointRejectsForeignStaleAndOversizedResults(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path, err := newPowCapturePath()
	if err != nil {
		t.Fatal(err)
	}
	other, _ := newPowCapturePath()
	if path == other {
		t.Fatal("capture nonce reused")
	}
	results := make(chan string, 1)
	handler := powCaptureHandler(ctx, path, results)
	for _, tc := range []struct {
		name, method, path, host, origin, body string
		want                                   int
	}{
		{"valid", "POST", path, "localhost:8765", "http://localhost:8765", "pow=" + url.QueryEscape(captureFixture()), 200},
		{"foreign path", "POST", other, "localhost:8765", "", "pow=" + url.QueryEscape(captureFixture()), 404},
		{"wrong host", "POST", path, "evil.test", "", "", 403},
		{"wrong origin", "POST", path, "localhost:8765", "https://evil.test", "", 403},
		{"method", "GET", path, "localhost:8765", "", "", 405},
		{"query", "POST", path + "?pow=x", "localhost:8765", "", "", 400},
		{"success token", "POST", path, "localhost:8765", "", "token=PRIVATE_SUCCESS", 400},
		{"invalid proof", "POST", path, "localhost:8765", "", "pow=PRIVATE_MALFORMED", 400},
		{"oversized", "POST", path, "localhost:8765", "", "pow=" + strings.Repeat("X", (1<<20)+1), 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://"+tc.host+tc.path, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			handler(w, r)
			if w.Code != tc.want {
				t.Fatalf("status=%d want=%d", w.Code, tc.want)
			}
			if strings.Contains(w.Body.String(), "PRIVATE_") {
				t.Fatal("private result in response")
			}
		})
	}
	select {
	case value := <-results:
		if value != captureFixture() {
			t.Fatal("wrong result kind")
		}
	default:
		t.Fatal("no proof captured")
	}
	cancel()
	r := httptest.NewRequest("POST", "http://localhost:8765"+path, strings.NewReader("pow="+url.QueryEscape(captureFixture())))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler(w, r)
	if w.Code != 410 {
		t.Fatal("late result accepted")
	}
	select {
	case <-results:
		t.Fatal("late result delivered")
	default:
	}
}

type recordingCaptureTransport struct{ calls int }

func (t *recordingCaptureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.calls++
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("page")), Request: r}, nil
}
func TestPowCaptureCannotConsumeVerification(t *testing.T) {
	rt := &recordingCaptureTransport{}
	capture := captureReadOnlyTransport{rt}
	for _, method := range []string{"GET", "HEAD", "POST", "PUT"} {
		r := httptest.NewRequest(method, "https://api.vk.ru/method/captchaNotRobot.check", strings.NewReader("session_token=PRIVATE_SESSION"))
		resp, err := capture.RoundTrip(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if (method == "POST" || method == "PUT") && resp.StatusCode != 409 {
			t.Fatal("capture consumed challenge")
		}
	}
	if rt.calls != 2 {
		t.Fatal("mutation reached upstream")
	}
}
func TestCaptchaProxyPermitIsCancellable(t *testing.T) {
	proxyPermit <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	defer func() { <-proxyPermit }()
	_, err := CapturePowViaProxyWithPresenter(ctx, "https://id.vk.ru/captcha", net.Dialer{}, browserprofile.Profile{}, func(string) { t.Fatal("queued capture opened") })
	if err == nil {
		t.Fatal("cancelled permit succeeded")
	}
}

func TestCaptureServerRejectsLateBufferedProofAndClosesListener(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	key := make(chan string, 1)
	key <- captureFixture()
	cancel()
	start := func(*http.Server, string) error { return nil }
	_, err := runCaptchaServerWithStart(ctx, http.NewServeMux(), "http://localhost:8765/", key, nil, "fixture", func(string) {}, start)
	if err == nil {
		t.Fatal("late buffered proof accepted after STOP")
	}
}

func TestCaptureServerReleasesItsListenerAfterProof(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	path, err := newPowCapturePath()
	if err != nil {
		t.Fatal(err)
	}
	key := make(chan string, 1)
	handler := powCaptureHandler(ctx, path, key)
	start := func(server *http.Server, _ string) error { go func() { _ = server.Serve(listener) }(); return nil }
	present := func(string) {
		req, _ := http.NewRequestWithContext(ctx, "POST", "http://"+address+path, strings.NewReader("pow="+url.QueryEscape(captureFixture())))
		req.Host = "localhost:8765"
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Error("fixture capture unreachable")
			cancel()
			return
		}
		_ = response.Body.Close()
	}
	got, err := runCaptchaServerWithStart(context.WithValue(ctx, quietPresenterKey{}, true), handler, "http://"+address, key, nil, "fixture", present, start)
	if err != nil || got != captureFixture() {
		t.Fatal("capture failed")
	}
	rebound, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal("capture listener retained")
	}
	rebound.Close()
}
