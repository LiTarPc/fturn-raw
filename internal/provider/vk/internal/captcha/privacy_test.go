package captcha

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	fhttp "github.com/bogdanfinn/fhttp"
)

type privacyLogger struct{ bytes.Buffer }

func (l *privacyLogger) Debugf(f string, a ...any) { fmt.Fprintf(&l.Buffer, f+"\n", a...) }
func (l *privacyLogger) Infof(f string, a ...any)  { l.Debugf(f, a...) }
func (l *privacyLogger) Warnf(f string, a ...any)  { l.Debugf(f, a...) }
func (l *privacyLogger) Errorf(f string, a ...any) { l.Debugf(f, a...) }
func (*privacyLogger) DebugEnabled() bool          { return true }

func TestDebugExchangeOmitsHeadersAndProofPayload(t *testing.T) {
	l := &privacyLogger{}
	req, err := fhttp.NewRequest("POST", "https://api.example.test/method/captchaNotRobot.check?session_token=PRIVATE_QUERY", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Cookie", "PRIVATE_COOKIE")
	req.Header.Set("Authorization", "Bearer PRIVATE_AUTH")
	req.Header.Set("X-Unclassified", "PRIVATE_HEADER")
	s := &captchaSession{log: l}
	s.dumpExchange(req, []byte(`hash=PRIVATE_POW&payload=PRIVATE_PAYLOAD`))
	got := l.String()
	for _, marker := range []string{"PRIVATE_QUERY", "PRIVATE_COOKIE", "PRIVATE_AUTH", "PRIVATE_HEADER", "PRIVATE_POW", "PRIVATE_PAYLOAD"} {
		if strings.Contains(got, marker) {
			t.Fatalf("private marker %s in debug", marker)
		}
	}
	if !strings.Contains(got, "captchaNotRobot.check") || !strings.Contains(got, "body_bytes=") || !strings.Contains(got, "header_count=") {
		t.Fatal("lost safe operation counters")
	}
}

func TestSafeResponseMasksProofAndPayloadFields(t *testing.T) {
	got := SafeResponse(map[string]any{"pow": "PRIVATE_POW", "payload": map[string]any{"unclassified": "PRIVATE_PAYLOAD"}, "status": "BOT", "error_code": 10})
	for _, marker := range []string{"PRIVATE_POW", "PRIVATE_PAYLOAD"} {
		if strings.Contains(got, marker) {
			t.Fatalf("private marker %s in response", marker)
		}
	}
	if !strings.Contains(got, `"status":"BOT"`) || !strings.Contains(got, `"error_code":10`) {
		t.Fatal("lost safe provider status")
	}
}
