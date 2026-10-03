package captcha

import "strings"

// captchaLimitedResponse recognizes explicit provider refusal; it contains no
// retry or alternative identity logic.
func captchaLimitedResponse(raw map[string]any) bool {
	if r, ok := raw["response"].(map[string]any); ok {
		if strings.EqualFold(captchaStringifyAny(r["status"]), "error_limit") || strings.EqualFold(captchaStringifyAny(r["show_captcha_type"]), "error_limit") {
			return true
		}
	}
	if e, ok := raw["error"].(map[string]any); ok {
		c := captchaStringifyAny(e["error_code"])
		return c == "6" || c == "29"
	}
	return false
}
