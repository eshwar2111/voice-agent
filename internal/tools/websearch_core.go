package tools

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// SearchResult is one parsed web-search hit.
type SearchResult struct {
	Title   string
	URL     string
	Snippet string
}

var (
	ddgResultRe  = regexp.MustCompile(`(?s)class="result__a"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	ddgSnippetRe = regexp.MustCompile(`(?s)class="result__snippet"[^>]*>(.*?)</a>`)
	htmlTagRe    = regexp.MustCompile(`<[^>]+>`)
)

// stripTags removes HTML tags and unescapes basic entities, trimming whitespace.
func stripTags(s string) string {
	s = htmlTagRe.ReplaceAllString(s, "")
	s = strings.NewReplacer("&amp;", "&", "&#x27;", "'", "&quot;", `"`, "&lt;", "<", "&gt;", ">", "&nbsp;", " ").Replace(s)
	return strings.TrimSpace(s)
}

// decodeDDGURL turns a //duckduckgo.com/l/?uddg=<encoded> redirect into the real target.
func decodeDDGURL(href string) string {
	href = strings.TrimSpace(href)
	if strings.Contains(href, "uddg=") {
		if u, err := url.Parse(href); err == nil {
			if t := u.Query().Get("uddg"); t != "" {
				return t
			}
		}
	}
	if strings.HasPrefix(href, "//") {
		return "https:" + href
	}
	return href
}

// parseDDGResults extracts up to max results from a DuckDuckGo HTML-endpoint page.
// Never panics; returns a (possibly empty, non-nil) slice on unexpected markup.
func parseDDGResults(html string, max int) []SearchResult {
	out := []SearchResult{}
	links := ddgResultRe.FindAllStringSubmatch(html, -1)
	snips := ddgSnippetRe.FindAllStringSubmatch(html, -1)
	for i, m := range links {
		if len(out) >= max {
			break
		}
		u := decodeDDGURL(m[1])
		if u == "" || strings.Contains(u, "duckduckgo.com") {
			continue
		}
		sr := SearchResult{Title: stripTags(m[2]), URL: u}
		if i < len(snips) {
			sr.Snippet = stripTags(snips[i][1])
		}
		out = append(out, sr)
	}
	return out
}

// ddgSearch runs one live DuckDuckGo query and returns up to max parsed results.
func ddgSearch(ctx context.Context, query string, max int) ([]SearchResult, error) {
	searchURL := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return parseDDGResults(string(body), max), nil
}
