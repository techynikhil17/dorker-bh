package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/publicsuffix"
)

// Client fetches public HTML results from the selected search engines.
type Client struct {
	timeout                   time.Duration
	retries                   int
	proxies                   []*url.URL
	next                      atomic.Uint64
	mu                        sync.Mutex
	clients                   map[string]*http.Client
	commonCrawlIndex          string
	commonCrawlMu             sync.Mutex
	lastCommonCrawl           time.Time
	googleAPIKey, googleCSEID string
}

type statusError struct{ Code int }

func (e statusError) Error() string { return fmt.Sprintf("provider returned HTTP %d", e.Code) }

var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/155.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 15.0; rv:154.0) Gecko/20100101 Firefox/154.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 15_0) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.0 Safari/605.1.15",
}

var bodyPool = sync.Pool{New: func() any { return bytes.NewBuffer(make([]byte, 0, 64<<10)) }}

func releaseBody(body *bytes.Buffer) {
	if body == nil {
		return
	}
	body.Reset()
	if body.Cap() <= 512<<10 {
		bodyPool.Put(body)
	}
}

func NewClient(timeout time.Duration, retries int, rawProxies []string) (*Client, error) {
	c := &Client{timeout: timeout, retries: retries, clients: make(map[string]*http.Client), googleAPIKey: os.Getenv("DORKER_GOOGLE_API_KEY"), googleCSEID: os.Getenv("DORKER_GOOGLE_CSE_ID")}
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
	if engine == "wayback" || engine == "commoncrawl" {
		return c.searchArchive(ctx, engine, query)
	}
	if engine == "google" && (c.googleAPIKey != "" || c.googleCSEID != "") {
		if c.googleAPIKey == "" || c.googleCSEID == "" {
			return nil, errors.New("Google API requires both DORKER_GOOGLE_API_KEY and DORKER_GOOGLE_CSE_ID")
		}
		return c.searchGoogleAPI(ctx, query)
	}
	address, err := endpoint(engine, query)
	if err != nil {
		return nil, err
	}
	return c.searchAt(ctx, engine, address)
}

func (c *Client) searchAt(ctx context.Context, engine, address string) ([]string, error) {
	body, err := c.fetch(ctx, address)
	var status statusError
	if engine == "duckduckgo" && errors.As(err, &status) && status.Code == http.StatusAccepted {
		// DuckDuckGo occasionally challenges browser-like header sets with 202.
		// Its HTML endpoint also accepts a minimal compatibility UA.
		body, err = c.fetchWithUA(ctx, address, "Mozilla/5.0")
	}
	if err != nil {
		return nil, err
	}
	defer releaseBody(body)
	return ParseResults(engine, bytes.NewReader(body.Bytes()))
}

func (c *Client) fetch(ctx context.Context, address string) (*bytes.Buffer, error) {
	return c.fetchWithUA(ctx, address, "")
}

