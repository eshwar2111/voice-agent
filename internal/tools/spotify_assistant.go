package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/auth"
	"github.com/yourname/voice-agent/internal/llm"
)

type SpotifyAssistantTool struct {
	Cfg      *config.Config
	Provider llm.Provider
}

func (t *SpotifyAssistantTool) Name() string { return "spotify_assistant" }

func (t *SpotifyAssistantTool) Description() string {
	return "Specialized Spotify assistant that understands natural language for playback, queueing, recommendations, and AI-curated listening sessions."
}

func (t *SpotifyAssistantTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"request": {"type": "string", "description": "Natural language Spotify request"},
			"mode": {"type": "string", "description": "Optional mode override: status, play, pause, next, previous, search, recommend, curate, queue_add, volume"}
		},
		"required": ["request"]
	}`
}

func (t *SpotifyAssistantTool) RequiresConfirmation() bool { return false }

func (t *SpotifyAssistantTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Request string `json:"request"`
		Mode    string `json:"mode"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Request) == "" {
		return "", fmt.Errorf("request is required")
	}

	mode := strings.TrimSpace(strings.ToLower(args.Mode))
	query := args.Request
	if mode == "" {
		plannedMode, plannedQuery, err := t.planMode(ctx, args.Request)
		if err == nil {
			mode = plannedMode
			if plannedQuery != "" {
				query = plannedQuery
			}
		}
	}
	if mode == "" {
		mode = "status"
	}

	switch mode {
	case "status":
		return t.status(ctx)
	case "play":
		return (&SpotifyPlayTool{Cfg: t.Cfg}).Execute(ctx, mustJSON(map[string]any{"query": query}))
	case "pause":
		return (&SpotifyPauseTool{Cfg: t.Cfg}).Execute(ctx, mustJSON(map[string]any{}))
	case "next":
		return (&SpotifyNextTool{Cfg: t.Cfg}).Execute(ctx, mustJSON(map[string]any{}))
	case "previous":
		return (&SpotifyPreviousTool{Cfg: t.Cfg}).Execute(ctx, mustJSON(map[string]any{}))
	case "search":
		return (&SpotifySearchTool{Cfg: t.Cfg}).Execute(ctx, mustJSON(map[string]any{"query": query, "limit": 5}))
	case "recommend":
		tool := &SpotifySmartRecommendTool{Cfg: t.Cfg, Provider: t.Provider}
		return tool.Execute(ctx, mustJSON(map[string]any{"action": "recommend", "seed": query}))
	case "curate":
		tool := &SpotifyAICuratTool{Cfg: t.Cfg, Provider: t.Provider}
		return tool.Execute(ctx, mustJSON(map[string]any{"action": "create", "theme": query, "num_songs": 12}))
	case "queue_add":
		return t.queueAddBySearch(ctx, query)
	case "volume":
		volume := extractFirstInt(query)
		if volume < 0 {
			volume = 50
		}
		return (&SpotifyVolumeTool{Cfg: t.Cfg}).Execute(ctx, mustJSON(map[string]any{"volume_percent": volume}))
	default:
		return t.status(ctx)
	}
}

func (t *SpotifyAssistantTool) planMode(ctx context.Context, request string) (string, string, error) {
	prompt := fmt.Sprintf(`Classify this Spotify request into one mode and extract the best query if useful.

Request: %s

Allowed modes:
- status
- play
- pause
- next
- previous
- search
- recommend
- curate
- queue_add
- volume

Return ONLY JSON like {"mode":"play","query":"lofi coding music"}`, request)

	response, err := t.Provider.Generate(ctx, prompt, nil)
	if err != nil {
		return "", "", err
	}

	var planned struct {
		Mode  string `json:"mode"`
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(extractJSONText(response)), &planned); err != nil {
		return "", "", err
	}
	return strings.ToLower(planned.Mode), planned.Query, nil
}

