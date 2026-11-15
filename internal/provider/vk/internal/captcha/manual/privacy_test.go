package manual

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type privacyLogger struct{ bytes.Buffer }

func (l *privacyLogger) Debugf(f string, a ...any) { fmt.Fprintf(&l.Buffer, f+"\n", a...) }
func (l *privacyLogger) Infof(f string, a ...any)  { l.Debugf(f, a...) }
func (l *privacyLogger) Warnf(f string, a ...any)  { l.Debugf(f, a...) }
func (l *privacyLogger) Errorf(f string, a ...any) { l.Debugf(f, a...) }
func (*privacyLogger) DebugEnabled() bool          { return true }

func TestBrowserDebugOmitsPrivateRequestAndErrorValues(t *testing.T) {
	oldLog, oldDebug := Log, Debug
	t.Cleanup(func() { Log = oldLog; Debug = oldDebug })
	for _, failed := range []bool{false, true} {
		l := &privacyLogger{}
		Log = l
		Debug = true
		body := "hash=PRIVATE_POW&payload=PRIVATE_PAYLOAD"
		req, err := http.NewRequest("POST", "https://api.example.test/method/captchaNotRobot.check?session_token=PRIVATE_QUERY", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Cookie", "PRIVATE_COOKIE")
		req.Header.Set("Authorization", "Bearer PRIVATE_AUTH")
		req.Header.Set("X-Unclassified", "PRIVATE_HEADER")
		req.Header.Set("Referer", "https://id.example.test/captcha?session_token=PRIVATE_REFERER")
		tr := &loggingTransport{started: time.Now(), rt: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			received, e := io.ReadAll(r.Body)
			if e != nil || string(received) != body {
				t.Fatal("debug consumed or changed request")
			}
			if failed {
				return nil, errors.New("transport PRIVATE_ERROR")
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: r}, nil
		})}
		resp, err := tr.RoundTrip(req)
		if resp != nil {
			resp.Body.Close()
		}
		if failed != (err != nil) {
			t.Fatal("transport result changed")
		}
		got := l.String()
		for _, marker := range []string{"PRIVATE_POW", "PRIVATE_PAYLOAD", "PRIVATE_QUERY", "PRIVATE_COOKIE", "PRIVATE_AUTH", "PRIVATE_HEADER", "PRIVATE_REFERER", "PRIVATE_ERROR"} {
			if strings.Contains(got, marker) {
				t.Fatalf("private marker %s in browser debug", marker)
			}
		}
		if !strings.Contains(got, "captchaNotRobot.check") || !strings.Contains(got, "body_bytes=") {
			t.Fatal("lost safe operation counters")
		}
	}
}
