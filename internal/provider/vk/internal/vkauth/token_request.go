package vkauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	neturl "net/url"

	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk/internal/browserprofile"
	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk/internal/captcha"

	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
)

func (c *Client) openJoinPage(ctx context.Context, httpClient tlsclient.HttpClient, profile browserprofile.Profile, link string) error {
	req, err := fhttp.NewRequestWithContext(ctx, fhttp.MethodGet, "https://vk.ru/call/join/"+link, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Sec-Fetch-Site", "none")
	browserprofile.ApplyFhttp(req, profile)

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			c.log.Warnf("[VK Auth] close join page body: %s", closeErr)
		}
	}()
	if resp.StatusCode == 429 {
		return captcha.NewRateLimitError("join page HTTP 429", resp.Header.Get("Retry-After"))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("join page HTTP %d", resp.StatusCode)
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return err
	}
	return nil
}

func (c *Client) doRequest(ctx context.Context, httpClient tlsclient.HttpClient, profile browserprofile.Profile, data, url string) (map[string]any, error) {
	parsedURL, err := neturl.Parse(url)
	if err != nil {
		return nil, fmt.Errorf("parse request URL: %w", err)
	}
	domain := parsedURL.Hostname()

	req, err := fhttp.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer([]byte(data)))
	if err != nil {
		return nil, err
	}
	req.Host = domain
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Origin", "https://vk.ru")
	req.Header.Set("Referer", "https://vk.ru/")
	req.Header.Set("Sec-Fetch-Site", "same-site")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	browserprofile.ApplyFhttp(req, profile)

	httpResp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := httpResp.Body.Close(); closeErr != nil {
			c.log.Warnf("[VK Auth] close response body: %s", closeErr)
		}
	}()

	if httpResp.StatusCode == 429 {
		body, _ := io.ReadAll(io.LimitReader(httpResp.Body, 64*1024))
		return nil, captcha.WithResponseBody(captcha.NewRateLimitError(captcha.SafeURL(url)+" HTTP 429", httpResp.Header.Get("Retry-After")), body)
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(httpResp.Body, 64*1024))
		return nil, captcha.WithResponseBody(fmt.Errorf("%s HTTP %d", captcha.SafeURL(url), httpResp.StatusCode), body)
	}
	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, err
	}
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	c.log.Debugf("[VK Auth] response source=%s HTTP=%d response=%s", captcha.SafeURL(url), httpResp.StatusCode, captcha.SafeResponse(resp))
	if captcha.LimitedResponse(resp) {
		return nil, captcha.WithResponse(captcha.NewRateLimitError(captcha.SafeURL(url)+" API limit", httpResp.Header.Get("Retry-After")), resp)
	}
	if errData, ok := resp["error"].(map[string]any); ok && errData["error_code"] == float64(10) {
		return nil, fmt.Errorf("%w: response=%s", ErrVKServiceUnavailable, captcha.SafeResponse(resp))
	}
	return resp, nil
}
