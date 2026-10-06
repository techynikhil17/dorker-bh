package discovery

import (
	"errors"
	"net"
	"net/url"
	"path"
	"sort"
	"strings"
)

type Scope struct {
	Target                          string
	IncludeSubdomains, AllowPrivate bool
}

func NewScope(target string, includeSubdomains, allowPrivate bool) (Scope, error) {
	target = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(target), "."))
	if target == "" || strings.ContainsAny(target, "/:@ ") {
		return Scope{}, errors.New("invalid target")
	}
	return Scope{target, includeSubdomains, allowPrivate}, nil
}

func (s Scope) ContainsString(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && s.Contains(u)
}
func (s Scope) Contains(u *url.URL) bool {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return false
	}
	h := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	return h == s.Target || (s.IncludeSubdomains && strings.HasSuffix(h, "."+s.Target))
}

func IsPublicIP(ip net.IP) bool {
	if ip == nil || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, raw := range []string{"192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24", "2001:db8::/32"} {
		_, block, _ := net.ParseCIDR(raw)
		if block.Contains(ip) {
			return false
		}
	}
	return true
}

type Observation struct {
	URL, Target, Content, ContentType, FilterState string
	Sources, Queries                               []string
	Verified                                       bool
	StatusCode, Depth                              int
}

func Canonicalize(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return "", errors.New("invalid HTTP URL")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	port := u.Port()
	if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		port = ""
	}
	u.Host = host
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	}
	u.Fragment = ""
	u.Path = path.Clean("/" + strings.TrimPrefix(u.Path, "/"))
	if u.Path == "." {
		u.Path = "/"
	}
	tracking := map[string]struct{}{"msockid": {}, "utm_source": {}, "utm_medium": {}, "utm_campaign": {}, "utm_term": {}, "utm_content": {}}
	parts := strings.Split(u.RawQuery, "&")
	kept := parts[:0]
	for _, part := range parts {
		if part == "" {
			continue
		}
		key := strings.SplitN(part, "=", 2)[0]
		if decoded, e := url.QueryUnescape(key); e == nil {
			if _, drop := tracking[strings.ToLower(decoded)]; drop {
				continue
			}
		}
		kept = append(kept, part)
	}
	u.RawQuery = strings.Join(kept, "&")
	return u.String(), nil
}

func Merge(dst map[string]*Observation, in Observation) {
	key, err := Canonicalize(in.URL)
	if err != nil {
		return
	}
	in.URL = key
	if old := dst[key]; old != nil {
		old.Sources = union(old.Sources, in.Sources)
		old.Queries = union(old.Queries, in.Queries)
		if in.Verified {
			old.Verified = true
			old.StatusCode = in.StatusCode
			old.ContentType = in.ContentType
			old.Content = in.Content
		}
		if old.Depth == 0 || (in.Depth > 0 && in.Depth < old.Depth) {
			old.Depth = in.Depth
		}
		return
	}
	in.Sources = union(nil, in.Sources)
	in.Queries = union(nil, in.Queries)
	dst[key] = &in
}

func union(a, b []string) []string {
	m := map[string]struct{}{}
	for _, x := range append(a, b...) {
		if x != "" {
			m[x] = struct{}{}
		}
	}
	out := make([]string, 0, len(m))
	for x := range m {
		out = append(out, x)
	}
	sort.Strings(out)
	return out
}
