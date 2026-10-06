package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Archive providers yield indexed URLs for a domain. Dork syntax is not
// meaningful to CDX indexes, so these providers run once per target.
func (c *Client) searchArchive(ctx context.Context, engine, domain string) ([]string, error) {
	if engine == "commoncrawl" {
		// Common Crawl asks clients to avoid concurrent index requests.
		c.commonCrawlMu.Lock()
		defer c.commonCrawlMu.Unlock()
	}
	q := url.Values{}
	q.Set("url", domain)
	q.Set("output", "json")
	q.Set("limit", "1000")
	var base string
	switch engine {
	case "wayback":
		q.Set("matchType", "domain")
		q.Set("fl", "original,statuscode")
		q.Set("filter", "statuscode:200")
		q.Set("collapse", "urlkey")
		q.Set("gzip", "false")
		base = "https://web.archive.org/cdx/search/cdx"
	case "commoncrawl":
		index, err := c.commonCrawlEndpoint(ctx)
		if err != nil {
			return nil, err
		}
		q.Set("matchType", "domain")
		q.Set("filter", "status:200")
		q.Set("collapse", "urlkey")
		base = index
	default:
		return nil, fmt.Errorf("unsupported archive provider %q", engine)
	}
	fetch := func(values url.Values) (*bytes.Buffer, error) {
		if engine == "commoncrawl" {
			if err := c.paceCommonCrawl(ctx); err != nil {
				return nil, err
			}
		}
		return c.fetch(ctx, base+"?"+values.Encode())
	}
	countQ := cloneValues(q)
	countQ.Del("limit")
	countQ.Set("pageSize", "1")
	countQ.Set("showNumPages", "true")
	countData, countErr := fetch(countQ)
	pages := 0
	if countErr == nil {
		pages, countErr = parseArchivePageCount(countData)
		releaseBody(countData)
	}
	queries := []url.Values{}
	if countErr == nil && pages > 0 {
		for page := 0; page < pages; page++ {
			pageQ := cloneValues(q)
			pageQ.Del("limit")
			pageQ.Set("pageSize", "1")
			pageQ.Set("page", strconv.Itoa(page))
			queries = append(queries, pageQ)
		}
	} else {
		queries = append(queries, q)
	}
	seen := map[string]struct{}{}
	var out []string
	for _, values := range queries {
		data, err := fetch(values)
		var status statusError
		if errors.As(err, &status) && status.Code == 404 {
			continue
		}
		if err != nil {
			return nil, err
		}
		var urls []string
		if engine == "wayback" {
			urls, err = parseWayback(data, domain)
		} else {
			urls, err = parseCommonCrawl(data, domain)
		}
		releaseBody(data)
		if err != nil {
			return nil, err
		}
		for _, raw := range urls {
			if _, ok := seen[raw]; !ok {
				seen[raw] = struct{}{}
				out = append(out, raw)
			}
		}
	}
	return out, nil
}

func cloneValues(in url.Values) url.Values {
	out := url.Values{}
	for k, values := range in {
		out[k] = append([]string(nil), values...)
	}
	return out
}
func parseArchivePageCount(r io.Reader) (int, error) {
	var v struct {
		Pages int `json:"pages"`
	}
	if err := json.NewDecoder(r).Decode(&v); err != nil {
		return 0, err
	}
	if v.Pages < 0 {
		return 0, errors.New("archive returned invalid page count")
	}
	return v.Pages, nil
}

func (c *Client) commonCrawlEndpoint(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.commonCrawlIndex != "" {
		endpoint := c.commonCrawlIndex
		c.mu.Unlock()
		return endpoint, nil
	}
	c.mu.Unlock()
	if err := c.paceCommonCrawl(ctx); err != nil {
		return "", err
	}
	data, err := c.fetch(ctx, "https://index.commoncrawl.org/collinfo.json")
	if err != nil {
		return "", err
	}
	defer releaseBody(data)
	var indexes []struct {
		API string `json:"cdx-api"`
	}
	if err := json.Unmarshal(data.Bytes(), &indexes); err != nil {
		return "", fmt.Errorf("Common Crawl index list: %w", err)
	}
	if len(indexes) == 0 || !strings.HasPrefix(indexes[0].API, "https://index.commoncrawl.org/") {
		return "", errors.New("Common Crawl returned no valid index")
	}
	c.mu.Lock()
	c.commonCrawlIndex = indexes[0].API
	c.mu.Unlock()
	return indexes[0].API, nil
}

// Call only while commonCrawlMu is held.
func (c *Client) paceCommonCrawl(ctx context.Context) error {
	if wait := time.Until(c.lastCommonCrawl.Add(time.Second)); wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	c.lastCommonCrawl = time.Now()
	return nil
}

func archiveURL(raw, domain string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host != domain && !strings.HasSuffix(host, "."+domain) {
		return ""
	}
	u.Fragment = ""
	return u.String()
}

func parseWayback(reader io.Reader, domain string) ([]string, error) {
	var rows [][]string
	if err := json.NewDecoder(reader).Decode(&rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	urlCol, statusCol := -1, -1
	for i, col := range rows[0] {
		if col == "original" {
			urlCol = i
		}
		if col == "statuscode" {
			statusCol = i
		}
	}
	if urlCol < 0 || statusCol < 0 {
		return nil, errors.New("Wayback response lacks required columns")
	}
	var urls []string
	for _, row := range rows[1:] {
		if len(row) <= urlCol || len(row) <= statusCol || row[statusCol] != "200" {
			continue
		}
		if result := archiveURL(row[urlCol], domain); result != "" {
			urls = append(urls, result)
		}
	}
	return urls, nil
}

func parseCommonCrawl(reader io.Reader, domain string) ([]string, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 2<<20)
	var urls []string
	for scanner.Scan() {
		var row struct {
			URL    string `json:"url"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, fmt.Errorf("Common Crawl response: %w", err)
		}
		if row.Status == "200" {
			if result := archiveURL(row.URL, domain); result != "" {
				urls = append(urls, result)
			}
		}
	}
	return urls, scanner.Err()
}
