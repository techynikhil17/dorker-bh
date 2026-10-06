# High-Coverage Recon Design

Date: 2026-10-06

## Purpose

Extend `dorker-bh` from a search-page scraper into a high-coverage URL discovery tool for authorized bug bounty targets. The tool must work without a monthly search quota, emit only observed URLs, enforce target scope before output or crawling, and keep the existing CLI usable.

The design treats live search engines as optional URL sources. Passive indexes and a bounded active crawler provide the main coverage. Dork templates are evaluated locally against the combined URL set so nineteen dorks do not require nineteen upstream searches.

## Success Criteria

- Every emitted URL was observed in a provider response, archive record, sitemap, robots file, redirect, HTML document, JavaScript document, or HTTP response.
- Every emitted and actively requested URL belongs to the configured target scope.
- A run can complete using only free, non-metered sources and authorized target requests.
- Provider failures do not discard results from other providers.
- A target is queried once per broad collector rather than once per dork.
- The tool records how each URL was discovered and whether it was actively verified.
- Existing raw URL output remains available for pipelines.
- The crawler performs only `HEAD` and `GET` requests and never submits forms.

## Non-Goals

- Bypassing CAPTCHAs, bot challenges, provider quotas, or authentication.
- Rotating accounts, browser cookies, or public metasearch instances to evade limits.
- Vulnerability testing, payload injection, parameter fuzzing, form submission, or state-changing requests.
- Crawling hosts outside the declared scope after redirects or links.
- Guaranteeing complete coverage of the public web.

## Provider Model

Providers implement one of two roles.

### Broad collectors

Broad collectors run once per target and return observed URLs for local filtering.

- `wayback`: query the Wayback CDX index with pagination.
- `commoncrawl`: query current Common Crawl indexes with pagination.
- `yahoo`: query Yahoo's mobile search endpoint using a broad `site:<target>` query and decode destination URLs from Yahoo redirect paths.
- `crawl`: perform bounded active discovery on the authorized target.

### Query providers

Query providers may execute each synthesized dork because the provider itself applies query operators.

- `duckduckgo`: retain the HTML provider, try the HTML and Lite endpoints, detect HTTP 202 and challenge pages, and report a provider failure without blocking other providers.
- `bing`: retain public HTML and optional SerpApi compatibility.
- `google`: retain public HTML and existing Custom Search compatibility.
- `yandex`: add an experimental HTML provider. It must recognize Yandex robot challenges and fail explicitly. It is opt-in and excluded from defaults until an independent live test produces correctly scoped results.

Yahoo is deliberately a broad collector. Live testing showed correct site-wide results but unreliable behavior when `inurl:` was added. Local matching provides stable dork semantics over Yahoo's collected URLs.

## Default Providers

The new default provider set is:

```text
duckduckgo,yahoo,wayback,commoncrawl,crawl
```

Google, Bing, Yandex, and SerpApi remain opt-in. A failure in one provider increments provider diagnostics but does not make a run fail when another selected provider produced or successfully checked results. The process exits unsuccessfully only when all selected providers fail for a target, input or output fails, or the run is interrupted.

## Scope Model

Each input line remains a bare hostname.

- Default scope is the exact hostname.
- `--include-subdomains` permits subdomains of the listed hostname.
- A hostname must pass the existing domain validation before jobs are queued.
- Scope comparisons lowercase hostnames, remove a trailing dot, and compare label boundaries.
- URLs with credentials, unsupported schemes, invalid ports, or malformed hosts are rejected.
- Redirects are checked before following. An out-of-scope redirect is recorded as metadata for the source URL but is not requested.
- DNS rebinding protection resolves a hostname before connection and rejects loopback, link-local, private, multicast, unspecified, and documentation-only address ranges unless `--allow-private` is explicitly set for an authorized internal program.
- Proxy use does not weaken hostname scope validation.

## Active Crawler

The crawler begins with both `https://<target>/` and `http://<target>/`; successful canonical redirects within scope determine the preferred origin.

It also seeds:

- `/robots.txt`
- `/sitemap.xml`
- sitemap URLs declared in `robots.txt`
- URLs from passive collectors that are in scope

The crawler extracts URLs from:

- HTML anchors, forms as references only, scripts, stylesheets, images, source sets, canonical links, alternate links, refresh metadata, and Open Graph URL fields
- XML sitemap indexes and URL sets, including gzip-compressed sitemap responses
- `robots.txt` allow, disallow, and sitemap directives
- same-scope HTTP redirects
- JavaScript string literals that contain absolute URLs, root-relative paths, and common endpoint-like relative paths
- source map references

Crawler defaults:

```text
--crawl-depth=2
--crawl-requests=1000
--crawl-duration=10m
--crawl-response-bytes=5242880
--crawl-concurrency=5
--crawl-delay=200ms
```

The global `--timeout` applies per request. The crawler maintains a per-host rate limiter and honors the lower of `--crawl-concurrency` and global concurrency. It sends a descriptive `dorker-bh/<version>` user agent. HTTP compression is enabled with a decompressed response-size limit.

Only HTML, XML, text, and JavaScript-like content types are parsed. Other observed URLs may be output but their bodies are not downloaded unless required to validate status.

## URL Canonicalization and Deduplication

Before deduplication, the tool:

- lowercases the scheme and hostname
- removes default ports
- removes fragments
- normalizes an empty path to `/`
- resolves dot segments
- preserves path case and query parameter order
- removes known search-provider tracking parameters such as Bing `msockid`
- does not remove application parameters whose semantics are unknown

Deduplication keys use the canonical URL. Provenance is merged when multiple sources discover the same URL.

## Local Dork Matching

