package search

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// ArticleMeta represents metadata of a candidate article.
type ArticleMeta struct {
	ID          string
	URL         string
	Title       string
	Description string
	Section     string
}

// FetchYonhapTopNews fetches the front page of Yonhap News, extracts domestic article IDs,
// and then fetches the metadata for the top N unique articles.
func FetchYonhapTopNews(ctx context.Context, topN int) ([]ArticleMeta, error) {
	if topN <= 0 {
		topN = 10
	}

	req, err := http.NewRequestWithContext(ctx, "GET", "https://www.yna.co.kr", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("yonhap frontpage request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yonhap frontpage returned %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	html := string(bodyBytes)

	// Extract IDs using regex: view/AKR[0-9A-Z]+(?=\?section=(politics|economy|society|industry)/)
	idRegex := regexp.MustCompile(`view/(AKR[0-9A-Z]+)\?section=(politics|economy|society|industry|culture|entertainment|sports|local)/`)
	matches := idRegex.FindAllStringSubmatch(html, -1)

	uniqueIDs := make(map[string]bool)
	var ids []string
	for _, match := range matches {
		id := match[1]
		if !uniqueIDs[id] {
			uniqueIDs[id] = true
			ids = append(ids, id)
		}
	}

	// Also get fallback IDs if section specific ones are not enough
	fallbackRegex := regexp.MustCompile(`view/(AKR[0-9A-Z]+)`)
	fallbackMatches := fallbackRegex.FindAllStringSubmatch(html, -1)
	for _, match := range fallbackMatches {
		id := match[1]
		if !uniqueIDs[id] {
			uniqueIDs[id] = true
			ids = append(ids, id)
		}
	}

	if len(ids) > 30 {
		ids = ids[:30]
	}

	var articles []ArticleMeta
	for _, id := range ids {
		if len(articles) >= topN {
			break
		}

		meta, err := fetchYonhapMeta(ctx, client, id)
		if err != nil || meta == nil {
			continue
		}
		
		// Filter out purely international or unimportant ones if we can tell,
		// though we already filtered by section mostly. Let's just append.
		if meta.Section == "국제" {
			continue
		}
		
		articles = append(articles, *meta)
	}

	return articles, nil
}

func fetchYonhapMeta(ctx context.Context, client *http.Client, id string) (*ArticleMeta, error) {
	url := "https://www.yna.co.kr/view/" + id
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}

	// Read only first ~300 lines equivalent (let's say 16KB is enough for head)
	headBytes := make([]byte, 16384)
	n, _ := io.ReadFull(resp.Body, headBytes)
	html := string(headBytes[:n])

	titleMatch := regexp.MustCompile(`<meta property="og:title" content="([^"]+)"`).FindStringSubmatch(html)
	descMatch := regexp.MustCompile(`<meta property="og:description" content="([^"]+)"`).FindStringSubmatch(html)
	sectionMatch := regexp.MustCompile(`<meta property="article:section" content="([^"]+)"`).FindStringSubmatch(html)

	if titleMatch == nil || descMatch == nil {
		return nil, nil
	}

	section := ""
	if sectionMatch != nil {
		section = sectionMatch[1]
	}

	return &ArticleMeta{
		ID:          id,
		URL:         url,
		Title:       strings.ReplaceAll(titleMatch[1], "&quot;", "\""),
		Description: strings.ReplaceAll(descMatch[1], "&quot;", "\""),
		Section:     section,
	}, nil
}

// FetchYonhapArticleBody fetches the full article and extracts the body text.
func FetchYonhapArticleBody(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("article returned %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	html := string(bodyBytes)

	// Extract <article id="articleWrap">...</article> or <article id="dic_area">
	articleRegex := regexp.MustCompile(`(?s)<article id="(articleWrap|dic_area)"[^>]*>(.*?)</article>`)
	match := articleRegex.FindStringSubmatch(html)
	if match == nil {
		return "", fmt.Errorf("could not find articleWrap or dic_area")
	}

	content := match[2]

	// Strip HTML tags
	tagRegex := regexp.MustCompile(`<[^>]*>`)
	text := tagRegex.ReplaceAllString(content, " ")

	// Clean up whitespaces
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.ReplaceAll(text, "&quot;", "\"")
	text = strings.ReplaceAll(text, "&apos;", "'")
	
	spaceRegex := regexp.MustCompile(`\s+`)
	text = spaceRegex.ReplaceAllString(text, " ")
	
	return strings.TrimSpace(text), nil
}
