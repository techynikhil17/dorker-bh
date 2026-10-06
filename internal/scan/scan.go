package scan

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/techynikhil17/dorker-bh/internal/crawl"
	"github.com/techynikhil17/dorker-bh/internal/discovery"
	dorkfilter "github.com/techynikhil17/dorker-bh/internal/filter"
	"github.com/techynikhil17/dorker-bh/internal/provider"
)

var targetPattern = regexp.MustCompile(`(?i)^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

func ValidTarget(s string) bool { return len(s) <= 253 && targetPattern.MatchString(s) }
func Synthesize(template, target string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(strings.ReplaceAll(template, "{target}", target), "%s", target)), " ")
}

func scopedQuery(template, target string) string {
	fields := strings.Fields(Synthesize(template, target))
	queryFields := fields[:0]
	hasTargetSite := false
	for _, field := range fields {
		if strings.EqualFold(field, target) {
			continue
		}
		if strings.EqualFold(field, "site:"+target) {
			hasTargetSite = true
		}
		queryFields = append(queryFields, field)
	}
	query := strings.Join(queryFields, " ")
	if hasTargetSite {
		return query
	}
	if query == "" {
		return "site:" + target
	}
	return "site:" + target + " " + query
}

func belongsToTarget(rawURL, target string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	target = strings.ToLower(target)
	return host == target || strings.HasSuffix(host, "."+target)
}

func passive(engine string) bool {
	return engine == "wayback" || engine == "commoncrawl" || engine == "yahoo"
}

type Job struct{ Target, Engine, Query, ID string }
type Record struct {
	URL         string   `json:"url"`
	Domain      string   `json:"domain"`
	Engine      string   `json:"engine"`
	Query       string   `json:"query"`
	Timestamp   string   `json:"timestamp"`
	Sources     []string `json:"sources,omitempty"`
	Verified    bool     `json:"verified"`
	StatusCode  int      `json:"status_code,omitempty"`
	ContentType string   `json:"content_type,omitempty"`
	Depth       int      `json:"depth,omitempty"`
	FilterState string   `json:"filter_state,omitempty"`
}
type Config struct {
	List, Dorks, Engines, Proxies, Output, Resume             string
	Concurrency, Retries                                      int
	Delay, Timeout                                            time.Duration
	JSON, Silent, Verbose                                     bool
	IncludeSubdomains, AllowPrivate, IncludeUnverifiedFilters bool
	CrawlDepth, CrawlRequests, CrawlConcurrency               int
	CrawlDuration, CrawlDelay                                 time.Duration
	CrawlResponseBytes                                        int64
	AutoResume                                                bool
	Stdin                                                     io.Reader
	Stdout, Stderr                                            io.Writer
	Search                                                    func(context.Context, string, string) ([]string, error)
	Crawl                                                     func(context.Context, discovery.Scope, []string, crawl.Config) ([]discovery.Observation, crawl.Stats, error)
}

type state struct {
	mu                    sync.Mutex
	seen                  map[string]struct{}
	completed             map[string]struct{}
	output                *bufio.Writer
	file                  *os.File
	checkpoint            *os.File
	checkpointWriter      *bufio.Writer
	json, silent, verbose bool
	stdout, stderr        io.Writer
	failures              int
	count                 int
}

func readLines(r io.Reader, fn func(string) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if err := fn(line); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func linesFromFile(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var lines []string
	err = readLines(file, func(line string) error { lines = append(lines, line); return nil })
	return lines, err
}

func (s *state) add(job Job, rawURL string) error {
	if !belongsToTarget(rawURL, job.Target) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.seen[rawURL]; exists {
		return nil
	}
	record := Record{URL: rawURL, Domain: job.Target, Engine: job.Engine, Query: job.Query, Timestamp: time.Now().UTC().Format(time.RFC3339Nano)}
	var line string
	if s.json {
		data, err := json.Marshal(record)
		if err != nil {
			return err
		}
		line = string(data)
	} else {
		line = rawURL
	}
	if s.file != nil {
		if _, err := s.output.WriteString(line + "\n"); err != nil {
			return err
		}
		// A completed checkpoint must never outrun its output.
		if err := s.output.Flush(); err != nil {
			return err
		}
	}
	stdoutLine := line
	if s.silent {
		stdoutLine = rawURL
	}
	if _, err := fmt.Fprintln(s.stdout, stdoutLine); err != nil {
		return err
	}
	s.seen[rawURL] = struct{}{}
	s.count++
	return nil
}

func (s *state) addObservation(obs discovery.Observation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.seen[obs.URL]; exists {
		return nil
	}
	engine := "local"
	if len(obs.Sources) == 1 {
		engine = obs.Sources[0]
	}
	query := ""
	if len(obs.Queries) > 0 {
		query = obs.Queries[0]
	}
	record := Record{URL: obs.URL, Domain: obs.Target, Engine: engine, Query: query, Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Sources: obs.Sources, Verified: obs.Verified, StatusCode: obs.StatusCode, ContentType: obs.ContentType, Depth: obs.Depth, FilterState: obs.FilterState}
	line := obs.URL
	if s.json {
		data, err := json.Marshal(record)
		if err != nil {
			return err
		}
		line = string(data)
	}
	if s.file != nil {
		if _, err := s.output.WriteString(line + "\n"); err != nil {
			return err
		}
		if err := s.output.Flush(); err != nil {
			return err
		}
	}
	stdoutLine := line
	if s.silent {
		stdoutLine = obs.URL
	}
	if _, err := fmt.Fprintln(s.stdout, stdoutLine); err != nil {
		return err
	}
	s.seen[obs.URL] = struct{}{}
	s.count++
	return nil
}

func (s *state) finish(job Job) error {
	if s.checkpoint == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.completed[job.ID]; exists {
		return nil
	}
	if _, err := fmt.Fprintln(s.checkpointWriter, job.ID); err != nil {
		return err
	}
	s.completed[job.ID] = struct{}{}
	return nil
}

func (s *state) isCompleted(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, done := s.completed[id]
	return done
}

func (s *state) failed(job Job, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures++
	if !s.silent {
		fmt.Fprintf(s.stderr, "dorker-bh: %s %s: %v\n", job.Engine, job.Target, err)
	}
}

func openState(cfg Config) (*state, error) {
	s := &state{seen: make(map[string]struct{}), completed: make(map[string]struct{}), json: cfg.JSON, silent: cfg.Silent, verbose: cfg.Verbose, stdout: cfg.Stdout, stderr: cfg.Stderr}
	resumeExisting := false
	if cfg.Resume != "" {
		if cfg.Resume == cfg.Output {
			return nil, errors.New("resume and output paths must differ")
		}
		_, err := os.Stat(cfg.Resume)
		if err == nil {
			resumeExisting = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	if cfg.Output != "" {
		if resumeExisting {
			if _, err := os.Stat(cfg.Output); err != nil {
				return nil, fmt.Errorf("resuming requires existing output file %q: %w", cfg.Output, err)
			}
		}
		// Resume appends; fresh scans truncate to avoid mixing two result sets.
		flag := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
		if resumeExisting {
			flag = os.O_CREATE | os.O_WRONLY | os.O_APPEND
		}
		f, err := os.OpenFile(cfg.Output, flag, 0644)
		if err != nil {
			return nil, err
		}
		s.file = f
		s.output = bufio.NewWriterSize(f, 64*1024)
		if resumeExisting {
			old, err := os.Open(cfg.Output)
			if err == nil {
				readErr := readLines(old, func(line string) error {
					if cfg.JSON {
						var rec Record
						if json.Unmarshal([]byte(line), &rec) == nil {
							s.seen[rec.URL] = struct{}{}
						}
					} else {
						s.seen[line] = struct{}{}
					}
					return nil
				})
				old.Close()
				if readErr != nil {
					s.close()
					return nil, readErr
				}
			}
		}
	}
	if cfg.Resume != "" {
		old, err := os.Open(cfg.Resume)
		if err == nil {
			err = readLines(old, func(line string) error { s.completed[line] = struct{}{}; return nil })
			old.Close()
			if err != nil {
				s.close()
				return nil, err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			s.close()
			return nil, err
		}
		f, err := os.OpenFile(cfg.Resume, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			s.close()
			return nil, err
		}
		s.checkpoint = f
		s.checkpointWriter = bufio.NewWriterSize(f, 64*1024)
	}
	return s, nil
}

func (s *state) close() error {
	var errs []error
	if s.output != nil {
		errs = append(errs, s.output.Flush())
	}
	if s.file != nil {
		errs = append(errs, s.file.Sync())
		errs = append(errs, s.file.Close())
	}
	if s.checkpoint != nil {
		errs = append(errs, s.checkpointWriter.Flush())
		errs = append(errs, s.checkpoint.Sync())
		errs = append(errs, s.checkpoint.Close())
	}
	return errors.Join(errs...)
}

func jobID(engine, query string) string {
	sum := sha256.Sum256([]byte(engine + "\x00" + query))
	return hex.EncodeToString(sum[:])
}

// Run streams targets into a bounded queue. Cancelling ctx stops production;
// workers already processing jobs finish their timeout-bounded requests.
func Run(ctx context.Context, cfg Config) (runErr error) {
	if cfg.Concurrency < 1 || cfg.Retries < 0 || cfg.Timeout <= 0 || cfg.Delay < 0 {
		return errors.New("invalid concurrency, retries, timeout or delay")
	}
	if cfg.Dorks == "" {
		return errors.New("-d/--dorks is required")
	}
	templates, err := linesFromFile(cfg.Dorks)
	if err != nil {
		return fmt.Errorf("read dorks: %w", err)
	}
	if len(templates) == 0 {
		return errors.New("dork file is empty")
	}
	engines := strings.Split(cfg.Engines, ",")
	for i, engine := range engines {
		engine = strings.ToLower(strings.TrimSpace(engine))
		engines[i] = engine
		if engine != "duckduckgo" && engine != "bing" && engine != "google" && engine != "yandex" && engine != "crawl" && !passive(engine) {
			return fmt.Errorf("unsupported engine %q", engine)
		}
		if engine == "crawl" && (cfg.CrawlDepth < 0 || cfg.CrawlRequests < 1 || cfg.CrawlDuration <= 0 || cfg.CrawlResponseBytes < 1 || cfg.CrawlConcurrency < 1 || cfg.CrawlDelay < 0) {
			return errors.New("invalid crawl depth, requests, duration, response bytes, concurrency or delay")
		}
	}
	var proxies []string
	if cfg.Proxies != "" {
		proxies, err = linesFromFile(cfg.Proxies)
		if err != nil {
			return fmt.Errorf("read proxies: %w", err)
		}
		if len(proxies) == 0 {
			return errors.New("proxy file is empty")
		}
	}
	client, err := provider.NewClient(cfg.Timeout, cfg.Retries, proxies)
	if err != nil {
		return err
	}
	search := cfg.Search
	if search == nil {
		search = client.Search
	}
	crawlRun := cfg.Crawl
	if crawlRun == nil {
		crawlRun = crawl.Run
	}
	if cfg.Stdout == nil {
		cfg.Stdout = os.Stdout
	}
	if cfg.Stderr == nil {
		cfg.Stderr = os.Stderr
	}
	s, err := openState(cfg)
	if err != nil {
		return err
	}
	defer func() {
		runErr = errors.Join(runErr, s.close())
		if runErr == nil && cfg.AutoResume && cfg.Resume != "" {
			runErr = os.Remove(cfg.Resume)
		}
	}()

	var input io.Reader = cfg.Stdin
	if cfg.List != "" {
		f, err := os.Open(cfg.List)
		if err != nil {
			return fmt.Errorf("read targets: %w", err)
		}
		defer f.Close()
		input = f
	}
	if input == nil {
		return errors.New("provide -l/--list or pipe targets to stdin")
	}
	stopInput := context.AfterFunc(ctx, func() {
		if closer, ok := input.(io.Closer); ok {
			closer.Close()
		}
	})
	defer stopInput()
	var produced int
	readErr := readLines(input, func(target string) error {
		if !ValidTarget(target) {
			return fmt.Errorf("invalid target %q", target)
		}
		scope, err := discovery.NewScope(target, cfg.IncludeSubdomains, cfg.AllowPrivate)
		if err != nil {
			return err
		}
		matchers := make([]dorkfilter.Matcher, len(templates))
		var contentTerms []string
		for i, tpl := range templates {
			matchers[i], err = dorkfilter.Compile(tpl, target)
			if err != nil {
				return fmt.Errorf("dork %q: %w", tpl, err)
			}
			contentTerms = append(contentTerms, matchers[i].ContentTerms()...)
		}
		observed := map[string]*discovery.Observation{}
		var successfulJobs []Job
		successes, attempts := 0, 0
		var jobs []Job
		for _, engine := range engines {
			if engine == "crawl" {
				continue
			}
			if passive(engine) {
				query := target
				if engine == "yahoo" {
					query = "site:" + target
				}
				jobs = append(jobs, Job{Target: target, Engine: engine, Query: query, ID: jobID(engine, query)})
				continue
			}
			for _, tpl := range templates {
				q := scopedQuery(tpl, target)
				jobs = append(jobs, Job{Target: target, Engine: engine, Query: q, ID: jobID(engine, q)})
			}
		}
		var mu sync.Mutex
		sem := make(chan struct{}, cfg.Concurrency)
		var wg sync.WaitGroup
		for _, job := range jobs {
			if s.isCompleted(job.ID) {
				continue
			}
			produced++
			attempts++
			wg.Add(1)
			go func(job Job) {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					return
				}
				defer func() { <-sem }()
				if cfg.Verbose && !cfg.Silent {
					fmt.Fprintf(cfg.Stderr, "dorker-bh: querying %s %q\n", job.Engine, job.Query)
				}
				if cfg.Delay > 0 {
					select {
					case <-time.After(cfg.Delay):
					case <-ctx.Done():
						return
					}
				}
				providerCtx, cancelProvider := context.WithTimeout(ctx, cfg.Timeout)
				results, e := search(providerCtx, job.Engine, job.Query)
				cancelProvider()
				inScope, outScope := 0, 0
				mu.Lock()
				defer mu.Unlock()
				if e == nil {
					for _, raw := range results {
						canon, ce := discovery.Canonicalize(raw)
						if ce != nil || !scope.ContainsString(canon) {
							outScope++
							continue
						}
						inScope++
						obs := discovery.Observation{URL: canon, Target: target, Sources: []string{job.Engine}}
						discovery.Merge(observed, obs)
					}
					if len(results) > 0 && inScope == 0 {
						e = fmt.Errorf("provider returned %d URLs but none matched requested domain %s; %s may have ignored the target scope", outScope, target, job.Engine)
					}
				}
				if e != nil {
					s.failed(job, e)
				} else {
					successes++
					successfulJobs = append(successfulJobs, job)
				}
			}(job)
		}
		wg.Wait()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		for _, engine := range engines {
			if engine != "crawl" {
				continue
			}
			crawlJob := Job{Target: target, Engine: "crawl", Query: target, ID: jobID("crawl", fmt.Sprintf("%s\x00%d\x00%d\x00%s", target, cfg.CrawlDepth, cfg.CrawlRequests, cfg.CrawlDuration))}
			if s.isCompleted(crawlJob.ID) {
				continue
			}
			attempts++
			produced++
			seeds := []string{"https://" + target + "/", "http://" + target + "/"}
			for raw := range observed {
				seeds = append(seeds, raw)
			}
			obs, _, e := crawlRun(ctx, scope, seeds, crawl.Config{Depth: cfg.CrawlDepth, Requests: cfg.CrawlRequests, Duration: cfg.CrawlDuration, ResponseBytes: cfg.CrawlResponseBytes, Concurrency: min(cfg.CrawlConcurrency, cfg.Concurrency), Delay: cfg.CrawlDelay, Timeout: cfg.Timeout, AllowPrivate: cfg.AllowPrivate, ContentTerms: contentTerms})
			if e != nil {
				s.failed(crawlJob, e)
			} else {
				successes++
				successfulJobs = append(successfulJobs, crawlJob)
				for _, o := range obs {
					discovery.Merge(observed, o)
				}
			}
		}
		if attempts > 0 && successes == 0 {
			return fmt.Errorf("%d search jobs failed", attempts)
		}
		keys := make([]string, 0, len(observed))
		for raw := range observed {
			keys = append(keys, raw)
		}
		sort.Strings(keys)
		for _, raw := range keys {
			o := *observed[raw]
			matched := false
			for i, m := range matchers {
				ok, state := m.Match(o, cfg.IncludeUnverifiedFilters)
				if ok {
					o.Queries = append(o.Queries, Synthesize(templates[i], target))
					o.FilterState = state
					matched = true
				} else if o.FilterState == "" && state == "unverified_filter" {
					o.FilterState = state
				}
			}
			if matched {
				if o.FilterState == "" {
					o.FilterState = "matched"
				}
				if err := s.addObservation(o); err != nil {
					return err
				}
			}
		}
		for _, job := range successfulJobs {
			if err := s.finish(job); err != nil {
				return err
			}
		}
		return nil
	})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if readErr != nil && !errors.Is(readErr, context.Canceled) {
		return readErr
	}
	if produced == 0 && !errors.Is(readErr, context.Canceled) && len(s.completed) == 0 {
		return errors.New("no target domains found")
	}
	if errors.Is(readErr, context.Canceled) {
		return context.Canceled
	}
	return nil
}
