package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

	// A long task → show a progress card with a working Stop (cancels the child
	// context feeding the searches + synthesis).
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	prog := ui.StartProgress(fmt.Sprintf("Researching %q", query), cancel)

	// 1. Search (shared DuckDuckGo scraper also used by web_search)
	prog.Note("Searching the web…")
	results, err := ddgSearch(rctx, query, 5)
	if err != nil {
		prog.Fail("Search failed")
		return "", fmt.Errorf("search failed: %w", err)
	}

	var researchContext strings.Builder
	researchContext.WriteString(fmt.Sprintf("Research Results for: %s\n\n", query))

	// 2. Fetch content from top results
	linksCount := 0
	for _, r := range results {
		link := r.URL

		prog.Update(linksCount, 3, "Reading sources…")
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
		prog.Fail("Not enough found")
		return "I couldn't find enough information on the web to provide a detailed answer.", nil
	}

	// 4. Synthesize answer using LLM
	prog.Note("Synthesizing answer…")
	prompt := fmt.Sprintf(`Answer the question directly and CONCISELY from the web results below, like a helpful assistant speaking aloud. Rules:
- Lead with the direct answer in the first sentence.
- Keep it to 2–4 short sentences. Do NOT produce a long report, bullet lists of every figure, or a "Sources:" section.
- Mention only the single most relevant number/fact; skip the rest.
- If the results are a forecast for a specific date rather than live data, say so in a few words ("as of <date>") — don't refuse or over-explain.

Question: "%s"

Web results:
%s`, query, researchContext.String())

	finalAnswer, err := t.Provider.Generate(rctx, prompt, nil)
	if err != nil {
		prog.Fail("Synthesis failed")
		return "", fmt.Errorf("synthesis failed: %w", err)
	}

	prog.Done("Research complete", nil)
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
