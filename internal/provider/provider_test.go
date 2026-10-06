package provider

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseResults(t *testing.T) {
	cases := []struct{ engine, body, want string }{
		{"duckduckgo", `<a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fadmin">Admin</a>`, "https://example.com/admin"},
		{"bing", `<li class="b_algo"><h2><a href="https://example.com/login">Login</a></h2></li>`, "https://example.com/login"},
		{"google", `<div class="MjjYud"><a href="/url?q=https%3A%2F%2Fexample.com%2Fsecret&sa=U"><h3>Secret</h3></a></div>`, "https://example.com/secret"},
	}
	for _, tc := range cases {
		t.Run(tc.engine, func(t *testing.T) {
			got, err := ParseResults(tc.engine, strings.NewReader(tc.body))
			if err != nil || len(got) != 1 || got[0] != tc.want {
				t.Fatalf("got %v, %v", got, err)
			}
		})
	}
}

func TestSearchRetriesRateLimit(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`<a class="result__a" href="https://example.com/ok">OK</a>`))
	}))
	defer server.Close()
	client, err := NewClient(2*time.Second, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.searchAt(context.Background(), "duckduckgo", server.URL)
	if err != nil || attempts != 2 || len(got) != 1 || got[0] != "https://example.com/ok" {
		t.Fatalf("attempts=%d results=%v err=%v", attempts, got, err)
	}
}

func TestDuckDuckGoCompatibilityFallback(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if r.Header.Get("User-Agent") != "Mozilla/5.0" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Write([]byte(`<a class="result__a" href="https://example.com/ok">OK</a>`))
	}))
	defer server.Close()
	client, err := NewClient(time.Second, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	results, err := client.searchAt(context.Background(), "duckduckgo", server.URL)
	if err != nil || attempts != 2 || len(results) != 1 {
		t.Fatalf("attempts=%d results=%v err=%v", attempts, results, err)
	}
}

func TestDuckDuckGoChallengeReportsBlocked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte("Unfortunately, bots use DuckDuckGo too."))
	}))
	defer server.Close()
	client, err := NewClient(time.Second, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.searchAt(context.Background(), "duckduckgo", server.URL)
	if err == nil || !strings.Contains(err.Error(), "bot challenge") {
		t.Fatalf("expected clear challenge error, got %v", err)
	}
}

func TestProxyRotation(t *testing.T) {
	client, err := NewClient(time.Second, 0, []string{"http://127.0.0.1:8080", "socks5://127.0.0.1:1080"})
	if err != nil {
		t.Fatal(err)
	}
	if a, b, c := client.proxy().Scheme, client.proxy().Scheme, client.proxy().Scheme; a != "http" || b != "socks5" || c != "http" {
		t.Fatalf("rotation: %s %s %s", a, b, c)
	}
}

func TestParseResultsRejectsNavigationAndAds(t *testing.T) {
	body := `<a class="result__a" href="https://html.duckduckgo.com/about">Self</a><div class="result--ad"><a class="result__a" href="https://ads.example/">Ad</a></div>`
	got, err := ParseResults("duckduckgo", strings.NewReader(body))
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestBingTrackingLinkUnwrapped(t *testing.T) {
	encoded := base64.RawURLEncoding.EncodeToString([]byte("https://example.com/admin"))
	body := `<li class="b_algo"><h2><a href="https://www.bing.com/ck/a?u=a1` + encoded + `">Admin</a></h2></li>`
	got, err := ParseResults("bing", strings.NewReader(body))
	if err != nil || len(got) != 1 || got[0] != "https://example.com/admin" {
		t.Fatalf("got=%v err=%v", got, err)
	}
}

func TestGoogleJavaScriptInterstitialIsError(t *testing.T) {
	_, err := ParseResults("google", strings.NewReader(`<html><body><meta content="0;url=/httpservice/retry/enablejs" http-equiv="refresh"></body></html>`))
	if err == nil {
		t.Fatal("expected blocked-page error")
	}
}

func TestParseGoogleAPI(t *testing.T) {
	got, err := parseGoogleAPI(strings.NewReader(`{"items":[{"link":"https://example.com/admin"},{"link":"javascript:alert(1)"}]}`))
	if err != nil || len(got) != 1 || got[0] != "https://example.com/admin" {
		t.Fatalf("got=%v err=%v", got, err)
	}
}

func TestDestinationRejectsRegionalGoogleNavigation(t *testing.T) {
	if got := destination("google", "https://www.google.co.in/preferences"); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := destination("google", "https://google.evil.com/page"); got == "" {
		t.Fatal("unrelated domain was rejected")
	}
}

func TestParseWayback(t *testing.T) {
	body := `[["original","statuscode"],["https://example.com/admin","200"],["https://example.com/error","404"],["https://other.test/no","200"]]`
	got, err := parseWayback(strings.NewReader(body), "example.com")
	if err != nil || len(got) != 1 || got[0] != "https://example.com/admin" {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestParseCommonCrawl(t *testing.T) {
	body := "{\"url\":\"https://example.com/admin\",\"status\":\"200\"}\n{\"url\":\"https://example.com/error\",\"status\":\"404\"}\n"
	got, err := parseCommonCrawl(strings.NewReader(body), "example.com")
	if err != nil || len(got) != 1 || got[0] != "https://example.com/admin" {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestParseResultsReportsChallenge(t *testing.T) {
	_, err := ParseResults("duckduckgo", strings.NewReader(`<html><body>Please complete the CAPTCHA to continue</body></html>`))
	if err == nil {
		t.Fatal("expected challenge error")
	}
}
