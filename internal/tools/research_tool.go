package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/yourname/voice-agent/internal/llm"
	"github.com/yourname/voice-agent/internal/ui"
)

type ResearchTool struct {
	Provider llm.Provider
}

func (t *ResearchTool) Name() string {
	return "research"
}

func (t *ResearchTool) Description() string {
	return "Performs deep web research to answer a question. It searches the web, fetches content from top results, and synthesizes a comprehensive answer."
}

func (t *ResearchTool) Parameters() string {
	return `{"query": "string (required - the research question or search query)"}`
}

func (t *ResearchTool) RequiresConfirmation() bool {
	return false
}

type ResearchArgs struct {
	Query string `json:"query"`
}

func (t *ResearchTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params ResearchArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	query := params.Query
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("missing query parameter")
	}

	ui.ShowNotification(fmt.Sprintf("Researching: %s...", query))

	// 1. Search (using DuckDuckGo HTML/Lite for simplicity without API keys)
	searchURL := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(query))

	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("GET", searchURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	// 2. Extract top 3-5 links (very basic regex for this MVP)
	re := regexp.MustCompile(`class="result__a" href="([^"]+)"`)
	matches := re.FindAllStringSubmatch(html, 5)

	var researchContext strings.Builder
	researchContext.WriteString(fmt.Sprintf("Research Results for: %s\n\n", query))

	// 3. Fetch content from top results
	linksCount := 0
	for _, match := range matches {
		link := match[1]
		// Decode URL if needed
		if strings.Contains(link, "uddg=") {
			u, _ := url.Parse(link)
			link = u.Query().Get("uddg")
		}

		if link == "" || strings.Contains(link, "duckduckgo.com") {
			continue
		}

		ui.ShowNotification(fmt.Sprintf("Reading: %s", link))
		content, err := fetchPageText(link)
		if err == nil && len(content) > 200 {
			researchContext.WriteString(fmt.Sprintf("--- Source: %s ---\n", link))
			// Limit content per page to keep context manageable
			if len(content) > 3000 {
				content = content[:3000] + "..."
			}
			researchContext.WriteString(content)
			researchContext.WriteString("\n\n")
			linksCount++
		}
		if linksCount >= 3 {
			break
		}
	}

	if researchContext.Len() < 100 {
		return "I couldn't find enough information on the web to provide a detailed answer.", nil
	}

	// 4. Synthesize answer using LLM
	ui.ShowNotification("Synthesizing answer...")
	prompt := fmt.Sprintf("Based on the following web research results, provide a comprehensive and detailed answer to the question: \"%s\". Use a professional and helpful tone. Cite sources if possible.\n\n%s", query, researchContext.String())

	finalAnswer, err := t.Provider.Generate(ctx, prompt, nil)
	if err != nil {
		return "", fmt.Errorf("synthesis failed: %w", err)
	}

	// Also trigger "Speak" via state change if needed, but usually the engine handles the output
	return finalAnswer, nil
}

func fetchPageText(urlStr string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest("GET", urlStr, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Extremely basic HTML to text conversion (removes tags, scripts, styles)
	text := string(body)

	// Remove <script> and <style> sections
	reScript := regexp.MustCompile(`(?s)<script.*?>.*?</script>`)
	text = reScript.ReplaceAllString(text, "")
	reStyle := regexp.MustCompile(`(?s)<style.*?>.*?</style>`)
	text = reStyle.ReplaceAllString(text, "")

	// Remove all HTML tags
	reTags := regexp.MustCompile(`<.*?>`)
	text = reTags.ReplaceAllString(text, " ")

	// Collapse whitespace
	reSpace := regexp.MustCompile(`\s+`)
	text = reSpace.ReplaceAllString(text, " ")

	return strings.TrimSpace(text), nil
}
