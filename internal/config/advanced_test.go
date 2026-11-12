package config

import (
	"io"
	"testing"
)

func TestAdvancedCaptchaIsExplicitAndManualOnlyWins(t *testing.T) {
	for _, tc := range []struct {
		name, json       string
		advanced, manual bool
	}{
		{"legacy", `{"peer":"127.0.0.1:56010","vk":{"links":["test"]}}`, false, false},
		{"advanced", `{"peer":"127.0.0.1:56010","vk":{"links":["test"],"advancedCaptcha":true}}`, true, false},
		{"manual", `{"peer":"127.0.0.1:56010","vk":{"links":["test"],"advancedCaptcha":true,"manualCaptcha":true}}`, true, true},
		{"auto disabled", `{"peer":"127.0.0.1:56010","vk":{"links":["test"],"advancedCaptcha":true,"autoCaptcha":false}}`, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := ParseClientJSON([]byte(tc.json), "")
			if err != nil {
				t.Fatal(err)
			}
			if c.VK.AdvancedCaptcha != tc.advanced || c.VK.ManualCaptcha != tc.manual {
				t.Fatal("wrong precedence")
			}
		})
	}
	c, err := ParseClient([]string{"-peer", "127.0.0.1:56010", "-links", "test", "-advanced-captcha", "-auto-captcha=false"}, io.Discard)
	if err != nil || !c.VK.AdvancedCaptcha || !c.VK.ManualCaptcha {
		t.Fatal("CLI config missing")
	}
}
