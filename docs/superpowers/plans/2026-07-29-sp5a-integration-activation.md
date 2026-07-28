# SP5a — Integration Activation & Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `web_search` return real results (sharing one DuckDuckGo helper with `research`), and fix/extend Spotify (no-device 404 auto-recovery + transfer, seek, like/save with a scope add, hardened parsing).

**Architecture:** Two independent feature tracks in `internal/tools`. Track 1 (web search) adds `websearch_core.go` consumed by `web_search.go` + `research_tool.go`. Track 2 (Spotify) adds pure helpers + three new tools in `spotify_tools.go`/new files, wraps existing playback tools with a one-shot device-recovery retry, and adds a library scope. Pure functions (`ddgSearch` parsing, `pickDevice`, `parseSeekPosition`, safe accessors) are the tested seams; HTTP/OAuth glue follows existing patterns and is verified manually.

**Tech Stack:** Go 1.26, stdlib `net/http`, `regexp`, `encoding/json`. `internal/tools` links CGO (robotgo) — its tests still run with the toolchain on PATH.

## Global Constraints

- Prefix EVERY go command with `export PATH="$PATH:/c/w64devkit/bin"`. Verify with `go test ./internal/tools/...` and `go build ./internal/tools/...` (NOT `go build ./cmd/app` except a final optional build).
- Explicit `git add <files>` only — NEVER `git add -A` (config.json holds secrets).
- Commit messages end with: `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`.
- No new external dependencies. No real search API (that's slice B). Microsoft/Google-writes/sports out of scope.
- New tools set `RequiresConfirmation() bool { return false }`.
- The Spotify HTTP helpers `spotifyGet/Put/Post(ctx, client, path[, body])` return `([]byte, error)` and on a 4xx return `fmt.Errorf("Spotify error (%s): %s", status, body)` — so a `NO_ACTIVE_DEVICE` 404 surfaces as an error whose message CONTAINS `"NO_ACTIVE_DEVICE"`. Detect via the error string; do not add a status-returning variant.
- Tools get their client with `client, err := auth.GetSpotifyClient(ctx, t.Cfg)`; base path constant is `spotifyBase`.

---

## Task 1: Web search core + repurpose `web_search`

**Files:**
- Create: `internal/tools/websearch_core.go`, `internal/tools/websearch_core_test.go`, `internal/tools/testdata/ddg_sample.html`
- Modify: `internal/tools/web_search.go` (return results), `internal/tools/research_tool.go` (use `ddgSearch`)

**Interfaces:**
- Consumes: nothing new.
- Produces: `type SearchResult struct { Title, URL, Snippet string }`; `func ddgSearch(ctx context.Context, query string, max int) ([]SearchResult, error)`; `func parseDDGResults(html string, max int) []SearchResult` (pure, the tested seam).

- [ ] **Step 1: Create the HTML fixture** — `internal/tools/testdata/ddg_sample.html`

Save a minimal but realistic DuckDuckGo HTML-endpoint snippet with two results. Use this exact content:

```html
<div class="results">
  <div class="result results_links results_links_deep web-result">
    <div class="links_main">
      <a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdoc%2F&amp;rut=x">The Go Programming Language Docs</a>
      <a class="result__snippet" href="https://go.dev/doc/">Official documentation for the Go programming language, including tutorials and references.</a>
    </div>
  </div>
  <div class="result results_links results_links_deep web-result">
    <div class="links_main">
      <a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fen.wikipedia.org%2Fwiki%2FGo_(programming_language)&amp;rut=y">Go (programming language) - Wikipedia</a>
      <a class="result__snippet" href="https://en.wikipedia.org/wiki/Go">Go is a statically typed, compiled high-level programming language designed at Google.</a>
    </div>
  </div>
</div>
```

- [ ] **Step 2: Write the failing test** — `internal/tools/websearch_core_test.go`

```go
package tools

import (
	"os"
	"strings"
	"testing"
)

func TestParseDDGResults(t *testing.T) {
	html, err := os.ReadFile("testdata/ddg_sample.html")
	if err != nil {
		t.Fatal(err)
	}
	res := parseDDGResults(string(html), 6)
	if len(res) != 2 {
		t.Fatalf("expected 2 results, got %d: %+v", len(res), res)
	}
	if !strings.Contains(res[0].Title, "Go Programming Language") {
		t.Errorf("title[0]=%q", res[0].Title)
	}
	// URL must be the decoded uddg target, not the duckduckgo redirect.
	if res[0].URL != "https://go.dev/doc/" {
		t.Errorf("url[0]=%q want https://go.dev/doc/", res[0].URL)
	}
	if !strings.Contains(res[0].Snippet, "Official documentation") {
		t.Errorf("snippet[0]=%q", res[0].Snippet)
	}
}

func TestParseDDGResultsGarbageNoPanic(t *testing.T) {
	if got := parseDDGResults("<html>no results here</html>", 6); len(got) != 0 {
		t.Errorf("garbage should yield 0 results, got %d", len(got))
	}
	if got := parseDDGResults("", 6); got == nil || len(got) != 0 {
		t.Errorf("empty should yield empty (non-nil) slice")
	}
}

func TestParseDDGResultsRespectsMax(t *testing.T) {
	html, _ := os.ReadFile("testdata/ddg_sample.html")
	if got := parseDDGResults(string(html), 1); len(got) != 1 {
		t.Errorf("max=1 should cap results, got %d", len(got))
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/tools/ -run TestParseDDG`
Expected: FAIL — `undefined: parseDDGResults`.

- [ ] **Step 4: Write the implementation** — `internal/tools/websearch_core.go`

```go
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
```

- [ ] **Step 5: Run test to verify it passes**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/tools/ -run TestParseDDG -v`
Expected: PASS.

- [ ] **Step 6: Repurpose `web_search.go`**

Replace the body of `WebSearchTool.Execute` so it returns results instead of opening a browser, and update `Description()`. Keep `Name()`/`Parameters()`/`RequiresConfirmation()`. New `Execute`:

```go
func (w *WebSearchTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params WebSearchArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}
	query := strings.TrimSpace(params.Query)
	if query == "" {
		return "", errors.New("missing query parameter")
	}
	results, err := ddgSearch(ctx, query, 6)
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}
	if len(results) == 0 {
		return "No results found for: " + query, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Web results for %q:\n\n", query)
	for i, r := range results {
		fmt.Fprintf(&b, "%d. %s — %s\n", i+1, r.Title, r.URL)
		if r.Snippet != "" {
			fmt.Fprintf(&b, "   %s\n", r.Snippet)
		}
	}
	return b.String(), nil
}
```
Update `Description()` to: `"Searches the web (DuckDuckGo) and returns the top results (title, URL, snippet) as text to reason over. For a deep synthesized answer use 'research' instead."` Remove the now-unused imports (`net/url`, `os/exec`) and add any needed (`errors`, `strings` already present).

- [ ] **Step 7: DRY `research_tool.go` onto `ddgSearch`**

In `ResearchTool.Execute`, replace the inline search request + regex link extraction (the block building `searchURL`, doing the `http.Get`, and `re := regexp.MustCompile(...)` / `matches`) with:

```go
results, err := ddgSearch(ctx, query, 5)
if err != nil {
	return "", fmt.Errorf("search failed: %w", err)
}
```
Then iterate `results` (using `r.URL`) where the old code iterated `matches`/`link`. Keep the page-fetch (`fetchPageText`), the 3-source cap, the 3000-char truncation, and the LLM synthesis unchanged. Remove now-unused imports (`net/url`, `regexp`, and `io`/`net/http`/`time` only if no longer referenced — check `fetchPageText` still needs them; it does, so keep `net/http`/`time`/`io`).

- [ ] **Step 8: Verify build + tests + commit**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/tools/ -run "TestParseDDG" && go build ./internal/tools/...`
Expected: tests PASS, build OK.

