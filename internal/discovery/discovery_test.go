package discovery

import (
	"net"
	"testing"
)

func TestScopeExactAndSubdomains(t *testing.T) {
	exact, _ := NewScope("Example.COM", false, false)
	withSubs, _ := NewScope("example.com", true, false)
	for raw, want := range map[string][2]bool{
		"https://example.com/a":           {true, true},
		"https://sub.example.com/a":       {false, true},
		"https://example.com.evil.test/a": {false, false},
		"https://user@example.com/a":      {false, false},
		"https://example.com.:443/a":      {true, true},
	} {
		if got := exact.ContainsString(raw); got != want[0] {
			t.Errorf("exact %s=%v", raw, got)
		}
		if got := withSubs.ContainsString(raw); got != want[1] {
			t.Errorf("subs %s=%v", raw, got)
		}
	}
}

func TestDocumentationAndPrivateAddressesAreNotPublic(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "192.0.2.1", "198.51.100.2", "203.0.113.3", "2001:db8::1"} {
		if IsPublicIP(net.ParseIP(raw)) {
			t.Errorf("accepted %s", raw)
		}
	}
	if !IsPublicIP(net.ParseIP("1.1.1.1")) {
		t.Fatal("rejected public IP")
	}
}

func TestCanonicalizeAndMerge(t *testing.T) {
	got, err := Canonicalize("HTTPS://Example.com:443/a/../b?b=2&a=1&msockid=junk#frag")
	if err != nil || got != "https://example.com/b?b=2&a=1" {
		t.Fatalf("got %q %v", got, err)
	}
	m := map[string]*Observation{}
	Merge(m, Observation{URL: got, Target: "example.com", Sources: []string{"wayback"}, Queries: []string{"q1"}})
	Merge(m, Observation{URL: got, Target: "example.com", Sources: []string{"html", "wayback"}, Queries: []string{"q2"}, Verified: true, StatusCode: 200})
	if len(m) != 1 || len(m[got].Sources) != 2 || len(m[got].Queries) != 2 || !m[got].Verified {
		t.Fatalf("bad merge: %#v", m[got])
	}
}
