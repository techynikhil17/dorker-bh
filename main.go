package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/techynikhil17/dorker-bh/internal/scan"
)

func main() { os.Exit(run()) }

func defaultConfig() scan.Config {
	return scan.Config{Engines: "duckduckgo,yahoo,wayback,commoncrawl,crawl", Concurrency: 10, Timeout: 10 * time.Second, Retries: 3, Resume: ".dorker-bh.resume", CrawlDepth: 2, CrawlRequests: 1000, CrawlDuration: 10 * time.Minute, CrawlResponseBytes: 5 << 20, CrawlConcurrency: 5, CrawlDelay: 200 * time.Millisecond}
}

func run() int {
	cfg := defaultConfig()
	flag.StringVar(&cfg.List, "l", "", "file containing target domains")
	flag.StringVar(&cfg.List, "list", "", "file containing target domains")
	flag.StringVar(&cfg.Dorks, "d", "", "file containing dork templates")
	flag.StringVar(&cfg.Dorks, "dorks", "", "file containing dork templates")
	flag.StringVar(&cfg.Engines, "e", cfg.Engines, "comma-separated providers: duckduckgo,yahoo,wayback,commoncrawl,crawl,bing,google,yandex")
	flag.StringVar(&cfg.Engines, "engines", cfg.Engines, "comma-separated providers")
	flag.IntVar(&cfg.Concurrency, "c", cfg.Concurrency, "worker count")
	flag.IntVar(&cfg.Concurrency, "concurrency", cfg.Concurrency, "worker count")
	flag.StringVar(&cfg.Proxies, "p", "", "HTTP/HTTPS/SOCKS5 proxy file")
	flag.StringVar(&cfg.Proxies, "proxies", "", "HTTP/HTTPS/SOCKS5 proxy file")
	flag.StringVar(&cfg.Output, "o", "", "output file")
	flag.StringVar(&cfg.Output, "output", "", "output file")
	flag.DurationVar(&cfg.Delay, "delay", 0, "delay between worker requests")
	flag.DurationVar(&cfg.Timeout, "timeout", cfg.Timeout, "request timeout")
	flag.IntVar(&cfg.Retries, "retries", cfg.Retries, "retries on rate limits and network errors")
	flag.BoolVar(&cfg.JSON, "json", false, "write JSON Lines")
	flag.BoolVar(&cfg.Silent, "s", false, "print URLs only, suppress operational logs")
	flag.BoolVar(&cfg.Silent, "silent", false, "print URLs only, suppress operational logs")
	flag.BoolVar(&cfg.Verbose, "v", false, "log queries and errors to stderr")
	flag.BoolVar(&cfg.Verbose, "verbose", false, "log queries and errors to stderr")
	flag.StringVar(&cfg.Resume, "resume", cfg.Resume, "checkpoint file (empty disables checkpointing)")
	flag.BoolVar(&cfg.IncludeSubdomains, "include-subdomains", false, "include subdomains of each target")
	flag.BoolVar(&cfg.AllowPrivate, "allow-private", false, "allow crawling private and local addresses")
	flag.BoolVar(&cfg.IncludeUnverifiedFilters, "include-unverified-filters", false, "include passive URLs whose content filters cannot be verified")
	flag.IntVar(&cfg.CrawlDepth, "crawl-depth", cfg.CrawlDepth, "maximum crawl link depth")
	flag.IntVar(&cfg.CrawlRequests, "crawl-requests", cfg.CrawlRequests, "maximum crawl requests per target")
	flag.DurationVar(&cfg.CrawlDuration, "crawl-duration", cfg.CrawlDuration, "maximum crawl duration per target")
	flag.Int64Var(&cfg.CrawlResponseBytes, "crawl-response-bytes", cfg.CrawlResponseBytes, "maximum decompressed bytes per crawl response")
	flag.IntVar(&cfg.CrawlConcurrency, "crawl-concurrency", cfg.CrawlConcurrency, "crawl workers per target")
	flag.DurationVar(&cfg.CrawlDelay, "crawl-delay", cfg.CrawlDelay, "delay before each crawl request")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "dorker-bh: search engine reconnaissance for authorized targets")
		fmt.Fprintln(flag.CommandLine.Output(), "Usage: dorker-bh -l targets.txt -d dorks.txt [flags]")
		fmt.Fprintln(flag.CommandLine.Output(), "       cat targets.txt | dorker-bh -d dorks.txt [flags]")
		flag.PrintDefaults()
	}
	flag.Parse()
	cfg.AutoResume = true
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "resume" {
			cfg.AutoResume = false
		}
	})
	cfg.Stdout, cfg.Stderr = os.Stdout, os.Stderr
	if cfg.List == "" {
		info, err := os.Stdin.Stat()
		if err != nil {
			fmt.Fprintln(os.Stderr, "dorker-bh:", err)
			return 1
		}
		if info.Mode()&os.ModeCharDevice != 0 {
			fmt.Fprintln(os.Stderr, "dorker-bh: provide -l/--list or pipe targets to stdin")
			return 2
		}
		cfg.Stdin = os.Stdin
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := scan.Run(ctx, cfg); err != nil {
		if errors.Is(err, context.Canceled) {
			return 130
		}
		fmt.Fprintln(os.Stderr, "dorker-bh:", err)
		return 1
	}
	return 0
}