```bash
git add internal/tools/websearch_core.go internal/tools/websearch_core_test.go internal/tools/testdata/ddg_sample.html internal/tools/web_search.go internal/tools/research_tool.go
git commit -m "feat(search): web_search returns real results; DRY the DDG scraper with research"
```

---

## Task 2: Spotify pure helpers (device pick, seek parse, safe accessors, delete)

**Files:**
- Modify: `internal/tools/spotify_tools.go` (add helpers)
- Test: `internal/tools/spotify_helpers_test.go`

**Interfaces:**
- Produces:
  - `type SpotifyDevice struct { ID string \`json:"id"\`; Name string \`json:"name"\`; IsActive bool \`json:"is_active"\` }`
  - `func pickDevice(devices []SpotifyDevice, preferName string) (id, name string)` — if `preferName != ""` → case-insensitive name match (or `"",""`); else already-active device, else first; `"",""` if empty.
  - `func parseSeekPosition(s string, currentMs int) (int, error)`
  - `func str(m map[string]any, key string) string`, `func nested(m map[string]any, keys ...string) map[string]any`, `func firstImageURL(images any) string`
  - `func spotifyDelete(ctx context.Context, client *http.Client, path string) ([]byte, error)`
  - `func isNoActiveDevice(err error) bool`

- [ ] **Step 1: Write the failing test** — `internal/tools/spotify_helpers_test.go`

