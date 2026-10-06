# High-Coverage Recon Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn `dorker-bh` into a scope-safe URL discovery tool combining free passive indexes, Yahoo and DuckDuckGo results, experimental Yandex results, local dork matching, and a bounded active crawler.

**Architecture:** Shared discovery types own scope, canonicalization, and provenance. Providers and the crawler emit observations; the scanner merges them, applies compiled dorks locally, and writes each canonical URL once. Query providers retain per-dork searches while broad collectors run once per target.

**Tech Stack:** Go 1.26, standard library HTTP/XML/HTML packages, `golang.org/x/net/html`, table-driven tests with `httptest`.

**Spec:** `docs/superpowers/specs/2026-10-06-high-coverage-recon-design.md`

## Global Constraints

- Emit only URLs observed in a provider, archive, page, script, sitemap, robots file, redirect, or HTTP response.
- Enforce exact-host scope by default; `--include-subdomains` expands scope.
- Never follow out-of-scope redirects or crawl disallowed IP ranges unless `--allow-private` is set.
- The crawler uses only GET and HEAD, never submits forms, and obeys all configured budgets.
- Default providers are `duckduckgo,yahoo,wayback,commoncrawl,crawl`.
- Google, Bing, and experimental Yandex remain opt-in.
- Preserve raw URL output, JSON Lines, proxy rotation, retries, graceful cancellation, and resume support.

## Review Focus

- Redirects to a different host must be observed but never requested; Task 4 tests the redirect target server receives zero requests.
- Hostname tricks such as `target.example.evil.test`, credentials, trailing dots, and private resolved addresses must fail scope checks; Task 1 tests each class.
- Quoted content dorks cannot be proven from archive URLs alone; Task 2 tests default omission and the explicit unverified option.
- Challenge pages with HTTP 200 or 202 must produce provider errors rather than empty successful results; Task 3 tests all three live HTML providers.
- Cancellation and exhausted crawl budgets must stop scheduling without leaking workers or losing buffered output; Tasks 4 and 5 test both paths.

---

### Task 1: Scope, canonical URLs, and observations

**Files:**
- Create: `internal/discovery/discovery.go`
- Test: `internal/discovery/discovery_test.go`

**Interfaces:**
- Produces: `NewScope(target string, includeSubdomains, allowPrivate bool) (Scope, error)`, `Scope.Contains(*url.URL) bool`, `Canonicalize(string) (string, error)`, `Observation`, and `Merge(map[string]*Observation, Observation)`.

- [ ] **Step 1: Write failing table tests** for exact/subdomain boundaries, credential rejection, default ports/fragments/dot segments, provider tracking removal, and provenance merging.
- [ ] **Step 2: Run** `/usr/local/go/bin/go test ./internal/discovery`; expect build failure because the package API is absent.
- [ ] **Step 3: Implement the API** with normalized label-boundary comparison and canonical URL keys. `Observation` contains URL, target, source set, query set, verification fields, depth, and filter state.
- [ ] **Step 4: Run** `/usr/local/go/bin/go test ./internal/discovery`; expect PASS.
- [ ] **Step 5: Commit** `git commit -am 'Add scoped URL observation model'` after adding the new files.

### Task 2: Local dork compiler and matcher

**Files:**
- Create: `internal/filter/filter.go`
- Test: `internal/filter/filter_test.go`

**Interfaces:**
- Consumes: `discovery.Observation`.
- Produces: `Compile(template, target string) (Matcher, error)` and `Matcher.Match(discovery.Observation, includeUnverified bool) (matched bool, state string)`.

- [ ] **Step 1: Write failing tests** for `site:`, `inurl:`, `ext:`, `filetype:`, quoted/bare terms, unary negative terms, escaped quotes, unsupported operators, and content-only predicates on unfetched URLs.
- [ ] **Step 2: Run** `/usr/local/go/bin/go test ./internal/filter`; expect build failure for missing compiler.
- [ ] **Step 3: Implement a small lexer and predicate list**. URL predicates inspect decoded path plus raw query; content predicates inspect fetched text. Unknown `name:value` operators return a template error.
- [ ] **Step 4: Run** `/usr/local/go/bin/go test ./internal/filter`; expect PASS.
- [ ] **Step 5: Commit** `git commit -am 'Add local dork filtering'`.

### Task 3: Yahoo, Yandex, and resilient DuckDuckGo providers

**Files:**
- Create: `internal/provider/yahoo.go`
- Create: `internal/provider/yandex.go`
- Modify: `internal/provider/provider.go`
- Modify: `internal/provider/provider_test.go`

**Interfaces:**
- Produces through existing `Client.Search(ctx, engine, query) ([]string, error)`.
- Yahoo accepts a target and internally submits `site:<target>` to `https://search.yahoo.com/mobile/s`.
- Yandex submits the synthesized query and fails on robot-check markup.

