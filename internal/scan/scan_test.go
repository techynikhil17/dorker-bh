package scan

import (
	"bytes"
	"context"
	"errors"
	"io"
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

func TestRunScopesQueriesAndResultsToTarget(t *testing.T) {
	dir := t.TempDir()
	dorks := filepath.Join(dir, "dorks.txt")
	if err := os.WriteFile(dorks, []byte("{target} inurl:admin\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var query string
	cfg := Config{Dorks: dorks, Engines: "bing", Concurrency: 1, Timeout: time.Second,
		Stdin: strings.NewReader("kohls.com\n"), Stdout: &stdout, Stderr: io.Discard,
		Search: func(_ context.Context, _, q string) ([]string, error) {
			query = q
			return []string{
				"https://www.kohls.com/admin",
				"https://kohls.com/login",
				"https://other.example/admin",
				"https://kohls.com.other.example/admin",
			}, nil
		},
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if query != "site:kohls.com inurl:admin" {
		t.Fatalf("unscoped query %q", query)
	}
	if got := stdout.String(); got != "https://www.kohls.com/admin\nhttps://kohls.com/login\n" {
		t.Fatalf("out-of-scope results: %q", got)
	}
}

func TestRunReportsWhenProviderReturnsOnlyOutOfScopeURLs(t *testing.T) {
	dir := t.TempDir()
	dorks := filepath.Join(dir, "dorks.txt")
	if err := os.WriteFile(dorks, []byte("{target}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	cfg := Config{Dorks: dorks, Engines: "bing", Concurrency: 1, Timeout: time.Second,
		Stdin: strings.NewReader("kohls.com\n"), Stdout: &stdout, Stderr: &stderr,
		Search: func(context.Context, string, string) ([]string, error) {
			return []string{"https://www.microsoft.com/help", "https://www.zhihu.com/question"}, nil
		},
	}
	err := Run(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "1 search jobs failed") {
		t.Fatalf("expected the all-out-of-scope provider response to fail, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("out-of-scope URLs were printed: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "none matched requested domain") {
		t.Fatalf("missing actionable scope diagnostic: %q", stderr.String())
	}
}

func TestScopedQueryPreservesSiteOperator(t *testing.T) {
	if got := scopedQuery("site:{target} inurl:admin", "kohls.com"); got != "site:kohls.com inurl:admin" {
		t.Fatalf("got %q", got)
	}
}

func TestScopedQueryDropsStandaloneTargetSearchTerm(t *testing.T) {
	for _, template := range []string{"{target}", "%s", "{target} inurl:admin", "site:{target}"} {
		got := scopedQuery(template, "kohls.com")
		if strings.Contains(got, "site:kohls.com kohls.com") || got == "kohls.com" {
			t.Errorf("target was still searched as a required text term for %q: %q", template, got)
		}
	}
	if got := scopedQuery("{target} inurl:admin", "kohls.com"); got != "site:kohls.com inurl:admin" {
		t.Fatalf("got %q", got)
	}
}

func TestScopedQueryAddsTargetWhenTemplateNamesAnotherSite(t *testing.T) {
	if got := scopedQuery("site:other.example {target}", "kohls.com"); got != "site:kohls.com site:other.example" {
		t.Fatalf("query was not scoped to target: %q", got)
	}
}

func TestBelongsToTargetIsCaseInsensitive(t *testing.T) {
	if !belongsToTarget("https://WWW.KOHLS.COM/admin", "Kohls.com") {
		t.Fatal("rejected in-scope URL with mixed-case host")
	}
}

func TestPassiveProvidersRunOncePerTarget(t *testing.T) {
	dir := t.TempDir()
	dorks := filepath.Join(dir, "dorks.txt")
	if err := os.WriteFile(dorks, []byte("site:{target} admin\nsite:{target} login\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	cfg := Config{Dorks: dorks, Engines: "duckduckgo,wayback,commoncrawl", Concurrency: 2, Timeout: time.Second, Stdin: strings.NewReader("example.com\n"), Stdout: io.Discard, Stderr: io.Discard, Search: func(context.Context, string, string) ([]string, error) { calls.Add(1); return nil, nil }}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 4 {
		t.Fatalf("expected 4 jobs, got %d", got)
	}
}

func TestCancellationUnblocksStdin(t *testing.T) {
	dir := t.TempDir()
	dorks := filepath.Join(dir, "dorks.txt")
	if err := os.WriteFile(dorks, []byte("site:{target}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cfg := Config{Dorks: dorks, Engines: "duckduckgo", Concurrency: 1, Timeout: time.Second, Stdin: reader, Stdout: io.Discard, Stderr: io.Discard, Search: func(context.Context, string, string) ([]string, error) { return nil, nil }}
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stdin did not unblock")
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

func TestAutoResumeClearsCheckpointAfterSuccess(t *testing.T) {
	dir := t.TempDir()
	dorks := filepath.Join(dir, "dorks.txt")
	resume := filepath.Join(dir, ".dorker-bh.resume")
	if err := os.WriteFile(dorks, []byte("site:{target}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	cfg := Config{Dorks: dorks, Engines: "duckduckgo", Concurrency: 1, Timeout: time.Second, Resume: resume, AutoResume: true, Stdin: strings.NewReader("example.com\n"), Stdout: io.Discard, Stderr: io.Discard, Search: func(context.Context, string, string) ([]string, error) { calls.Add(1); return nil, nil }}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(resume); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("checkpoint still exists: %v", err)
	}
	cfg.Stdin = strings.NewReader("example.com\n")
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected fresh second run, got %d calls", calls.Load())
	}
}

func TestResumeDoesNotSkipIntoMissingOutput(t *testing.T) {
	dir := t.TempDir()
	dorks := filepath.Join(dir, "dorks.txt")
	output := filepath.Join(dir, "results.txt")
	resume := filepath.Join(dir, "resume.txt")
	if err := os.WriteFile(dorks, []byte("site:{target}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Dorks: dorks, Engines: "duckduckgo", Concurrency: 1, Timeout: time.Second, Output: output, Resume: resume, Stdin: strings.NewReader("example.com\n"), Stdout: io.Discard, Stderr: io.Discard, Search: func(context.Context, string, string) ([]string, error) { return []string{"https://example.com/"}, nil }}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(output); err != nil {
		t.Fatal(err)
	}
	cfg.Stdin = strings.NewReader("example.com\n")
	if err := Run(context.Background(), cfg); err == nil {
		t.Fatal("expected error for missing resumed output")
	}
}