```go
package tools

import (
	"errors"
	"testing"
)

func TestPickDevice(t *testing.T) {
	devs := []SpotifyDevice{
		{ID: "a", Name: "Phone", IsActive: false},
		{ID: "b", Name: "Laptop", IsActive: true},
		{ID: "c", Name: "Kitchen Speaker", IsActive: false},
	}
	// no preference → active wins
	if id, _ := pickDevice(devs, ""); id != "b" {
		t.Errorf("no-pref should pick active 'b', got %q", id)
	}
	// name match (case-insensitive) beats active
	if id, _ := pickDevice(devs, "phone"); id != "a" {
		t.Errorf("name match should pick 'a', got %q", id)
	}
	// name miss → "",""
	if id, name := pickDevice(devs, "car"); id != "" || name != "" {
		t.Errorf("name miss should be empty, got %q/%q", id, name)
	}
	// no active, no pref → first
	none := []SpotifyDevice{{ID: "x", Name: "X"}, {ID: "y", Name: "Y"}}
	if id, _ := pickDevice(none, ""); id != "x" {
		t.Errorf("no active → first 'x', got %q", id)
	}
	// empty list
	if id, _ := pickDevice(nil, ""); id != "" {
		t.Errorf("empty list → '', got %q", id)
	}
}

func TestParseSeekPosition(t *testing.T) {
	cases := []struct {
		in      string
		cur     int
		want    int
		wantErr bool
	}{
		{"1:30", 0, 90000, false},
		{"0:05", 0, 5000, false},
		{"90", 0, 90000, false},         // bare seconds
		{"+30s", 60000, 90000, false},   // relative forward
		{"-15s", 20000, 5000, false},    // relative back
		{"-30s", 10000, 0, false},       // floor at 0
		{"abc", 0, 0, true},
	}
	for _, c := range cases {
		got, err := parseSeekPosition(c.in, c.cur)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseSeekPosition(%q) expected error", c.in)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("parseSeekPosition(%q,%d)=%d,%v want %d", c.in, c.cur, got, err, c.want)
		}
	}
}

func TestSafeAccessors(t *testing.T) {
	m := map[string]any{
		"name": "Song",
		"album": map[string]any{
			"images": []any{map[string]any{"url": "http://img/1"}},
		},
		"bad": 42,
	}
	if str(m, "name") != "Song" {
		t.Error("str name")
	}
	if str(m, "missing") != "" || str(m, "bad") != "" {
		t.Error("str missing/wrong-type must be empty")
	}
	if nested(m, "album", "images") == nil {
		t.Error("nested should not be nil")
	}
	if nested(m, "album", "nope") != nil || nested(m, "missing", "x") != nil {
		t.Error("nested miss must be nil")
	}
	if firstImageURL(nested(m, "album")["images"]) != "http://img/1" {
		t.Error("firstImageURL")
	}
	if firstImageURL(nil) != "" || firstImageURL("bad") != "" {
		t.Error("firstImageURL bad shape must be empty")
	}
}

func TestIsNoActiveDevice(t *testing.T) {
	if !isNoActiveDevice(errors.New("Spotify error (404 Not Found): {\"error\":{\"reason\":\"NO_ACTIVE_DEVICE\"}}")) {
		t.Error("should detect NO_ACTIVE_DEVICE")
	}
	if isNoActiveDevice(nil) || isNoActiveDevice(errors.New("other")) {
		t.Error("must not false-positive")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/tools/ -run "TestPickDevice|TestParseSeek|TestSafeAccessors|TestIsNoActive"`
