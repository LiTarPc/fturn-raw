//go:build windows

package rawvpn

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

type controlDNSUpstream struct {
	name     string
	client   *http.Client
	endpoint string
}
type controlDNSPool struct {
	upstreams []controlDNSUpstream
	preferred atomic.Uint32
	timeLimit time.Duration
}

func newControlDNSPool(upstreams []controlDNSUpstream) *controlDNSPool {
	return &controlDNSPool{upstreams: upstreams, timeLimit: 4500 * time.Millisecond}
}
func (p *controlDNSPool) exchange(parent context.Context, query []byte) ([]byte, error) {
	if len(p.upstreams) == 0 {
		return nil, fmt.Errorf("no HTTPS DNS endpoints")
	}
	var question dnsmessage.Message
	if err := question.Unpack(query); err != nil || question.Response || len(question.Questions) != 1 {
		return nil, fmt.Errorf("invalid DNS query")
	}
	ctx, cancel := context.WithTimeout(parent, p.timeLimit)
	defer cancel()
	count := len(p.upstreams)
	start := int(p.preferred.Load()) % count
	var failures []error
	for attempt := 0; attempt < count; attempt++ {
		if ctx.Err() != nil {
			failures = append(failures, ctx.Err())
			break
		}
		index := (start + attempt) % count
		body, err := controlDoHQuery(ctx, p.upstreams[index], query, &question)
		if err == nil {
			previous := int(p.preferred.Swap(uint32(index))) % count
			if previous != index {
				serviceDNSLogger().Infof("[DNS] Service DoH switched from %s to %s via selected underlay", p.upstreams[previous].name, p.upstreams[index].name)
			}
			return body, nil
		}
		failures = append(failures, fmt.Errorf("%s: %w", p.upstreams[index].name, err))
		if ctx.Err() == nil {
			serviceDNSLogger().Warnf("[DNS] Service DoH %s failed: %v", p.upstreams[index].name, err)
		}
	}
	return nil, fmt.Errorf("HTTPS DNS unavailable: %w", errors.Join(failures...))
}
func controlDoHQuery(ctx context.Context, upstream controlDNSUpstream, query []byte, question *dnsmessage.Message) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream.endpoint, bytes.NewReader(query))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/dns-message")
	request.Header.Set("Accept", "application/dns-message")
	response, err := upstream.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || media != "application/dns-message" {
		return nil, fmt.Errorf("invalid DNS content type")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 65536))
	if err != nil {
		return nil, err
	}
	if len(body) < 12 || len(body) > 65535 {
		return nil, fmt.Errorf("invalid DNS response size")
	}
	var answer dnsmessage.Message
	if err = answer.Unpack(body); err != nil || !answer.Response || answer.ID != question.ID || len(answer.Questions) != 1 {
		return nil, fmt.Errorf("invalid DNS response")
	}
	q, a := question.Questions[0], answer.Questions[0]
	if !strings.EqualFold(q.Name.String(), a.Name.String()) || q.Type != a.Type || q.Class != a.Class {
		return nil, fmt.Errorf("DNS response question mismatch")
	}
	// NXDOMAIN is a legitimate result. Only provider-side transient DNS failures retry.
	if answer.RCode == dnsmessage.RCodeServerFailure || answer.RCode == dnsmessage.RCodeRefused || answer.Truncated {
		return nil, fmt.Errorf("DNS failure: %s (truncated=%t)", answer.RCode, answer.Truncated)
	}
	return body, nil
}
