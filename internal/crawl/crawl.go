package crawl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/techynikhil17/dorker-bh/internal/discovery"
)

type Config struct {
	Depth, Requests int
	Duration        time.Duration
	ResponseBytes   int64
	Concurrency     int
	Delay, Timeout  time.Duration
	AllowPrivate    bool
}
type Stats struct {
	Requests        int
	BudgetExhausted bool
}
type queued struct {
	url    string
	depth  int
	source string
}

func Run(ctx context.Context, scope discovery.Scope, seeds []string, cfg Config) ([]discovery.Observation, Stats, error) {
	if cfg.Depth < 0 || cfg.Requests < 1 || cfg.Duration <= 0 || cfg.ResponseBytes < 1 || cfg.Concurrency < 1 || cfg.Timeout <= 0 {
		return nil, Stats{}, errors.New("invalid crawl configuration")
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Duration)
	defer cancel()
	seen := map[string]struct{}{}
	observations := map[string]*discovery.Observation{}
	var current []queued
	add := func(raw string, depth int, source string) {
		canon, err := discovery.Canonicalize(raw)
		if err != nil || !scope.ContainsString(canon) {
			return
		}
		discovery.Merge(observations, discovery.Observation{URL: canon, Target: scope.Target, Sources: []string{source}, Depth: depth})
		if _, ok := seen[canon]; !ok && depth <= cfg.Depth {
			seen[canon] = struct{}{}
			current = append(current, queued{canon, depth, source})
		}
	}
	for _, seed := range seeds {
		add(seed, 0, "seed")
		if u, e := url.Parse(seed); e == nil {
			add(u.Scheme+"://"+u.Host+"/robots.txt", 0, "standard")
			add(u.Scheme+"://"+u.Host+"/sitemap.xml", 0, "standard")
		}
	}
	client := &http.Client{Timeout: cfg.Timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if !scope.Contains(req.URL) {
			return http.ErrUseLastResponse
		}
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return nil
	}}
	stats := Stats{}
	var count atomic.Int64
	for len(current) > 0 && ctx.Err() == nil {
		batch := current
		current = nil
		slots := cfg.Requests - int(count.Load())
		if slots <= 0 {
			stats.BudgetExhausted = true
			break
		}
		if len(batch) > slots {
			batch = batch[:slots]
			stats.BudgetExhausted = true
		}
		type result struct {
			item        queued
			status      int
			ctype, body string
			links       []link
			err         error
		}
		results := make(chan result, len(batch))
		sem := make(chan struct{}, cfg.Concurrency)
		var wg sync.WaitGroup
		for _, item := range batch {
			wg.Add(1)
			go func(item queued) {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					return
				}
				defer func() { <-sem }()
				if cfg.Delay > 0 {
					select {
					case <-time.After(cfg.Delay):
					case <-ctx.Done():
						return
					}
				}
				if !cfg.AllowPrivate {
					ips, e := net.DefaultResolver.LookupIP(ctx, "ip", mustURL(item.url).Hostname())
					if e != nil {
						results <- result{item: item, err: e}
						return
					}
					for _, ip := range ips {
						if !discovery.IsPublicIP(ip) {
							results <- result{item: item, err: fmt.Errorf("non-public address %s", ip)}
							return
						}
					}
				}
				req, e := http.NewRequestWithContext(ctx, http.MethodGet, item.url, nil)
				if e != nil {
					results <- result{item: item, err: e}
					return
				}
				req.Header.Set("User-Agent", "dorker-bh/recon")
				resp, e := client.Do(req)
				count.Add(1)
				if e != nil {
					results <- result{item: item, err: e}
					return
				}
				defer resp.Body.Close()
				limited := io.LimitReader(resp.Body, cfg.ResponseBytes+1)
				data, e := io.ReadAll(limited)
				if e != nil {
					results <- result{item: item, err: e}
					return
				}
				if int64(len(data)) > cfg.ResponseBytes {
					results <- result{item: item, status: resp.StatusCode, ctype: resp.Header.Get("Content-Type"), err: errors.New("response exceeds crawl-response-bytes")}
					return
				}
				body := string(data)
				results <- result{item: item, status: resp.StatusCode, ctype: resp.Header.Get("Content-Type"), body: body, links: extract(item.url, resp.Header.Get("Content-Type"), body)}
			}(item)
		}
		wg.Wait()
		close(results)
		for r := range results {
			if r.err != nil {
				continue
			}
			o := *observations[r.item.url]
			o.Verified = true
			o.StatusCode = r.status
			o.ContentType = r.ctype
			o.Content = r.body
			discovery.Merge(observations, o)
			for _, l := range r.links {
				add(l.url, r.item.depth+1, l.source)
			}
		}
	}
	stats.Requests = int(count.Load())
	out := make([]discovery.Observation, 0, len(observations))
	for _, o := range observations {
		out = append(out, *o)
	}
	if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return out, stats, ctx.Err()
	}
	return out, stats, nil
}

func mustURL(raw string) *url.URL { u, _ := url.Parse(raw); return u }