Expected: FAIL — undefined helpers.

- [ ] **Step 3: Write the implementation** (append to `internal/tools/spotify_tools.go`)

```go
type SpotifyDevice struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	IsActive bool   `json:"is_active"`
}

// pickDevice chooses a target device id+name. With preferName set it does a
// case-insensitive name match (or "","" on miss). Otherwise an already-active
// device wins, else the first device. "","" if the list is empty.
func pickDevice(devices []SpotifyDevice, preferName string) (string, string) {
	if preferName != "" {
		for _, d := range devices {
			if strings.EqualFold(d.Name, preferName) {
				return d.ID, d.Name
			}
		}
		return "", ""
	}
	for _, d := range devices {
		if d.IsActive {
			return d.ID, d.Name
		}
	}
	if len(devices) > 0 {
		return devices[0].ID, devices[0].Name
	}
	return "", ""
}

// parseSeekPosition converts "1:30" / "90" / raw ms-ish / relative "+30s"/"-15s"
// into an absolute position in ms (floored at 0). currentMs is used for relative.
func parseSeekPosition(s string, currentMs int) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty position")
	}
	// relative: +30s / -15s
	if (s[0] == '+' || s[0] == '-') && strings.HasSuffix(s, "s") {
		n, err := strconv.Atoi(strings.TrimSuffix(s[1:], "s"))
		if err != nil {
			return 0, fmt.Errorf("bad relative seek %q", s)
		}
		delta := n * 1000
		if s[0] == '-' {
			delta = -delta
		}
		pos := currentMs + delta
		if pos < 0 {
			pos = 0
		}
		return pos, nil
	}
	// mm:ss
	if strings.Contains(s, ":") {
		parts := strings.SplitN(s, ":", 2)
		mm, e1 := strconv.Atoi(parts[0])
		ss, e2 := strconv.Atoi(parts[1])
		if e1 != nil || e2 != nil {
			return 0, fmt.Errorf("bad mm:ss %q", s)
		}
		return (mm*60 + ss) * 1000, nil
	}
	// bare number = seconds
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("bad seek %q", s)
	}
	return n * 1000, nil
}

func str(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func nested(m map[string]any, keys ...string) map[string]any {
	cur := m
	for _, k := range keys {
		if cur == nil {
			return nil
		}
		next, ok := cur[k].(map[string]any)
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

func firstImageURL(images any) string {
	arr, ok := images.([]any)
	if !ok || len(arr) == 0 {
		return ""
	}
	if m, ok := arr[0].(map[string]any); ok {
		return str(m, "url")
	}
	return ""
}

func spotifyDelete(ctx context.Context, client *http.Client, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "DELETE", spotifyBase+path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create DELETE request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("DELETE failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Spotify error (%s): %s", resp.Status, string(body))
	}
	return io.ReadAll(resp.Body)
}

func isNoActiveDevice(err error) bool {
	return err != nil && strings.Contains(err.Error(), "NO_ACTIVE_DEVICE")
}
```

Ensure `strconv` is imported in `spotify_tools.go` (add to the import block if missing).

