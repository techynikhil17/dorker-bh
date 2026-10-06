package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBingSerpAPIUsesStructuredOrganicResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("engine"); got != "bing" {
			t.Errorf("engine = %q", got)
		}
		if got := r.URL.Query().Get("q"); got != "site:go.dev documentation" {
			t.Errorf("query = %q", got)
		}
		if got := r.URL.Query().Get("api_key"); got != "test-secret" {
			t.Errorf("key = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"search_metadata":{"status":"Success"},"ads":[{"link":"https://ads.example/"}],"organic_results":[{"link":"https://go.dev/doc/?msockid=tracking"},{"link":"https://go.dev/doc/"},{"link":"https://www.bing.com/search?q=go"}]}`)
	}))
	defer server.Close()
	c, err := NewClient(time.Second, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.serpAPIKey = "test-secret"
	c.serpAPIBaseURL = server.URL
	got, err := c.Search(context.Background(), "bing", "site:go.dev documentation")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "https://go.dev/doc/" {
		t.Fatalf("organic URLs = %#v", got)
	}
}

func TestBingSerpAPIRejectsProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"search_metadata":{"status":"Error"},"error":"monthly limit reached for test-secret"}`)
	}))
	defer server.Close()
	c, err := NewClient(time.Second, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.serpAPIKey = "test-secret"
	c.serpAPIBaseURL = server.URL
	_, err = c.Search(context.Background(), "bing", "site:go.dev documentation")
	if err == nil || !strings.Contains(err.Error(), "monthly limit reached") {
		t.Fatalf("expected provider error, got %v", err)
	}
	if strings.Contains(err.Error(), "test-secret") {
		t.Fatalf("provider error leaked key: %v", err)
	}
}

func TestBingSerpAPITransportErrorDoesNotRevealKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	}))
	defer server.Close()
	c, err := NewClient(time.Second, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.serpAPIKey = "test-secret"
	c.serpAPIBaseURL = server.URL
	_, err = c.Search(context.Background(), "bing", "site:go.dev documentation")
	if err == nil || strings.Contains(err.Error(), "test-secret") {
		t.Fatalf("request error leaked key: %v", err)
	}
}
