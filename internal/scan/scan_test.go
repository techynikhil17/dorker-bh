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