- [ ] **Step 4: Run test to verify it passes**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/tools/ -run "TestPickDevice|TestParseSeek|TestSafeAccessors|TestIsNoActive" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tools/spotify_tools.go internal/tools/spotify_helpers_test.go
git commit -m "feat(spotify): pure helpers — pickDevice, parseSeekPosition, safe accessors, delete"
```

---

## Task 3: `ensureActiveDevice` + new Spotify tools (seek, save, transfer) + scope

**Files:**
- Modify: `internal/tools/spotify_tools.go` (add `ensureActiveDevice`), `internal/auth/spotify_provider.go` (add scopes)
- Create: `internal/tools/spotify_seek.go`, `internal/tools/spotify_save.go`, `internal/tools/spotify_transfer.go`

**Interfaces:**
- Consumes: Task 2 helpers, `auth.GetSpotifyClient`, `spotifyGet/Put/Delete`.
- Produces: `func ensureActiveDevice(ctx context.Context, client *http.Client, preferName string) (string, error)`; tools `SpotifySeekTool`, `SpotifySaveTrackTool`, `SpotifyTransferTool` (each with `Cfg *config.Config`).

- [ ] **Step 1: Add `ensureActiveDevice`** (append to `spotify_tools.go`)

```go
// ensureActiveDevice guarantees playback has a target device. If preferName is
// empty and a device is already active, it returns that device's name without
// transferring. Otherwise it picks a device (pickDevice) and transfers playback
// to it (PUT /me/player, play:false). Returns the target device name.
func ensureActiveDevice(ctx context.Context, client *http.Client, preferName string) (string, error) {
	body, err := spotifyGet(ctx, client, "/me/player/devices")
	if err != nil {
		return "", err
	}
	var dr struct {
		Devices []SpotifyDevice `json:"devices"`
	}
	if err := json.Unmarshal(body, &dr); err != nil {
		return "", fmt.Errorf("could not read devices: %w", err)
	}
	id, name := pickDevice(dr.Devices, preferName)
	if id == "" {
		if preferName != "" {
			return "", fmt.Errorf("no Spotify device named %q found", preferName)
		}
		return "", fmt.Errorf("no Spotify devices available — open Spotify on a device first")
	}
	// already active and no explicit target → nothing to do
	for _, d := range dr.Devices {
		if d.ID == id && d.IsActive {
			return name, nil
		}
	}
	payload, _ := json.Marshal(map[string]any{"device_ids": []string{id}, "play": false})
	if _, err := spotifyPut(ctx, client, "/me/player", payload); err != nil {
		return "", err
	}
	return name, nil
}
```

- [ ] **Step 2: Create `spotify_transfer.go`**

Standard tool struct (`Cfg *config.Config`). `Name()="spotify_transfer"`, `RequiresConfirmation()=false`, params `{"device":"string (device name, e.g. 'Laptop')"}`. `Execute`: parse `device`, get client via `auth.GetSpotifyClient`, `name, err := ensureActiveDevice(ctx, client, device)`; on success return `"Transferred playback to " + name`. Follow the exact struct/method shape of `SpotifyPauseTool` in `spotify_tools.go`.

- [ ] **Step 3: Create `spotify_seek.go`**

Tool `SpotifySeekTool{Cfg}`, `Name()="spotify_seek"`, params `{"position":"string — mm:ss, seconds, or relative +30s/-15s"}`. `Execute`:
- get client; if position is relative (`starts with + or -`), fetch current progress via `GET /me/player/currently-playing` and read `progress_ms` (reuse the anonymous-struct decode pattern already used at `spotify_tools.go:34-55`), else pass `currentMs=0`.
- `ms, err := parseSeekPosition(position, currentMs)`.
- `_, err = spotifyPut(ctx, client, fmt.Sprintf("/me/player/seek?position_ms=%d", ms), nil)`.
- **Device-recovery retry:** if `isNoActiveDevice(err)`, call `ensureActiveDevice(ctx, client, "")` then retry the seek once.
- Return e.g. `"Seeked to <mm:ss>"`.

- [ ] **Step 4: Create `spotify_save.go`**

Tool `SpotifySaveTrackTool{Cfg}`, `Name()="spotify_save_track"`, params `{"action":"save|remove|check (default save)","track_id":"optional; defaults to current track"}`. `Execute`:
- get client; if `track_id` empty, fetch current track id from `GET /me/player/currently-playing` (`item.id`); error if nothing playing.
- `save` → `spotifyPut(ctx, client, "/me/tracks?ids="+id, nil)`; `remove` → `spotifyDelete(ctx, client, "/me/tracks?ids="+id)`; `check` → `spotifyGet(ctx, client, "/me/tracks/contains?ids="+id)` (parse `[true]`).
- **Scope-error UX:** if the returned error message contains `"403"` or `"Insufficient"`, return (nil error) the friendly string: `"Re-link Spotify to enable saving songs (⚙ → Spotify)."`
- Return e.g. `"Saved to your Liked Songs"` / `"Removed from Liked Songs"` / `"That song is saved"`/`"Not saved yet"`.

- [ ] **Step 5: Add library scopes** — `internal/auth/spotify_provider.go`

Add `"user-library-modify"` and `"user-library-read"` to the scope slice (the list at ~lines 35-48). Nothing else changes; existing tokens will 403 on save until the user re-links (handled in Step 4).

- [ ] **Step 6: Verify build + tests**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go build ./internal/tools/... ./internal/auth/... && go test ./internal/tools/ -run "TestPickDevice|TestParseSeek|TestSafeAccessors|TestIsNoActive"`
Expected: build OK; helper tests still PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/tools/spotify_tools.go internal/tools/spotify_seek.go internal/tools/spotify_save.go internal/tools/spotify_transfer.go internal/auth/spotify_provider.go
git commit -m "feat(spotify): ensureActiveDevice + seek/save/transfer tools + library scope"
```

---

## Task 4: 404 device-recovery retry on existing playback tools

**Files:**
- Modify: `internal/tools/spotify_tools.go` (wrap play/pause/next/previous/volume)

**Interfaces:**
- Consumes: `ensureActiveDevice`, `isNoActiveDevice` (Tasks 2-3).

For each of `SpotifyPlayTool` (its `playURI` and resume path), `SpotifyPauseTool`, `SpotifyNextTool`, `SpotifyPreviousTool`, `SpotifyVolumeTool`: locate the `spotifyPut`/`spotifyPost` call that performs the action and wrap it with a one-shot recovery. Extract a tiny helper to avoid repetition:

```go
// withDeviceRecovery runs do(); if it fails with NO_ACTIVE_DEVICE, it transfers
// to an available device and retries do() once.
func withDeviceRecovery(ctx context.Context, client *http.Client, do func() error) error {
	err := do()
	if isNoActiveDevice(err) {
		if _, derr := ensureActiveDevice(ctx, client, ""); derr != nil {
			return derr
		}
		return do()
	}
	return err
}
```

Then wrap each action call, e.g. in `SpotifyPauseTool.Execute`:
```go
err = withDeviceRecovery(ctx, client, func() error {
	_, e := spotifyPut(ctx, client, "/me/player/pause", nil)
	return e
})
```
Do the same for play (resume + `playURI`), next, previous, volume. Keep the existing success/return messages.

- [ ] **Step 1: Write a failing test for the recovery helper** — add to `spotify_helpers_test.go`

```go
func TestWithDeviceRecoveryRetriesOnce(t *testing.T) {
	calls := 0
	// first call reports NO_ACTIVE_DEVICE; ensureActiveDevice is not reachable
	// without a client, so this test only covers the non-recoverable branch:
	// a non-device error is returned as-is without retry.
	err := withDeviceRecovery(nil, nil, func() error {
		calls++
		return errorsNew("some other error")
	})
	if calls != 1 || err == nil {
		t.Fatalf("non-device error must not retry; calls=%d err=%v", calls, err)
	}
}
```
Add a tiny local `func errorsNew(s string) error { return errors.New(s) }` OR just use `errors.New` directly (ensure `errors` imported in the test). The NO_ACTIVE_DEVICE→retry path hits the network (ensureActiveDevice), so it is verified manually, not unit-tested.

- [ ] **Step 2: Run it to verify it fails**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/tools/ -run TestWithDeviceRecovery`
Expected: FAIL — `undefined: withDeviceRecovery`.