func (t *SpotifyAssistantTool) status(ctx context.Context) (string, error) {
	client, err := auth.GetSpotifyClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	accountBody, _ := spotifyGet(ctx, client, "/me")
	nowBody, _ := spotifyGet(ctx, client, "/me/player/currently-playing")
	recentBody, _ := spotifyGet(ctx, client, "/me/player/recently-played?limit=5")

	var sb strings.Builder
	sb.WriteString("Spotify Assistant Brief\n\n")

	if len(accountBody) > 0 {
		var me struct {
			DisplayName string `json:"display_name"`
			Product     string `json:"product"`
			Country     string `json:"country"`
		}
		if json.Unmarshal(accountBody, &me) == nil {
			sb.WriteString(fmt.Sprintf("Account: %s (%s, %s)\n", me.DisplayName, me.Product, me.Country))
		}
	}

	if len(nowBody) > 0 {
		var now struct {
			Item struct {
				Name    string `json:"name"`
				Artists []struct {
					Name string `json:"name"`
				} `json:"artists"`
			} `json:"item"`
			IsPlaying bool `json:"is_playing"`
		}
		if json.Unmarshal(nowBody, &now) == nil && now.Item.Name != "" {
			artistNames := make([]string, 0, len(now.Item.Artists))
			for _, artist := range now.Item.Artists {
				artistNames = append(artistNames, artist.Name)
			}
			state := "paused"
			if now.IsPlaying {
				state = "playing"
			}
			sb.WriteString(fmt.Sprintf("Now %s: %s by %s\n", state, now.Item.Name, strings.Join(artistNames, ", ")))
		}
	}

	if len(recentBody) > 0 {
		var recent struct {
			Items []struct {
				Track struct {
					Name    string `json:"name"`
					Artists []struct {
						Name string `json:"name"`
					} `json:"artists"`
				} `json:"track"`
			} `json:"items"`
		}
		if json.Unmarshal(recentBody, &recent) == nil && len(recent.Items) > 0 {
			sb.WriteString("\nRecently Played:\n")
			for i, item := range recent.Items {
				if i >= 3 {
					break
				}
				artistNames := make([]string, 0, len(item.Track.Artists))
				for _, artist := range item.Track.Artists {
					artistNames = append(artistNames, artist.Name)
				}
				sb.WriteString(fmt.Sprintf("- %s by %s\n", item.Track.Name, strings.Join(artistNames, ", ")))
			}
		}
	}

	sb.WriteString("\nCapabilities: natural playback, AI recommendations, AI playlist curation, queue management")
	return sb.String(), nil
}

func (t *SpotifyAssistantTool) queueAddBySearch(ctx context.Context, query string) (string, error) {
	client, err := auth.GetSpotifyClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	body, err := spotifyGet(ctx, client, fmt.Sprintf("/search?q=%s&type=track&limit=1", url.QueryEscape(query)))
	if err != nil {
		return "", err
	}

	var result struct {
		Tracks struct {
			Items []struct {
				Name    string `json:"name"`
				URI     string `json:"uri"`
				Artists []struct {
					Name string `json:"name"`
				} `json:"artists"`
			} `json:"items"`
		} `json:"tracks"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if len(result.Tracks.Items) == 0 {
		return fmt.Sprintf("I couldn't find a track for '%s'.", query), nil
	}

	track := result.Tracks.Items[0]
	_, err = spotifyPost(ctx, client, fmt.Sprintf("/me/player/queue?uri=%s", url.QueryEscape(track.URI)), nil)
	if err != nil {
		return "", err
	}

	artists := make([]string, 0, len(track.Artists))
	for _, artist := range track.Artists {
		artists = append(artists, artist.Name)
	}

	return fmt.Sprintf("Added to queue: %s by %s", track.Name, strings.Join(artists, ", ")), nil
}

func extractFirstInt(text string) int {
	value := -1
	for _, field := range strings.FieldsFunc(text, func(r rune) bool {
		return r < '0' || r > '9'
	}) {
		if field == "" {
			continue
		}
		fmt.Sscanf(field, "%d", &value)
		if value >= 0 {
			return value
		}
	}
	return -1
}

func spotifyJSON(ctx context.Context, client *http.Client, endpoint string, v any) error {
	body, err := spotifyGet(ctx, client, endpoint)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}
