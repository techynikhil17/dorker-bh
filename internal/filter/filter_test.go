package filter

import (
	"github.com/techynikhil17/dorker-bh/internal/discovery"
	"testing"
)

func TestURLPredicates(t *testing.T) {
	cases := []struct {
		q, raw string
		want   bool
	}{
		{"site:{target} inurl:admin", "https://example.com/x/admin?id=1", true},
		{"site:{target} inurl:admin", "https://example.com/login", false},
		{"site:{target} ext:json", "https://example.com/openapi.json", true},
		{"site:{target} filetype:xml", "https://example.com/site.XML?q=1", true},
		{"site:{target} -inurl:logout account", "https://example.com/account", true},
		{"site:{target} -inurl:logout account", "https://example.com/account/logout", false},
	}
	for _, tc := range cases {
		m, err := Compile(tc.q, "example.com")
		if err != nil {
			t.Fatal(err)
		}
		got, _ := m.Match(discovery.Observation{URL: tc.raw}, false)
		if got != tc.want {
			t.Errorf("%q %s=%v", tc.q, tc.raw, got)
		}
	}
}

func TestContentAndUnsupported(t *testing.T) {
	m, err := Compile(`site:{target} "swagger ui"`, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if ok, state := m.Match(discovery.Observation{URL: "https://example.com/docs"}, false); ok || state != "unverified_filter" {
		t.Fatalf("%v %s", ok, state)
	}
	if ok, _ := m.Match(discovery.Observation{URL: "https://example.com/docs", Content: "Swagger UI", Verified: true}, false); !ok {
		t.Fatal("content did not match")
	}
	if _, err := Compile("site:{target} intitle:admin", "example.com"); err == nil {
		t.Fatal("unsupported operator accepted")
	}
}
