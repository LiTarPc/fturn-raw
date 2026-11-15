package captcha

import "testing"

func TestParseErrorIgnoresNonCaptchaErrors(t *testing.T) {
	for _, code := range []float64{5, 6, 10, 29} {
		got := ParseError(map[string]any{
			"error_code":   code,
			"error_msg":    "API error",
			"redirect_uri": "https://id.vk.ru/captcha?session_token=test",
		})
		if got != nil {
			t.Errorf("code %v parsed as a captcha: %+v", code, got)
		}
	}
}

func TestParseErrorSmartCaptchaWithoutSid(t *testing.T) {
	errData := map[string]any{
		"error_code":   float64(14),
		"error_msg":    "Captcha needed",
		"redirect_uri": "https://id.vk.ru/not_robot_captcha?domain=vk.com&session_token=token",
	}

	got := ParseError(errData)
	if got == nil {
		t.Fatal("ParseError returned nil")
	}
	if !got.IsCaptcha() {
		t.Fatalf("IsCaptcha = false, got %+v", got)
	}
	if got.CaptchaSid != "" {
		t.Fatalf("CaptchaSid = %q, want empty", got.CaptchaSid)
	}
	if got.SessionToken != "token" {
		t.Fatalf("SessionToken = %q, want token", got.SessionToken)
	}
}
