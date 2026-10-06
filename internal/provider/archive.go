package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
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
	var endpoint string
	switch engine {
	case "wayback":
		q.Set("matchType", "domain")
		q.Set("fl", "original,statuscode")
		q.Set("filter", "statuscode:200")
		q.Set("collapse", "urlkey")
		q.Set("gzip", "false")
		endpoint = "https://web.archive.org/cdx/search/cdx?" + q.Encode()
	case "commoncrawl":
		index, err := c.commonCrawlEndpoint(ctx)
		if err != nil {
			return nil, err
		}
		q.Set("matchType", "domain")
		q.Set("filter", "status:200")
		q.Set("collapse", "urlkey")
		endpoint = index + "?" + q.Encode()
	default:
		return nil, fmt.Errorf("unsupported archive provider %q", engine)
	}
	if engine == "commoncrawl" {
		if err := c.paceCommonCrawl(ctx); err != nil {
			return nil, err
		}
	}
	data, err := c.fetch(ctx, endpoint)
	var status statusError
	if errors.As(err, &status) && status.Code == 404 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer releaseBody(data)
	if engine == "wayback" {
		return parseWayback(data, domain)
	}
	return parseCommonCrawl(data, domain)
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