Templates continue to support `{target}` and `%s`. For broad collectors, the tool compiles each template into local predicates.

Supported local syntax:

- `site:{target}`: scope assertion; consumed during compilation
- `inurl:value`: case-insensitive substring match against the decoded path and raw query
- `ext:value` and `filetype:value`: case-insensitive path extension match
- quoted strings: case-insensitive URL substring match
- bare terms: case-insensitive URL substring match
- unary `-term`, `-inurl:value`, and `-ext:value`: negative predicates

Terms that require page content are evaluated when content was fetched by the crawler. A URL-only passive result that cannot prove a content predicate is labeled `unverified_filter` and omitted by default. `--include-unverified-filters` may include it with that label.

Unsupported query syntax produces a clear template error rather than silently weakening the filter.

Query providers retain their native query behavior, but their results still pass target scope validation.

## Pipeline

For each target:

1. Validate target and create an immutable scope object.
2. Run selected passive broad collectors concurrently with bounded concurrency.
3. Merge and canonicalize observed URLs with provenance.
4. Seed the active crawler with the origin, standard discovery files, and passive URLs.
5. Crawl within the configured budgets and merge newly observed URLs.
6. Run selected query providers for each dork independently.
7. Compile dorks and apply them locally to the broad-collector URL set.
8. Merge matching query-provider and locally filtered results.
9. Write each canonical URL once with combined provenance and validation metadata.
10. Persist provider and crawl checkpoints so an interrupted run can resume.

The implementation uses bounded channels. URL discovery, fetch scheduling, parsing, matching, and writing each have explicit ownership so no goroutine writes shared state without synchronization.

## Output

Raw output remains one URL per line.

JSON Lines adds fields while retaining the existing fields:

```json
{
  "url": "https://example.com/account/login",
  "domain": "example.com",
  "engine": "local",
  "query": "site:example.com inurl:login",
  "timestamp": "2026-10-06T12:00:00Z",
  "sources": ["wayback", "sitemap", "html"],
  "verified": true,
  "status_code": 200,
  "content_type": "text/html",
  "depth": 1,
  "filter_state": "matched"
}
```

For compatibility, `engine` is the query provider name when a query provider found the URL and `local` when only local matching found it. `sources` is authoritative when JSON output is used.

## CLI Changes

Add:

- `--include-subdomains`
- `--allow-private`
- `--crawl-depth`
- `--crawl-requests`
- `--crawl-duration`
- `--crawl-response-bytes`
- `--crawl-concurrency`
- `--crawl-delay`
- `--include-unverified-filters`

Add provider names `yahoo`, `yandex`, and `crawl` to `--engines`.

The existing `-d/--dorks` input remains required because it defines which collected URLs are emitted. A template containing only `site:{target}` emits all collected in-scope URLs.

## Error Handling

- Provider errors are associated with the provider and target.
- Challenge responses are never parsed as result pages.
- A malformed provider result is skipped and counted.
- A broad collector returning zero URLs is a successful empty result when its response is valid.
- If every selected provider fails for a target, that target fails.
- HTTP 429 and transient network errors use bounded exponential backoff.
- Permanent 4xx responses are not retried except 408 and 429.
- Crawl budget exhaustion is a successful bounded completion and appears in verbose diagnostics.
- Output and checkpoint errors stop the run to avoid claiming work was saved when it was not.

## Package Structure

```text
internal/provider/       query and passive provider clients
internal/provider/yahoo.go
internal/provider/yandex.go
internal/discovery/      shared observed-URL and provenance types
internal/crawl/          scope-safe active crawler and extractors
internal/filter/         local dork parser and matcher
internal/scan/           orchestration, checkpointing, and output
```

Provider and crawler packages return observations through interfaces. They do not write output files or own global deduplication.

## Verification Strategy

Unit tests cover:

- Yahoo redirect decoding and result-container selection
- Yandex challenge detection
- DuckDuckGo HTML and Lite result parsing and challenge detection
- exact-host and subdomain scope rules
- redirect scope enforcement
- public/private IP classification
- URL canonicalization and provenance merging
- each supported dork predicate and unsupported syntax
- HTML, JavaScript, robots, and sitemap extraction
- crawl depth, request, size, duration, and concurrency budgets
- resume behavior and thread-safe output

Integration tests use local HTTP servers to verify:

- same-scope crawling across HTML, redirects, JavaScript, robots, and nested sitemaps
- rejection of out-of-scope redirects and links
- deterministic output under concurrency
- partial provider failures with successful output from remaining providers

Live smoke tests use neutral public domains and are opt-in because public engines change and may challenge automation. A provider is enabled by default only after it returns correctly scoped destinations in a live smoke test.

Release verification runs:

```text
go test ./...
go test -race ./...
go vet ./...
```

It then builds the supported release targets and performs a clean `go install` of the new tag.

## Migration and Release

- Existing command lines continue to work.
- The changed default provider set is documented prominently because it introduces authorized target requests through `crawl`.
- Users who require passive-only operation can specify `-e duckduckgo,yahoo,wayback,commoncrawl`.
- A fresh resume file is required because broad-collector and crawl checkpoints use new job identifiers.
- The feature ships in a new minor release because default behavior and output metadata expand materially.

## Known Limitations

- Search engines and archives do not guarantee complete coverage.
- DuckDuckGo can return HTTP 202 challenges.
- Yahoo may inherit Bing indexing and ranking problems.
- Yandex public HTML frequently returns a robot challenge and remains experimental.
- JavaScript extraction is intentionally conservative and will miss dynamically constructed strings.
- Active crawling cannot discover unlinked endpoints without inventing or fuzzing paths, which this design excludes.