- [ ] **Step 1: Add failing fixture tests** proving Yahoo `/RU=<escaped>/RK=` decoding, result-container filtering, Yandex challenge detection, and DuckDuckGo Lite fallback after HTML challenge.
- [ ] **Step 2: Run** `/usr/local/go/bin/go test ./internal/provider`; expect the new cases to fail.
- [ ] **Step 3: Implement parsers and endpoints**. Only organic result containers are accepted; self links, ads, malformed redirects, and unsupported schemes are discarded.
- [ ] **Step 4: Run** `/usr/local/go/bin/go test ./internal/provider`; expect PASS.
- [ ] **Step 5: Commit** `git commit -am 'Add Yahoo and experimental Yandex providers'`.

### Task 4: Bounded scope-safe crawler

**Files:**
- Create: `internal/crawl/crawl.go`
- Create: `internal/crawl/extract.go`
- Test: `internal/crawl/crawl_test.go`

**Interfaces:**
- Consumes: `discovery.Scope`, seed URLs, `Config{Depth, Requests, Duration, ResponseBytes, Concurrency, Delay, Timeout, AllowPrivate}`.
- Produces: `Run(context.Context, Scope, []string, Config) ([]discovery.Observation, Stats, error)`.

- [ ] **Step 1: Write failing `httptest` integration tests** covering HTML attributes, refresh, redirects, JavaScript literals, source maps, robots, gzip sitemap indexes, depth/request/size limits, private-address rejection, cancellation, and no request to out-of-scope redirects.
- [ ] **Step 2: Run** `/usr/local/go/bin/go test ./internal/crawl`; expect build failure.
- [ ] **Step 3: Implement a bounded scheduler** with one canonical seen set, per-host pacing, explicit redirect checks, safe resolver/dialer, decompressed byte limits, and HTML/XML/text/JavaScript extractors.
- [ ] **Step 4: Run** `/usr/local/go/bin/go test ./internal/crawl`; expect PASS and no leaked server requests.
- [ ] **Step 5: Commit** `git commit -am 'Add bounded scope-safe crawler'`.

### Task 5: Orchestration, merged output, and partial failures

**Files:**
- Modify: `internal/scan/scan.go`
- Modify: `internal/scan/scan_test.go`
- Modify: `internal/scan/memory_test.go`

**Interfaces:**
- Consumes: provider search, crawler runner, discovery merger, and compiled matchers.
- Extends `Config` with scope, crawl budgets, and injectable `Crawl` function.
- Extends JSON `Record` with sources, verified, status code, content type, depth, and filter state.

- [ ] **Step 1: Add failing orchestration tests** proving broad collectors run once, query providers run per dork, passive observations are locally filtered, sources merge, exact-host defaults hold, partial provider failures succeed, all-provider failures fail, resume IDs include new mode settings, and cancellation flushes output.
- [ ] **Step 2: Run** `/usr/local/go/bin/go test ./internal/scan`; expect failures under the existing job-only scanner.
- [ ] **Step 3: Refactor `Run`** into per-target collection, merge, crawl, query, local match, and serialized write stages using bounded worker channels.
- [ ] **Step 4: Run** `/usr/local/go/bin/go test ./internal/scan`; expect PASS, then run `/usr/local/go/bin/go test ./...` to catch regressions.
- [ ] **Step 5: Commit** `git commit -am 'Orchestrate high-coverage URL discovery'`.

### Task 6: CLI, documentation, and release verification

**Files:**
- Modify: `main.go`
- Modify: `README.md`
- Modify: `.github/workflows/release.yml` if release validation needs no code change.
- Test: add `main_test.go` only for CLI parsing behavior that cannot be covered through `scan.Config` tests.

**Interfaces:**
- Adds all flags from the design with defaults: depth 2, requests 1000, duration 10m, response bytes 5242880, concurrency 5, delay 200ms.

- [ ] **Step 1: Add a failing CLI/config test** for the default provider list and crawl budget defaults.
- [ ] **Step 2: Run** `/usr/local/go/bin/go test ./...`; expect the defaults test to fail.
- [ ] **Step 3: Wire flags and update README** with passive-only, default hybrid, exact-host/subdomain, JSON provenance, experimental Yandex, authorization, and upgrade examples.
- [ ] **Step 4: Verify** `/usr/local/go/bin/gofmt -w .`, `/usr/local/go/bin/go test ./...`, `/usr/local/go/bin/go test -race ./...`, `/usr/local/go/bin/go vet ./...`, and `CGO_ENABLED=0 /usr/local/go/bin/go build -trimpath -o /tmp/dorker-bh .`.
- [ ] **Step 5: Run neutral live smoke checks** against `go.dev` for Yahoo, DuckDuckGo, archives, and a tightly bounded crawl; record upstream challenges as provider diagnostics, never as fabricated success.
- [ ] **Step 6: Review the full diff against every spec section**, fix any Critical or Important finding, rerun verification, commit, push `main`, tag the next minor release, and verify the GitHub release plus `go install github.com/techynikhil17/dorker-bh@latest` from a clean temporary module cache.