func (c *Client) fetchWithUA(ctx context.Context, address, userAgent string) (*bytes.Buffer, error) {
	for attempt := 0; attempt <= c.retries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return nil, err
		}
		ua := userAgent
		if ua == "" {
			ua = userAgents[rand.Intn(len(userAgents))]
		}
		req.Header.Set("User-Agent", ua)
		if userAgent == "" {
			req.Header.Set("Accept", "text/html,application/xhtml+xml")
			req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		}
		resp, err := c.httpClient(c.proxy()).Do(req)
		retry := err != nil && ctx.Err() == nil
		if err == nil {
			if resp.StatusCode == http.StatusOK {
				// Reject oversized pages instead of silently checkpointing partial data.
				body := bodyPool.Get().(*bytes.Buffer)
				body.Reset()
				count, readErr := io.CopyN(body, resp.Body, (2<<20)+1)
				closeErr := resp.Body.Close()
				if readErr != nil && !errors.Is(readErr, io.EOF) {
					releaseBody(body)
					return nil, readErr
				}
				if count > 2<<20 {
					releaseBody(body)
					return nil, errors.New("provider response exceeds 2 MiB")
				}
				if closeErr != nil {
					releaseBody(body)
					return nil, closeErr
				}
				return body, nil
			}
			retry = resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout
			err = statusError{Code: resp.StatusCode}
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
	type frame struct {
		tag, href                     string
		ad, bingResult, googleHeading bool
	}
	var stack []frame
	var out []string
	seen := make(map[string]struct{})
	challenge := false
	add := func(raw string) {
		if dest := destination(engine, raw); dest != "" {
			if _, exists := seen[dest]; !exists {
				seen[dest] = struct{}{}
				out = append(out, dest)
			}
		}
	}
	z := html.NewTokenizer(body)
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			if err := z.Err(); err != nil && err != io.EOF {
				return nil, err
			}
			break
		}
		switch tt {
		case html.TextToken:
			text := strings.ToLower(string(z.Text()))
			if strings.Contains(text, "captcha") || strings.Contains(text, "unusual traffic") || strings.Contains(text, "verify you are human") {
				challenge = true
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			token := z.Token()
			f := frame{tag: token.Data}
			if len(stack) > 0 {
				f.ad = stack[len(stack)-1].ad
				f.bingResult = stack[len(stack)-1].bingResult
			}
			var class string
			for _, a := range token.Attr {
				switch a.Key {
				case "href":
					f.href = a.Val
				case "class", "id":
					class += " " + a.Val
				}
				if strings.Contains(strings.ToLower(a.Val), "/httpservice/retry/enablejs") {
					challenge = true
				}
			}
			lower := strings.ToLower(class)
			for _, marker := range []string{"result--ad", "b_ad", "ads-ad", "ueierd"} {
				if strings.Contains(lower, marker) {
					f.ad = true
				}
			}
			if strings.Contains(" "+class+" ", " b_algo ") {
				f.bingResult = true
			}
			if token.Data == "h3" {
				for i := len(stack) - 1; i >= 0; i-- {
					if stack[i].tag == "a" {
						stack[i].googleHeading = true
						break
					}
				}
			}
			if token.Data == "a" && !f.ad {
				if engine == "duckduckgo" && strings.Contains(" "+class+" ", " result__a ") {
					add(f.href)
				}
				if engine == "bing" && f.bingResult {
					add(f.href)
				}
			}
			if tt == html.StartTagToken {
				stack = append(stack, f)
			}
		case html.EndTagToken:
			tag, _ := z.TagName()
			for i := len(stack) - 1; i >= 0; i-- {
				f := stack[i]
				if f.tag == "a" && engine == "google" && f.googleHeading && !f.ad {
					add(f.href)
				}
				stack = stack[:i]
				if f.tag == string(tag) {
					break
				}
			}
		}
	}
	if challenge && len(out) == 0 {
		return nil, errors.New("search engine returned a bot challenge")
	}
	return out, nil
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
	if engine == "bing" && (host == "bing.com" || strings.HasSuffix(host, ".bing.com")) && u.Path == "/ck/a" {
		encoded := u.Query().Get("u")
		if strings.HasPrefix(encoded, "a1") {
			decoded, decodeErr := base64.RawURLEncoding.DecodeString(strings.TrimRight(encoded[2:], "="))
			if decodeErr == nil {
				u, err = url.Parse(string(decoded))
				if err != nil {
					return ""
				}
				host = strings.ToLower(u.Hostname())
			}
		}
	}
	if engine == "duckduckgo" && (host == "duckduckgo.com" || strings.HasSuffix(host, ".duckduckgo.com")) {
		if value := u.Query().Get("uddg"); value != "" {
			u, err = url.Parse(value)
			if err != nil {
				return ""
			}
			host = strings.ToLower(u.Hostname())
		}
	}
	if engine == "google" && isGoogleHost(host) && u.Path == "/url" {
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
	if isGoogleHost(host) {
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

func isGoogleHost(host string) bool {
	base, err := publicsuffix.EffectiveTLDPlusOne(host)
	return err == nil && strings.HasPrefix(base, "google.")
}
