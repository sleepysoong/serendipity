package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Result represents a single search result.
type Result struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

// WebSection represents the web search results portion of the response.
type WebSection struct {
	Results []Result `json:"results"`
}

// SearchResponse represents the Brave Search Web API response payload.
type SearchResponse struct {
	Web WebSection `json:"web"`
}

// Search queries Brave Search API for a query string, extracts top N results,
// and merges their titles and descriptions into a single logical text block for grounding.
func Search(ctx context.Context, apiKey, query string, topN int) (string, error) {
	if apiKey == "" {
		return "", fmt.Errorf("Brave API Key is empty")
	}
	if query == "" {
		return "", fmt.Errorf("search query is empty")
	}
	if topN <= 0 {
		topN = 3 // default to top 3 results
	}

	// Prevent thread hang by setting a context timeout specifically for the network call
	searchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	endpoint := "https://api.search.brave.com/res/v1/web/search"
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("failed to parse Brave Search API URL: %w", err)
	}

	q := u.Query()
	q.Set("q", query)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(searchCtx, "GET", u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("failed to create search HTTP request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", apiKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Brave Search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Brave Search returned non-200 status code: %d, response: %s", resp.StatusCode, string(bodyBytes))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read Brave Search response body: %w", err)
	}

	var searchResponse SearchResponse
	if err := json.Unmarshal(bodyBytes, &searchResponse); err != nil {
		return "", fmt.Errorf("failed to unmarshal Brave Search response: %w", err)
	}

	var textBlock []string
	resultsCount := len(searchResponse.Web.Results)
	limit := topN
	if resultsCount < limit {
		limit = resultsCount
	}

	for i := 0; i < limit; i++ {
		res := searchResponse.Web.Results[i]
		block := fmt.Sprintf("[%d] Title: %s\nDescription: %s", i+1, res.Title, res.Description)
		textBlock = append(textBlock, block)
	}

	mergedResult := strings.Join(textBlock, "\n\n")
	return mergedResult, nil
}
