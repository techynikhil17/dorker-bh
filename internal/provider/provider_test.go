package provider

import (
	"context"
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
