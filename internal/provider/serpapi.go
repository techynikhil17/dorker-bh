package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

type serpAPIResponse struct {
	Error          string `json:"error"`
	SearchMetadata struct {
		Status string `json:"status"`
	} `json:"search_metadata"`
	OrganicResults []struct {
		Link string `json:"link"`
	} `json:"organic_results"`
}

// searchBingSerpAPI uses the structured Bing results service when a key is set.
// The direct public HTML provider remains available without a key.
func (c *Client) searchBingSerpAPI(ctx context.Context, query string) ([]string, error) {
	u, err := url.Parse(c.serpAPIBaseURL)
	if err != nil {
		return nil, fmt.Errorf("Bing results service URL: %w", err)
	}
	params := u.Query()
	params.Set("engine", "bing")
	params.Set("q", query)
	params.Set("api_key", c.serpAPIKey)
	params.Set("output", "json")
	u.RawQuery = params.Encode()
	body, err := c.fetch(ctx, u.String())
	if err != nil {
		// net/http can include the request URL (and key) in url.Error.
		var requestError *url.Error
		if errors.As(err, &requestError) {
			err = requestError.Err
		}
		return nil, fmt.Errorf("Bing results service request: %s", strings.ReplaceAll(err.Error(), c.serpAPIKey, "[redacted]"))
	}
	defer releaseBody(body)
	results, err := parseSerpAPIResults(body.Bytes())
	if err != nil {
		return nil, errors.New(strings.ReplaceAll(err.Error(), c.serpAPIKey, "[redacted]"))
	}
	return results, nil
}

func parseSerpAPIResults(body []byte) ([]string, error) {
	var response serpAPIResponse
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&response); err != nil {
		return nil, fmt.Errorf("decode Bing results service response: %w", err)
	}
	if response.Error != "" {
		return nil, fmt.Errorf("Bing results service: %s", response.Error)
	}
	if !strings.EqualFold(response.SearchMetadata.Status, "Success") {
		return nil, fmt.Errorf("Bing results service returned status %q", response.SearchMetadata.Status)
	}
	results := make([]string, 0, len(response.OrganicResults))
	seen := make(map[string]struct{}, len(response.OrganicResults))
	for _, item := range response.OrganicResults {
		link := destination("bing", item.Link)
		if link == "" {
			continue
		}
		if _, exists := seen[link]; exists {
			continue
		}
		seen[link] = struct{}{}
		results = append(results, link)
	}
	return results, nil
}