- [ ] **Step 3: Implement `withDeviceRecovery` and wrap the five tools** (as above).

- [ ] **Step 4: Verify build + tests**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/tools/ -run "TestWithDeviceRecovery|TestPickDevice|TestParseSeek" && go build ./internal/tools/...`
Expected: PASS + build OK.

- [ ] **Step 5: Commit**

```bash
git add internal/tools/spotify_tools.go internal/tools/spotify_helpers_test.go
git commit -m "feat(spotify): auto-recover NO_ACTIVE_DEVICE with one-shot transfer+retry"
```

---

## Task 5: Registration, assistant routing, docs

**Files:**
- Modify: `internal/tools/registry.go` (register 3 new tools), `internal/tools/spotify_assistant.go` (route them), `CLAUDE.md`, `README.md`

**Interfaces:**
- Consumes: `SpotifySeekTool`, `SpotifySaveTrackTool`, `SpotifyTransferTool`.

- [ ] **Step 1: Register the new tools** — in the `cfg != nil` Spotify block of `registry.go` (after `SpotifyContextualMoodTool` registration, ~line 175):

```go
		r.Register(&SpotifySeekTool{Cfg: cfg})
		r.Register(&SpotifySaveTrackTool{Cfg: cfg})
		r.Register(&SpotifyTransferTool{Cfg: cfg})
