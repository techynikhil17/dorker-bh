# dorker-bh

`dorker-bh` queries public DuckDuckGo, Bing, and Google results and passive Wayback and Common Crawl indexes. Use it only on targets you are authorized to audit. It prints discovered destination URLs, one per line, so results can be piped to other tools.

## Build

Go 1.26 or later:

```sh
go build -trimpath -o dorker-bh .
```

The module has no server-side services. Release builds disable CGO, producing a standalone binary. You can also install it with `go install github.com/techynikhil17/dorker-bh@latest`.

## Usage

```sh
./dorker-bh -l targets.txt -d dorks.txt -e duckduckgo,bing -c 10 -o results.txt
cat targets.txt | ./dorker-bh -d dorks.txt -s | httpx -silent
./dorker-bh -l targets.txt -d dorks.txt -e duckduckgo,bing,google,wayback,commoncrawl --json -o results.jsonl
```

`targets.txt` contains one bare domain or subdomain per line. `dorks.txt` contains one query template per line. Blank lines and lines beginning with `#` are skipped. Each template should contain `{target}` or `%s`, for example:

```text
site:{target} inurl:admin
site:%s ext:env
```

The `{target}` or `%s` placeholder is treated as the domain scope, not as a required text term. If a template does not already contain `site:<target>`, the tool adds it to the search query. Regardless of what an engine returns, only URLs on the requested domain or its subdomains are written. Search engines can ignore operators or return unrelated results; if every result is outside the target, the job is reported as failed with that explanation.

Flags: `-l/--list`, `-d/--dorks`, `-e/--engines` (default `duckduckgo,bing`), `-c/--concurrency` (default `10`), `-p/--proxies`, `-o/--output`, `--delay`, `--timeout` (default `10s`), `--retries` (default `3`), `--json`, `-s/--silent`, `-v/--verbose`, and `--resume`.

Proxy files accept `http://`, `https://`, and `socks5://` URLs, one per line. A proxy is selected for every request, including retries. `--silent` always prints raw URLs to stdout, even when `--json` writes JSON Lines to the output file. Operational messages go to stderr.

The default checkpoint is `.dorker-bh.resume`. On interruption or provider errors, successful jobs remain recorded; the next invocation resumes them. A fully successful run removes this default checkpoint so a later scan starts fresh. `--resume FILE` uses a persistent custom checkpoint. `--resume=` disables checkpointing. When resuming to a file, use the same `-o` and `--json` settings.

Wayback and Common Crawl are passive providers selected with `-e`. They query each target once and return indexed URLs; search dork syntax does not apply to archive indexes. Common Crawl requests are serialized and paced to respect its index service. Archive results are capped at 1,000 URLs per target and provider.

Google HTML may serve a JavaScript interstitial. Existing Custom Search JSON API customers can set `DORKER_GOOGLE_API_KEY` and `DORKER_GOOGLE_CSE_ID` to use Google's API instead. The API is closed to new customers; see [Google's current API notice](https://developers.google.com/custom-search/v1/overview).

### Bing results

The no-key Bing HTML mode uses the public search page. Bing sometimes ignores `site:` and other query operators, returning unrelated links even for a correctly encoded query. The tool rejects links outside the requested domain and reports a failed job when all returned links are out of scope.

For a structured Bing results path, set `DORKER_SERPAPI_KEY` to a [SerpApi key](https://serpapi.com/bing-search-api). With that variable set, `-e bing` uses SerpApi's Bing engine and only its organic result links. The [free plan currently lists 250 searches per month and 50 per hour](https://serpapi.com/pricing); an account and key are required, and limits can change. Each target/dork pair uses one search. The target-domain filter still applies because search providers can return irrelevant links.

In Bash or WSL, enter the key without putting it in shell history:

```sh
read -r -s -p 'SerpApi key: ' DORKER_SERPAPI_KEY; echo
export DORKER_SERPAPI_KEY
printf 'go.dev\n' | dorker-bh -d <(printf 'site:{target} documentation\n') -e bing -c 1 --resume= -v
```

Unset the variable with `unset DORKER_SERPAPI_KEY` to use the public Bing HTML mode again. No paid subscription is required within the free plan's allowance. A key is not bundled with the CLI.

Run the opt-in 100,000-job memory test with `DORKER_BH_STRESS=1 go test ./internal/scan -run TestHundredThousandJobsHeap -v`.

Search HTML and anti-bot behavior can change without notice. A query blocked by an engine is reported as a failed job; the checkpoint retains only successful jobs. The tool never follows result links or scans discovered URLs.

DuckDuckGo may return HTTP 202 with a bot challenge, including from its Lite endpoint. This is an upstream block; use `-e bing` or another provider and retry DuckDuckGo later. After upgrading from v0.1.0, start a fresh scan with `--resume=` and a new output file to discard any previously saved URLs outside the target scope.
