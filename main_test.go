package main

import (
	"testing"
	"time"
)

func TestDefaultConfigEnablesBoundedHybridDiscovery(t *testing.T) {
	c := defaultConfig()
	if c.Engines != "duckduckgo,yahoo,wayback,commoncrawl,crawl" {
		t.Fatalf("engines=%q", c.Engines)
	}
	if c.CrawlDepth != 2 || c.CrawlRequests != 1000 || c.CrawlDuration != 10*time.Minute || c.CrawlResponseBytes != 5<<20 || c.CrawlConcurrency != 5 || c.CrawlDelay != 200*time.Millisecond {
		t.Fatalf("bad crawl defaults: %#v", c)
	}
}
