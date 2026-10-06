package scan

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Opt-in stress test covers 100,000 unique queries and results. It measures
// peak live heap, which is the controllable portion of process memory.
func TestHundredThousandJobsHeap(t *testing.T) {
	if os.Getenv("DORKER_BH_STRESS") == "" {
		t.Skip("set DORKER_BH_STRESS=1")
	}
	dir := t.TempDir()
	dorks := filepath.Join(dir, "dorks.txt")
	if err := os.WriteFile(dorks, []byte("site:{target}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var input strings.Builder
	for i := 0; i < 100_000; i++ {
		fmt.Fprintf(&input, "t%d.example.com\n", i)
	}
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	peak := base.HeapAlloc
	var peakRSS uint64
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				if m.HeapAlloc > peak {
					peak = m.HeapAlloc
				}
				if rss := linuxRSS(); rss > peakRSS {
					peakRSS = rss
				}
			}
		}
	}()
	cfg := Config{Dorks: dorks, Engines: "duckduckgo", Concurrency: 10, Timeout: time.Second, Resume: filepath.Join(dir, ".dorker-bh.resume"), Stdin: strings.NewReader(input.String()), Stdout: io.Discard, Stderr: io.Discard, Search: func(_ context.Context, _ string, query string) ([]string, error) {
		return []string{"https://" + strings.TrimPrefix(query, "site:") + "/page"}, nil
	}}
	err := Run(context.Background(), cfg)
	close(done)
	<-stopped
	if err != nil {
		t.Fatal(err)
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	if m.HeapAlloc > peak {
		peak = m.HeapAlloc
	}
	t.Logf("peak live heap: %.1f MiB", float64(peak-base.HeapAlloc)/(1<<20))
	if peakRSS > 0 {
		t.Logf("peak process RSS: %.1f MiB", float64(peakRSS)/(1<<20))
	}
	if peak-base.HeapAlloc >= 100<<20 {
		t.Fatalf("peak live heap exceeded 100 MiB")
	}
	if peakRSS >= 100<<20 {
		t.Fatalf("peak process RSS exceeded 100 MiB")
	}
}

func linuxRSS() uint64 {
	if runtime.GOOS != "linux" {
		return 0
	}
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			var kb uint64
			if _, err := fmt.Sscanf(line, "VmRSS: %d kB", &kb); err == nil {
				return kb * 1024
			}
		}
	}
	return 0
}
