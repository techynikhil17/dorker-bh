# dorker-bh

`dorker-bh` queries public DuckDuckGo, Bing, and Google HTML results for domain-based dorks. Use it only on targets you are authorized to audit. It prints discovered destination URLs, one per line, so results can be piped to other tools.

## Build

Go 1.26 or later:

```sh
go build -trimpath -o dorker-bh .
```

The module has no server-side services. Release builds disable CGO, producing a standalone binary. You can also install it with `go install github.com/techynikhil17/dorker-bh@latest` after a tagged release is published.

## Usage

```sh
./dorker-bh -l targets.txt -d dorks.txt -e duckduckgo,bing -c 10 -o results.txt
cat targets.txt | ./dorker-bh -d dorks.txt -s | httpx -silent
./dorker-bh -l targets.txt -d dorks.txt --json -o results.jsonl --resume .dorker-bh.resume
```

`targets.txt` contains one bare domain or subdomain per line. `dorks.txt` contains one query template per line. Blank lines and lines beginning with `#` are skipped. Each template should contain `{target}` or `%s`, for example:

```text
site:{target} inurl:admin
site:%s ext:env
```

Flags: `-l/--list`, `-d/--dorks`, `-e/--engines` (default `duckduckgo`), `-c/--concurrency` (default `10`), `-p/--proxies`, `-o/--output`, `--delay`, `--timeout` (default `10s`), `--retries` (default `3`), `--json`, `-s/--silent`, `-v/--verbose`, and `--resume`.

Proxy files accept `http://`, `https://`, and `socks5://` URLs, one per line. A proxy is selected for every request, including retries. `--silent` always prints raw URLs to stdout, even when `--json` writes JSON Lines to the output file. Operational messages go to stderr.

`--resume FILE` appends completed query IDs to the checkpoint and appends to `-o` if supplied. On restart, completed queries are skipped and existing output URLs are loaded for deduplication. For a durable resumed result set, use `--resume` together with `-o`.

Search HTML and anti-bot behavior can change without notice. A query blocked by an engine is reported as a failed job; the checkpoint retains only successful jobs. The tool never follows result links or scans discovered URLs.
