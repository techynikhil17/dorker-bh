package crawl

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/techynikhil17/dorker-bh/internal/discovery"
)

func TestRunDiscoversHTMLRobotsSitemapAndJavaScript(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="/admin">x</a><script src="/app.js"></script>`)
	})
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "Sitemap: /sitemap.xml\nDisallow: /private\n")
	})
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<urlset><url><loc>/api</loc></url></urlset>`)
	})
	mux.HandleFunc("/app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		fmt.Fprint(w, `fetch("/graphql") //# sourceMappingURL=/app.js.map`)
	})
	s := httptest.NewServer(mux)
	defer s.Close()
	u, _ := url.Parse(s.URL)
	scope, _ := discovery.NewScope(u.Hostname(), false, true)
	obs, stats, err := Run(context.Background(), scope, []string{s.URL + "/"}, Config{Depth: 2, Requests: 20, Duration: time.Minute, ResponseBytes: 1 << 20, Concurrency: 3, Timeout: time.Second, AllowPrivate: true})
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, o := range obs {
		joined += o.URL + "\n"
	}
	for _, p := range []string{"/admin", "/api", "/graphql", "/private", "/app.js.map"} {
		if !strings.Contains(joined, p) {
			t.Errorf("missing %s in %s", p, joined)
		}
	}
	if stats.Requests == 0 || stats.Requests > 20 {
		t.Fatalf("requests=%d", stats.Requests)
	}
}

func TestRunDoesNotFollowOutOfScopeRedirect(t *testing.T) {
	var hits atomic.Int32
	outside := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer outside.Close()
	outsideURL := strings.Replace(outside.URL, "127.0.0.1", "localhost", 1)
	inside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, outsideURL+"/x", http.StatusFound) }))
	defer inside.Close()
	u, _ := url.Parse(inside.URL)
	scope, _ := discovery.NewScope(u.Hostname(), false, true)
	_, _, err := Run(context.Background(), scope, []string{inside.URL}, Config{Depth: 1, Requests: 2, Duration: time.Second, ResponseBytes: 1024, Concurrency: 1, Timeout: time.Second, AllowPrivate: true})
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 0 {
		t.Fatal("followed out-of-scope redirect")
	}
}

func TestHostPacerSpacesConcurrentRequests(t *testing.T) {
	p := newHostPacer(20 * time.Millisecond)
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.wait(context.Background(), "example.com"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if elapsed := time.Since(start); elapsed < 35*time.Millisecond {
		t.Fatalf("requests were not paced: %s", elapsed)
	}
}

func TestMatchingContentRetainsOnlyRequestedTerms(t *testing.T) {
	got := matchingContent("large page with Swagger and SECRET data", []string{"swagger", "openapi"})
	if got != "swagger" {
		t.Fatalf("stored %q", got)
	}
}
