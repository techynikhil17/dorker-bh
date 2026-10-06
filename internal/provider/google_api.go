package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
)

// Existing Google Custom Search JSON API accounts can opt in via environment
// variables. The HTML provider remains available without credentials.
func (c *Client) searchGoogleAPI(ctx context.Context, query string) ([]string, error) {
	q := url.Values{}
	q.Set("key", c.googleAPIKey)
	q.Set("cx", c.googleCSEID)
	q.Set("q", query)
	q.Set("num", "10")
	data, err := c.fetch(ctx, "https://www.googleapis.com/customsearch/v1?"+q.Encode())
	if err != nil {
		return nil, err
	}
	defer releaseBody(data)
	return parseGoogleAPI(data)
}

func parseGoogleAPI(reader io.Reader) ([]string, error) {
	var result struct {
		Items []struct {
			Link string `json:"link"`
		} `json:"items"`
	}
	if err := json.NewDecoder(reader).Decode(&result); err != nil {
		return nil, err
	}
	var urls []string
	for _, item := range result.Items {
		if dest := destination("google", item.Link); dest != "" {
			urls = append(urls, dest)
		}
	}
	return urls, nil
}