```

- [ ] **Step 2: Add NL routing awareness** — `spotify_assistant.go`

Find where `SpotifyAssistantTool` lists/enumerates the Spotify tools it can route to (its system/routing prompt or tool map). Add the three new tools with one-line descriptions so phrases like "like this song", "jump to 1:30", "skip ahead 30 seconds", "play on my laptop" resolve. If the assistant builds its prompt from a static list of tool names+descriptions, append the three; match the existing format exactly.

- [ ] **Step 3: Update docs**

- `README.md` Spotify section (and `CLAUDE.md` if it lists Spotify capabilities): note the new `spotify_seek`, `spotify_save_track`, `spotify_transfer`, the automatic no-device recovery, and that **saving songs needs a one-time Spotify re-link** (⚙ → Spotify) because it adds the `user-library-modify` scope. Note `web_search` now returns real result snippets.

- [ ] **Step 4: Full verification + commit**

```bash
export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/tools/... && go build ./internal/tools/...
```
Expected: all tools tests PASS; build OK. Optionally the full voice build:
`go build -tags whisper -ldflags="-s -w -H windowsgui" -o voice-agent.exe ./cmd/app`

```bash
git add internal/tools/registry.go internal/tools/spotify_assistant.go CLAUDE.md README.md
git commit -m "feat(spotify): register seek/save/transfer + assistant routing + docs"
```

---

## Self-Review notes (author)

- **Spec coverage:** web_search results + DRY (T1), Spotify device recovery/transfer (T3+T4), seek (T3), like/save + scope (T3), hardened parsing/safe accessors (T2), registration + routing (T5). All Feature-1/Feature-2 items map to a task.
- **pickDevice ordering correction:** the spec text said "active wins; else name match"; the plan makes **name match take priority when preferName is set** (correct for explicit "transfer to Laptop"), and active-wins only when no name is requested. Documented in T2. This is the intended behaviour.
- **Type consistency:** `SearchResult`, `parseDDGResults`, `ddgSearch`, `SpotifyDevice`, `pickDevice`, `parseSeekPosition`, `str/nested/firstImageURL`, `spotifyDelete`, `isNoActiveDevice`, `ensureActiveDevice`, `withDeviceRecovery`, tool structs `SpotifySeekTool`/`SpotifySaveTrackTool`/`SpotifyTransferTool` — used identically across tasks.
- **No placeholders.** HTTP/OAuth glue references exact existing patterns (`spotify_tools.go:34-55` current-track decode; `SpotifyPauseTool` shape). Network-dependent paths (live seek retry, real save) are manual-verify by design; pure logic is unit-tested.
