// Package bypassrules defines the shared, versioned UI/core bypass configuration.
package bypassrules

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"sort"
	"strings"
)

const MaxData = 2 << 20
const MaxRules = 512
const RUURL = "https://www.ipdeny.com/ipblocks/data/aggregated/ru-aggregated.zone"

type Settings struct {
	Enabled bool     `json:"enabled"`
	RU      bool     `json:"ru"`
	Sites   []string `json:"sites"`
	Apps    []string `json:"apps"`
}
type Config struct {
	Version int `json:"version"`
	Settings
	CIDRs []string `json:"cidrs,omitempty"`
}

func Normalize(s Settings) (Settings, error) {
	if len(s.Sites) > MaxRules || len(s.Apps) > MaxRules {
		return s, errors.New("too many bypass rules (maximum 512 per list)")
	}
	s.Sites = append([]string{}, s.Sites...)
	s.Apps = append([]string{}, s.Apps...)
	for i, v := range s.Sites {
		n, e := Site(v)
		if e != nil {
			return s, e
		}
		s.Sites[i] = n
	}
	for i, v := range s.Apps {
		v = strings.TrimSpace(v)
		if len(v) > 1024 || strings.ContainsAny(v, "\r\n\x00\"*?") || !strings.HasSuffix(strings.ToLower(v), ".exe") {
			return s, fmt.Errorf("invalid application %q: use an .exe name or full path", v)
		}
		if strings.ContainsAny(v, `\/:`) && !(len(v) > 3 && v[1] == ':' && (v[2] == '\\' || v[2] == '/')) {
			return s, fmt.Errorf("application path must be absolute: %q", v)
		}
		s.Apps[i] = strings.ToLower(strings.ReplaceAll(v, "/", `\`))
	}
	s.Sites = unique(s.Sites)
	s.Apps = unique(s.Apps)
	return s, nil
}
func Site(v string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	if len(v) > 1024 {
		return "", errors.New("site rule is too long")
	}
	if p, e := netip.ParsePrefix(v); e == nil {
		if !p.Addr().Is4() || p.Bits() == 0 {
			return "", errors.New("use IPv4 CIDR /1../32")
		}
		return p.Masked().String(), nil
	}
	if ip, e := netip.ParseAddr(v); e == nil {
		if !ip.Is4() {
			return "", errors.New("IPv6 is blocked; use IPv4")
		}
		return ip.String(), nil
	}
	if strings.Contains(v, "://") {
		u, e := url.Parse(v)
		if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
			return "", errors.New("invalid site URL")
		}
		v = u.Hostname()
	}
	v = strings.TrimPrefix(v, "*.")
	v = strings.TrimPrefix(v, ".")
	v = strings.TrimSuffix(v, ".")
	if ip, e := netip.ParseAddr(v); e == nil {
		if !ip.Is4() {
			return "", errors.New("IPv6 is blocked")
		}
		return ip.String(), nil
	}
	if len(v) == 0 || len(v) > 253 {
		return "", errors.New("invalid domain")
	}
	for _, label := range strings.Split(v, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("invalid domain %q", v)
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", fmt.Errorf("invalid domain %q; use punycode for international names", v)
			}
		}
	}
	return v, nil
}
func unique(v []string) []string {
	sort.Strings(v)
	out := make([]string, 0, len(v))
	for _, s := range v {
		if len(out) == 0 || out[len(out)-1] != s {
			out = append(out, s)
		}
	}
	return out
}
func ParseCIDRs(r io.Reader) ([]string, error) {
	data, e := io.ReadAll(io.LimitReader(r, MaxData+1))
	if e != nil {
		return nil, e
	}
	if len(data) > MaxData {
		return nil, errors.New("CIDR list exceeds 2 MiB")
	}
	scan := bufio.NewScanner(strings.NewReader(string(data)))
	out := []string{}
	for scan.Scan() {
		v := strings.TrimSpace(scan.Text())
		if v == "" {
			continue
		}
		p, e := netip.ParsePrefix(v)
		if e != nil || !p.Addr().Is4() || p.Bits() < 1 {
			return nil, fmt.Errorf("invalid IPv4 CIDR %q", v)
		}
		out = append(out, p.Masked().String())
		if len(out) > 50000 {
			return nil, errors.New("too many CIDRs")
		}
	}
	if e = scan.Err(); e != nil {
		return nil, e
	}
	if len(out) == 0 {
		return nil, errors.New("empty CIDR list")
	}
	return unique(out), nil
}
func Decode(r io.Reader) (Config, error) {
	var c Config
	data, e := io.ReadAll(io.LimitReader(r, MaxData+1))
	if e != nil {
		return c, e
	}
	if len(data) > MaxData {
		return c, errors.New("configuration exceeds 2 MiB")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if e := d.Decode(&c); e != nil {
		return c, e
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return c, errors.New("extra configuration data")
	}
	if c.Version != 1 {
		return c, errors.New("unsupported bypass configuration version")
	}
	s, e := Normalize(c.Settings)
	if e != nil {
		return c, e
	}
	c.Settings = s
	if len(c.CIDRs) > 50000 {
		return c, errors.New("too many CIDRs")
	}
	if len(c.CIDRs) > 0 {
		c.CIDRs, e = ParseCIDRs(strings.NewReader(strings.Join(c.CIDRs, "\n")))
	}
	return c, e
}
