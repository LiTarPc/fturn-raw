package manual

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

type resourceFixtureTransport func(*http.Request) (*http.Response, error)

func (f resourceFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func mustResourceURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, e := url.Parse(raw)
	if e != nil {
		t.Fatal(e)
	}
	return u
}

func TestResourceWebpackPublicPathKeepsCDNDirectory(t *testing.T) {
	entry := "https://st.vk.ru/vkid/vkid-bff/dist/projects/notRobotCaptcha-web/entrypoints/entry.243b7a49.js"
	local := mustResourceURL(t, "http://localhost:8765"+localResourceURL(mustResourceURL(t, entry)))
	// Webpack strips query/fragment/file, then appends ../ to currentScript.src.
	base := mustResourceURL(t, regexp.MustCompile(`/[^/]+$`).ReplaceAllString(local.String(), "/")+"../")
	for _, relative := range []string{"chunks/js/iui_subapp.a79f2d53.js?cache-bust=true&retry-attempt=2", "chunks/js/394.e400a060.js", "chunks/css/iui_subapp.f2b1747b.css"} {
		loaded := base.ResolveReference(mustResourceURL(t, relative))
		upstream, e := upstreamResourceURL(loaded)
		if e != nil {
			t.Fatal(e)
		}
		want := "https://st.vk.ru/vkid/vkid-bff/dist/projects/notRobotCaptcha-web/" + relative
		if upstream.String() != want {
			t.Fatalf("asset=%s, want=%s", upstream, want)
		}
	}
	css := base.ResolveReference(mustResourceURL(t, "chunks/css/main.css"))
	font := css.ResolveReference(mustResourceURL(t, "../../fonts/400latin.woff2"))
	u, e := upstreamResourceURL(font)
	if e != nil || u.String() != "https://st.vk.ru/vkid/vkid-bff/dist/projects/notRobotCaptcha-web/fonts/400latin.woff2" {
		t.Fatal(u, e)
	}
}
func TestResourceHTTPPreservesPathQueryMIMEAndErrorStatus(t *testing.T) {
	requested := []string{}
	transport := resourceFixtureTransport(func(r *http.Request) (*http.Response, error) {
		requested = append(requested, r.URL.String())
		content := "application/javascript"
		body := "window.fixtureLoaded=true;"
		status := 200
		switch {
		case strings.HasSuffix(r.URL.Path, ".css"):
			content = "text/css"
			body = ".captcha{display:block}"
		case strings.HasSuffix(r.URL.Path, ".woff2"):
			content = "font/woff2"
			body = "wOF2"
		case strings.Contains(r.URL.Path, "missing"):
			content = "text/html"
			body = "<h1>Not Found</h1>"
			status = 404
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{content}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	handler := externalResourceHandler(transport, false, make(chan string, 1), nil)
	for _, item := range []struct {
		path, mime string
		status     int
	}{{"chunks/js/394.e400a060.js?retry-attempt=2", "application/javascript", 200}, {"chunks/css/main.css", "text/css", 200}, {"fonts/400latin.woff2", "font/woff2", 200}, {"chunks/js/missing.js", "text/html", 404}} {
		target := "https://st.vk.ru/vkid/vkid-bff/dist/projects/notRobotCaptcha-web/" + item.path
		recorder := httptest.NewRecorder()
		handler(recorder, httptest.NewRequest(http.MethodGet, "http://localhost:8765"+localResourceURL(mustResourceURL(t, target)), nil))
		res := recorder.Result()
		res.Body.Close()
		if res.StatusCode != item.status || res.Header.Get("Content-Type") != item.mime {
			t.Fatal(res.StatusCode, res.Header)
		}
		if requested[len(requested)-1] != target {
			t.Fatal(requested)
		}
	}
	before := len(requested)
	for _, raw := range []string{"https://ad.mail.ru/static/sync-loader.js", "https://127.0.0.1/private", "https://st.vk.ru.evil.invalid/script.js"} {
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest("GET", localResourceURL(mustResourceURL(t, raw)), nil))
		if w.Code != 403 {
			t.Fatal(raw, w.Code)
		}
	}
	if len(requested) != before {
		t.Fatal("forbidden origin reached transport")
	}
}
func TestResourceRoundTripAndInvalidOrigins(t *testing.T) {
	raw := "https://st.vk.ru/path/a%2Fb/font%20name.woff2?v=1%2F2&x=3"
	local := mustResourceURL(t, localResourceURL(mustResourceURL(t, raw)))
	up, e := upstreamResourceURL(local)
	if e != nil || up.String() != raw {
		t.Fatal(up, e)
	}
	for _, origin := range []string{"file://st.vk.ru", "https://user:pass@st.vk.ru", "https://st.vk.ru/other", "https://st.vk.ru?proxy=1"} {
		key := base64.RawURLEncoding.EncodeToString([]byte(origin))
		if _, e := upstreamResourceURL(mustResourceURL(t, resourceProxyPrefix+key+"/a.js")); e == nil {
			t.Fatal("invalid origin accepted", origin)
		}
	}
}
func TestResourceHTMLAndDecodedPathsUseNewNamespace(t *testing.T) {
	target := mustResourceURL(t, "https://id.vk.ru/not_robot_captcha")
	html := rewriteHTMLAttrsServerSide(`<script src="https://st.vk.ru/vkid/p/entrypoints/entry.js"></script><link href="https://st.vk.ru/vkid/p/chunks/css/main.css">`, target)
	if strings.Contains(html, "generic_proxy?") || strings.Count(html, resourceProxyPrefix) != 2 {
		t.Fatal(html)
	}
	for _, method := range []string{"captchaNotRobot.check", "captchaNotRobot.componentDone", "captchaNotRobot.leaveCaptcha", "captchaNotRobot.endSession"} {
		raw := "https://api.vk.ru/method/" + method
		for _, local := range []string{localResourceURL(mustResourceURL(t, raw)), "/generic_proxy?proxy_url=" + url.QueryEscape(raw)} {
			if got := captchaRequestPath(mustResourceURL(t, local)); got != "/method/"+method {
				t.Fatal(got)
			}
		}
	}
}

func resourceTestRoute(target string, legacy bool) string {
	u, _ := url.Parse(target)
	if legacy {
		return "/generic_proxy?proxy_url=" + url.QueryEscape(target)
	}
	return localResourceURL(u)
}
func TestResourceProxyPreservesEscapedPathPostBodyAndHeaders(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		target := "https://st.vk.ru/path/a%2Fb/font%20name.woff2?v=1%2F2&x=3"
		var gotURL, gotPath, gotRawPath, gotMethod, gotBody, gotHeader string
		transport := resourceFixtureTransport(func(r *http.Request) (*http.Response, error) {
			body, e := io.ReadAll(r.Body)
			if e != nil {
				return nil, e
			}
			gotURL, gotPath, gotRawPath = r.URL.String(), r.URL.Path, r.URL.RawPath
			gotMethod, gotBody, gotHeader = r.Method, string(body), r.Header.Get("X-Fixture")
			return &http.Response{StatusCode: http.StatusPartialContent, Header: http.Header{"Content-Type": {"font/woff2"}, "X-Content-Type-Options": {"nosniff"}}, Body: io.NopCloser(strings.NewReader("wOF2")), Request: r}, nil
		})
		req := httptest.NewRequest(http.MethodPost, resourceTestRoute(target, legacy), strings.NewReader("fixture=preserved"))
		req.Header.Set("X-Fixture", "preserved")
		w := httptest.NewRecorder()
		externalResourceHandler(transport, false, make(chan string, 1), nil)(w, req)
		if gotURL != target || gotPath != "/path/a/b/font name.woff2" || gotRawPath != "/path/a%2Fb/font%20name.woff2" || gotMethod != "POST" || gotBody != "fixture=preserved" || gotHeader != "preserved" {
			t.Fatalf("upstream request changed: legacy=%t url=%s path=%s raw=%s method=%s body=%s header=%s", legacy, gotURL, gotPath, gotRawPath, gotMethod, gotBody, gotHeader)
		}
		if w.Code != 206 || w.Header().Get("Content-Type") != "font/woff2" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("response status/MIME protection changed: %d %v", w.Code, w.Header())
		}
	}
}
func TestResourceProxyRejectsLegacyAndNamespacedUntrustedTargets(t *testing.T) {
	calls := 0
	transport := resourceFixtureTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("unexpected")), Request: r}, nil
	})
	h := externalResourceHandler(transport, false, make(chan string, 1), nil)
	for _, target := range []string{"https://ad.mail.ru/static/sync-loader.js", "https://127.0.0.1/private", "https://st.vk.ru.evil.invalid/asset.js", "https://evil.invalid/asset.js"} {
		for _, legacy := range []bool{false, true} {
			w := httptest.NewRecorder()
			h(w, httptest.NewRequest("GET", resourceTestRoute(target, legacy), nil))
			if w.Code != 403 {
				t.Fatalf("legacy=%t target=%s status=%d", legacy, target, w.Code)
			}
		}
	}
	for _, target := range []string{"file://st.vk.ru/a.js", "ftp://st.vk.ru/a.js", "https://user:password@st.vk.ru/a.js"} {
		w := httptest.NewRecorder()
		h(w, httptest.NewRequest("GET", resourceTestRoute(target, true), nil))
		if w.Code != 400 {
			t.Fatalf("invalid legacy target accepted: %s status=%d", target, w.Code)
		}
		parsed, _ := url.Parse(target)
		origin := parsed.Scheme + "://" + parsed.Host
		if parsed.User != nil {
			origin = parsed.Scheme + "://" + parsed.User.String() + "@" + parsed.Host
		}
		key := base64.RawURLEncoding.EncodeToString([]byte(origin))
		w = httptest.NewRecorder()
		h(w, httptest.NewRequest("GET", resourceProxyPrefix+key+"/a.js", nil))
		if w.Code != 400 {
			t.Fatalf("invalid namespace origin accepted: %s status=%d", target, w.Code)
		}
	}
	if calls != 0 {
		t.Fatal("untrusted request reached upstream")
	}
}
func TestResourceProxyRetainsCaptureReadOnlyTransport(t *testing.T) {
	calls := 0
	var path, rawQuery string
	upstream := resourceFixtureTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		path, rawQuery = r.URL.Path, r.URL.RawQuery
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/javascript"}}, Body: io.NopCloser(strings.NewReader("fixture")), Request: r}, nil
	})
	h := externalResourceHandler(captureReadOnlyTransport{upstream}, true, make(chan string, 1), nil)
	for _, operation := range []string{"captchaNotRobot.check", "captchaNotRobot.componentDone", "captchaNotRobot.leaveCaptcha", "captchaNotRobot.endSession"} {
		for _, method := range []string{"POST", "PUT"} {
			for _, legacy := range []bool{false, true} {
				w := httptest.NewRecorder()
				h(w, httptest.NewRequest(method, resourceTestRoute("https://api.vk.ru/method/"+operation, legacy), strings.NewReader("fixture=value")))
				if w.Code != 409 {
					t.Fatalf("capture verification request forwarded: method=%s legacy=%t status=%d", method, legacy, w.Code)
				}
			}
		}
	}
	if calls != 0 {
		t.Fatal("capture mutation reached upstream")
	}
	for _, method := range []string{"GET", "HEAD"} {
		for _, legacy := range []bool{false, true} {
			w := httptest.NewRecorder()
			h(w, httptest.NewRequest(method, resourceTestRoute("https://st.vk.ru/vkid/p/chunks/js/main.js?v=1%2F2", legacy), nil))
			if w.Code != 200 || path != "/vkid/p/chunks/js/main.js" || rawQuery != "v=1%2F2" {
				t.Fatalf("capture resource did not retain real upstream path: %d %s %s", w.Code, path, rawQuery)
			}
		}
	}
}
func TestResourceQueryCannotImpersonateVerificationResponse(t *testing.T) {
	keys := make(chan string, 1)
	upstream := resourceFixtureTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader("{\"response\":{\"success_token\":\"fixture-token\"}}")), Request: r}, nil
	})
	h := externalResourceHandler(upstream, false, keys, nil)
	for _, legacy := range []bool{false, true} {
		w := httptest.NewRecorder()
		h(w, httptest.NewRequest("GET", resourceTestRoute("https://st.vk.ru/chunks/main.js?label=captchaNotRobot.check", legacy), nil))
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		select {
		case <-keys:
			t.Fatal("query marker promoted a resource response into a success token")
		default:
		}
	}
}
