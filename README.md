# dorker-bh

`dorker-bh` discovers real URLs for authorized security reconnaissance. It combines public search results, Wayback and Common Crawl indexes, Yahoo site results, and a bounded crawler of the target itself. Dorks are also applied locally to collected URLs, avoiding one upstream query per dork for broad collectors.

Every emitted URL was observed in a provider response or on the authorized target. The tool does not invent paths, submit forms, inject payloads, bypass CAPTCHAs, or evade provider quotas.

## Install

Go 1.26 or later:

```sh
go install github.com/techynikhil17/dorker-bh@latest
export PATH="$(go env GOPATH)/bin:$PATH"
```

Or run `go build -trimpath -o dorker-bh .` in a checkout.

## Quick start

```sh
# Default: DuckDuckGo, Yahoo, archives, and bounded crawling
dorker-bh -l targets.txt -d dorks.txt -o results.txt -v

# No requests to discovered target URLs
dorker-bh -l targets.txt -d dorks.txt \
  -e duckduckgo,yahoo,wayback,commoncrawl -o results.txt

# Include subdomains and save provenance
dorker-bh -l targets.txt -d dorks.txt --include-subdomains --json -o results.jsonl

printf 'example.com\n' | dorker-bh -d dorks.txt --resume=
```

Targets are bare hostnames. Exact-host scope is the default; `--include-subdomains` includes child hosts. Blank lines and lines beginning with `#` are ignored.

```text
site:{target} inurl:login
site:{target} inurl:admin
site:{target} ext:json
site:{target} "swagger"
site:{target} -inurl:logout account
```

Templates support `{target}` and `%s`, plus `site:`, `inurl:`, `ext:`, `filetype:`, quoted strings, bare terms, and unary negative terms. Unsupported operators fail clearly. Quoted content filters require fetched content; passive URLs that cannot prove the filter are omitted unless `--include-unverified-filters` is set.

## Providers

The default is `duckduckgo,yahoo,wayback,commoncrawl,crawl`.

- `duckduckgo`: HTML and Lite pages. Challenges are reported while other providers continue.
- `yahoo`: one broad `site:<target>` mobile search per target, followed by local dork matching.
- `wayback`, `commoncrawl`: passive indexes queried once per target.
- `crawl`: GET-only discovery from origins, robots, sitemaps, HTML, redirects, JavaScript strings, and source maps.
- `yandex`: experimental and opt-in because its public HTML frequently returns a robot challenge.
- `bing`, `google`: opt-in compatibility providers. Bing can use `DORKER_SERPAPI_KEY`; existing Google Custom Search customers can use `DORKER_GOOGLE_API_KEY` and `DORKER_GOOGLE_CSE_ID`.

Provider failures go to stderr and do not discard successful providers' results. A target fails when all its selected providers fail.

## Crawler boundaries

Defaults per target:

```text
--crawl-depth=2
--crawl-requests=1000
--crawl-duration=10m
--crawl-response-bytes=5242880
--crawl-concurrency=5
--crawl-delay=200ms
```

The crawler follows only in-scope HTTP/S URLs, checks redirects before following, rejects credentials, and blocks private, loopback, link-local, multicast, and unspecified IP addresses. `--allow-private` enables explicitly authorized internal targets. It never submits forms.

## Output and resume

Raw output is one canonical URL per line. `--json` adds sources, query, verification status, HTTP status, content type, depth, and filter state. Global deduplication merges repeated discoveries.

The automatic `.dorker-bh.resume` checkpoint is removed after a fully successful run. `--resume FILE` keeps a custom checkpoint; `--resume=` disables checkpointing. Start with a fresh checkpoint when upgrading from releases before this hybrid architecture.

The original flags remain: `-l/--list`, `-d/--dorks`, `-e/--engines`, `-c/--concurrency`, `-p/--proxies`, `-o/--output`, `--delay`, `--timeout`, `--retries`, `--json`, `-s/--silent`, `-v/--verbose`, and `--resume`.

Proxy files accept HTTP, HTTPS, SOCKS5, and SOCKS5H URLs. Search requests rotate proxies round-robin. Use this tool only for systems where you have explicit authorization.
