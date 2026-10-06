package scan

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSynthesize(t *testing.T) {
	got := Synthesize("site:{target} inurl:%s   admin", "example.com")
	if got != "site:example.com inurl:example.com admin" {
		t.Fatalf("got %q", got)
	}
}

func TestValidTarget(t *testing.T) {
	for _, value := range []string{"example.com", "sub.example.com"} {
		if !ValidTarget(value) {
			t.Fatalf("rejected %q", value)
		}
	}
	for _, value := range []string{"", "https://example.com", "foo bar", "-foo.com"} {
		if ValidTarget(value) {
			t.Fatalf("accepted %q", value)
		}
	}
}

func TestRunDeduplicatesAndResumes(t *testing.T) {
	dir := t.TempDir()
	dorks := filepath.Join(dir, "dorks.txt")
	output := filepath.Join(dir, "results.txt")
	resume := filepath.Join(dir, "state.txt")
	if err := os.WriteFile(dorks, []byte("site:{target} admin\nsite:{target} login\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var calls atomic.Int32
	cfg := Config{Dorks: dorks, Engines: "duckduckgo", Concurrency: 2, Timeout: time.Second, Output: output, Resume: resume, Stdin: strings.NewReader("example.com\n"), Stdout: &stdout, Stderr: &bytes.Buffer{}, Search: func(context.Context, string, string) ([]string, error) {
		calls.Add(1)
		return []string{"https://example.com/admin"}, nil
	}}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || stdout.String() != "https://example.com/admin\n" {
		t.Fatalf("calls=%d stdout=%q", calls.Load(), stdout.String())
	}
	stdout.Reset()
	cfg.Stdin = strings.NewReader("example.com\n")
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || stdout.Len() != 0 || string(data) != "https://example.com/admin\n" {
		t.Fatalf("calls=%d stdout=%q output=%q", calls.Load(), stdout.String(), data)
	}
}
