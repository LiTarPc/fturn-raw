package captchaipc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func ipcV2(t *testing.T) (*Server, *eventBuffer) {
	t.Helper()
	out := &eventBuffer{}
	s, err := New(context.Background(), Config{Token: testBearer, Generation: "generation_v2", Output: out, Protocol: 2, BrowserTimeout: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, out
}
func TestIPCV2CaptureIsSeparateFromSolutionAndAuthorization(t *testing.T) {
	s, out := ipcV2(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	id, err := s.BeginCapture(ctx, "http://localhost:8765/captcha?session_token=PRIVATE_SESSION", cancel)
	if err != nil {
		t.Fatal(err)
	}
	code, raw := ipcRequest(t, s, "GET", "/v1/captcha/"+id, "", testBearer, s.cfg.Generation, "", "")
	var d Challenge
	if code != 200 || json.Unmarshal(raw, &d) != nil || d.Purpose != "pow_capture" || d.Mode != "browser" {
		t.Fatal("wrong capture descriptor")
	}
	time.Sleep(25 * time.Millisecond)
	if strings.Contains(out.String(), `"mode":"manual"`) {
		t.Fatal("capture promoted by old45stimer")
	}
	s.End(id, "captured")
	if strings.Contains(out.String(), `"phase":"resolved"`) || strings.Contains(out.String(), `"phase":"authorized"`) {
		t.Fatal("proof treated as auth")
	}
	if !strings.Contains(out.String(), `"v":2`) || strings.Count(out.String(), `"phase":"captured"`) != 1 {
		t.Fatal("missing typed capture result")
	}
	code, _ = ipcRequest(t, s, "POST", "/v1/captcha/"+id+"/host", `{"generation":"generation_v2","status":"available"}`, testBearer, s.cfg.Generation, "", "")
	if code != 410 {
		t.Fatal("late callback accepted")
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	next, err := s.Begin(ctx2, "http://localhost:8765/new", "manual")
	if err != nil || next == id {
		t.Fatal("manual reused capture")
	}
	s.Authorized()
	if strings.Contains(out.String(), `"phase":"authorized"`) {
		t.Fatal("authorized during pending manual")
	}
	s.End(next, "resolved")
	s.Authorized()
	if strings.Count(out.String(), `"phase":"authorized"`) != 1 {
		t.Fatal("fresh creds completion not represented")
	}
	for _, secret := range []string{"PRIVATE_SESSION", testBearer, "localhost:8765"} {
		if strings.Contains(out.String(), secret) {
			t.Fatal("secret in event")
		}
	}
}
func TestIPCV2UnavailableCaptureCancelsInsteadOfExposingPage(t *testing.T) {
	for _, status := range []string{"unavailable", "manual_requested"} {
		t.Run(status, func(t *testing.T) {
			s, out := ipcV2(t)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			id, err := s.BeginCapture(ctx, "http://localhost:8765/", cancel)
			if err != nil {
				t.Fatal(err)
			}
			code, _ := ipcRequest(t, s, "POST", "/v1/captcha/"+id+"/host", `{"generation":"generation_v2","status":"`+status+`","reason":"host_unavailable"}`, testBearer, s.cfg.Generation, "", "")
			if code != 204 {
				t.Fatal(code)
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("capture not cancelled")
			}
			if strings.Contains(out.String(), `"mode":"manual"`) || strings.Count(out.String(), `"phase":"unavailable"`) != 1 {
				t.Fatal("wrong fallback event")
			}
		})
	}
}
func TestIPCV2CaptureTTLAndStopInvalidateID(t *testing.T) {
	s, out := ipcV2(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	id, err := s.BeginCapture(ctx, "http://localhost:8765/", cancel)
	if err != nil {
		t.Fatal(err)
	}
	<-ctx.Done()
	until := time.Now().Add(time.Second)
	for !strings.Contains(out.String(), `"phase":"expired"`) && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(out.String(), `"phase":"expired"`) {
		t.Fatal("no expiry")
	}
	code, _ := ipcRequest(t, s, "GET", "/v1/captcha/"+id, "", testBearer, s.cfg.Generation, "", "")
	if code != 410 {
		t.Fatal("expired URL served")
	}
	c2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	_, err = s.BeginCapture(c2, "http://localhost:8765/", cancel2)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	select {
	case <-c2.Done():
	case <-time.After(time.Second):
		t.Fatal("STOP not propagated")
	}
}
func TestIPCV1DoesNotAcceptAdvancedCapture(t *testing.T) {
	s, _ := ipcServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := s.BeginCapture(ctx, "http://localhost:8765/", cancel); err == nil {
		t.Fatal("v1 capture accepted")
	}
}
