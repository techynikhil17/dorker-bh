package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/html"
)

// Client fetches public HTML results from the selected search engines.
type Client struct {
	timeout time.Duration
	retries int
	proxies []*url.URL
	next    atomic.Uint64
	mu      sync.Mutex
	clients map[string]*http.Client
}

var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 14.5; rv:127.0) Gecko/20100101 Firefox/127.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15",
}

func NewClient(timeout time.Duration, retries int, rawProxies []string) (*Client, error) {
	c := &Client{timeout: timeout, retries: retries, clients: make(map[string]*http.Client)}
	for _, raw := range rawProxies {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5" && u.Scheme != "socks5h") {
			return nil, fmt.Errorf("invalid proxy %q (expected http, https or socks5 URL)", raw)
		}
		c.proxies = append(c.proxies, u)
	}
	return c, nil
}

func (c *Client) httpClient(proxy *url.URL) *http.Client {
	key := "direct"
	if proxy != nil {
		key = proxy.String()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing := c.clients[key]; existing != nil {
		return existing
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 20
	if proxy != nil {
		transport.Proxy = http.ProxyURL(proxy)
	}
	client := &http.Client{Transport: transport, Timeout: c.timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if len(via) > 0 && req.URL.Hostname() != via[0].URL.Hostname() {
			return errors.New("search engine redirected to another host")
		}
		return nil
	}}
	c.clients[key] = client
	return client
}

func (c *Client) proxy() *url.URL {
	if len(c.proxies) == 0 {
		return nil
	}
	return c.proxies[(c.next.Add(1)-1)%uint64(len(c.proxies))]
}

func endpoint(engine, query string) (string, error) {
	q := url.QueryEscape(query)
	switch engine {
	case "duckduckgo":
		return "https://html.duckduckgo.com/html/?q=" + q, nil
	case "bing":
		return "https://www.bing.com/search?q=" + q, nil
	case "google":
		return "https://www.google.com/search?q=" + q + "&num=100", nil
	default:
		return "", fmt.Errorf("unsupported engine %q", engine)
	}
}

func (c *Client) Search(ctx context.Context, engine, query string) ([]string, error) {
	address, err := endpoint(engine, query)
	if err != nil {
		return nil, err
	}
	return c.searchAt(ctx, engine, address)
}

func (c *Client) searchAt(ctx context.Context, engine, address string) ([]string, error) {
	for attempt := 0; attempt <= c.retries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", userAgents[rand.Intn(len(userAgents))])
		req.Header.Set("Accept", "text/html,application/xhtml+xml")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		resp, err := c.httpClient(c.proxy()).Do(req)
		retry := err != nil && ctx.Err() == nil
		if err == nil {
			if resp.StatusCode == http.StatusOK {
				results, parseErr := ParseResults(engine, io.LimitReader(resp.Body, 4<<20))
				closeErr := resp.Body.Close()
				if parseErr != nil {
					return nil, parseErr
				}
				if closeErr != nil {
					return nil, closeErr
				}
				return results, nil
			}
			retry = resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable
			err = fmt.Errorf("%s returned HTTP %d", engine, resp.StatusCode)
			resp.Body.Close()
		}
		if !retry || attempt == c.retries {
			return nil, err
		}
		pause := time.Duration(1<<min(attempt, 6))*time.Second + time.Duration(rand.Intn(500))*time.Millisecond
		timer := time.NewTimer(pause)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, errors.New("retry loop exhausted")
}

// ParseResults accepts links only in engine result containers, then unwraps
// known result redirects. It deliberately does not follow arbitrary redirects.
func ParseResults(engine string, body io.Reader) ([]string, error) {
	root, err := html.Parse(body)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	var out []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" && isResult(engine, n) && !isAd(n) {
			if dest := destination(engine, attr(n, "href")); dest != "" {
				if _, exists := seen[dest]; !exists {
					seen[dest] = struct{}{}
					out = append(out, dest)
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return out, nil
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, class string) bool {
	for _, word := range strings.Fields(attr(n, "class")) {
		if word == class {
			return true
		}
	}
	return false
}

func isResult(engine string, n *html.Node) bool {
	switch engine {
	case "duckduckgo":
		return hasClass(n, "result__a")
	case "bing":
		for p := n.Parent; p != nil; p = p.Parent {
			if hasClass(p, "b_algo") {
				return true
			}
		}
	case "google":
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == html.ElementNode && child.Data == "h3" {
				return true
			}
		}
	}
	return false
}

func isAd(n *html.Node) bool {
	for p := n; p != nil; p = p.Parent {
		v := strings.ToLower(attr(p, "class") + " " + attr(p, "id"))
		if strings.Contains(v, "result--ad") || strings.Contains(v, "b_ad") || strings.Contains(v, "ads-ad") || strings.Contains(v, "ueierd") {
			return true
		}
	}
	return false
}

func destination(engine, raw string) string {
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Host == "" && engine == "google" && u.Path == "/url" {
		raw = "https://www.google.com" + raw
		u, err = url.Parse(raw)
		if err != nil {
			return ""
		}
	}
	host := strings.ToLower(u.Hostname())
	if engine == "duckduckgo" && (host == "duckduckgo.com" || strings.HasSuffix(host, ".duckduckgo.com")) {
		if value := u.Query().Get("uddg"); value != "" {
			u, err = url.Parse(value)
			if err != nil {
				return ""
			}
			host = strings.ToLower(u.Hostname())
		}
	}
	if engine == "google" && (host == "google.com" || strings.HasSuffix(host, ".google.com")) && u.Path == "/url" {
		value := u.Query().Get("q")
		if value == "" {
			return ""
		}
		u, err = url.Parse(value)
		if err != nil {
			return ""
		}
		host = strings.ToLower(u.Hostname())
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	if host == "" || net.ParseIP(host) == nil && strings.ContainsAny(host, " \t\r\n") {
		return ""
	}
	for _, self := range []string{"duckduckgo.com", "bing.com", "google.com", "googleadservices.com", "doubleclick.net"} {
		if host == self || strings.HasSuffix(host, "."+self) {
			return ""
		}
	}
	u.Fragment = ""
	return u.String()
}
