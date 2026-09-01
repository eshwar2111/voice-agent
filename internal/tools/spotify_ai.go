package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/auth"
	"github.com/yourname/voice-agent/internal/llm"
	"github.com/yourname/voice-agent/internal/ui"
)

// ───────────────────────────────────────────────────────────
// AI-Augmented Spotify Tools
// ───────────────────────────────────────────

type SpotifyAICuratTool struct {
	Cfg      *config.Config
	Provider llm.Provider
}

func (t *SpotifyAICuratTool) Name() string { return "spotify_ai_curate" }
func (t *SpotifyAICuratTool) Description() string { return "AI-powered Spotify playlist creation. Uses an LLM to analyze a mood/theme and generate a curated list of songs that perfectly match. Can create new playlists or enhance existing ones." }
func (t *SpotifyAICuratTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"action": {"type": "string", "description": "Action: 'create' (AI-curated playlist), 'enhance' (AI adds to existing playlist), 'remix' (AI suggests improvements)"},
			"theme": {"type": "string", "description": "Mood/genre/activity theme (e.g., 'coding focus', 'chill evening vibes', 'workout energy')"},
			"playlist_id": {"type": "string", "description": "Existing playlist ID for 'enhance'/'remix' actions"},
			"num_songs": {"type": "integer", "description": "Number of songs to add (default 15)"}
		},
		"required": ["action", "theme"]
	}`
}
func (t *SpotifyAICuratTool) RequiresConfirmation() bool { return true }

func (t *SpotifyAICuratTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Action     string `json:"action"`
		Theme      string `json:"theme"`
		PlaylistID string `json:"playlist_id"`
		NumSongs   int    `json:"num_songs"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}

	if args.NumSongs <= 0 {
		args.NumSongs = 15
	}

	ui.ShowNotification("🤖 AI is curating your playlist...")

	// Step 1: LLM generates track list based on theme
	prompt := fmt.Sprintf(`You are a world-class music curator. Create a playlist with the theme: "%s"

CRITICAL: You MUST return EXACTLY a JSON array of %d tracks in this format:
[{"name": "Song Title", "artist": "Artist Name"}]

Rules:
- Return ONLY the JSON array, no other text
- Only include real, well-known songs that actually exist
- Mix popular hits with hidden gems
- Ensure variety in tempo and style while maintaining the theme
- Include diverse artists
- No repeats

Theme analysis: Why do these songs fit "%s"?`, args.Theme, args.NumSongs, args.Theme)

	response, err := t.Provider.Generate(ctx, prompt, nil)
	if err != nil {
		return "", fmt.Errorf("AI curation failed: %w", err)
	}

	jsonStr := extractJSON(response)
	if jsonStr == "" {
		return ToolResult{Summary: "AI couldn't generate a valid track list. Try a different theme."}.String(), nil
	}

	var tracks []struct {
		Name   string `json:"name"`
		Artist string `json:"artist"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &tracks); err != nil {
		return ToolResult{Summary: "AI returned invalid format. Try a different theme."}.String(), nil
	}

	client, err := auth.GetSpotifyClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	// Step 2: Find tracks on Spotify
	var uris []string
	var matched []string
	ui.ShowNotification("🔍 Finding tracks on Spotify...")

	for _, track := range tracks {
		q := url.QueryEscape(fmt.Sprintf("track:%s artist:%s", track.Name, track.Artist))
		searchURI := fmt.Sprintf("/search?q=%s&type=track&limit=1", q)
		body, err := spotifyGet(ctx, client, searchURI)
		if err != nil || body == nil {
			continue
		}
		var result map[string]interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			continue
		}
		items := arrField(asMap(result["tracks"]), "items")
		if len(items) == 0 {
			continue
		}
		if uri := str(asMap(items[0]), "uri"); uri != "" {
			uris = append(uris, uri)
			matched = append(matched, track.Name+" — "+track.Artist)
		}
	}

	if len(uris) == 0 {
		return ToolResult{Summary: "⚠️ AI curated tracks but none were found on Spotify. Try a more mainstream theme."}.String(), nil
	}

	switch args.Action {
	case "create":
		// Create a new playlist
		playlistName := fmt.Sprintf("AI Curated: %s", args.Theme)
		payload, _ := json.Marshal(map[string]interface{}{
			"name":        playlistName,
			"description": fmt.Sprintf("AI-curated playlist for theme: %s. %d tracks matched to %d from LLM recommendation.", args.Theme, len(uris), args.NumSongs),
			"public":      false,
		})
		body, err := spotifyPost(ctx, client, "/me/playlists", payload)
		if err != nil {
			return "", fmt.Errorf("failed to create playlist: %w", err)
		}

		var playlist struct {
			ID  string `json:"id"`
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(body, &playlist); err != nil || playlist.ID == "" {
			return "", fmt.Errorf("failed to parse created playlist response")
		}

		// Add tracks
		trackPayload, _ := json.Marshal(map[string]interface{}{"uris": uris})
		tracksEndpoint := fmt.Sprintf("/playlists/%s/tracks", playlist.ID)
		_, err = spotifyPost(ctx, client, tracksEndpoint, trackPayload)
		if err != nil {
			return "", fmt.Errorf("failed to add tracks: %w", err)
		}

		// Start playing
		playPayload, _ := json.Marshal(map[string]interface{}{"context_uri": playlist.URI})
		_, playErr := spotifyPut(ctx, client, "/me/player/play", playPayload)

		result := fmt.Sprintf("🤖 AI Playlist Created\nTheme: %s\nMatched: %d/%d AI suggestions\n\nNow playing:\n", args.Theme, len(matched), args.NumSongs)
		if playErr != nil {
			result = fmt.Sprintf("🤖 AI Playlist Created (playback couldn't start — no active device?)\nTheme: %s\nMatched: %d/%d AI suggestions\n\nTracks:\n", args.Theme, len(matched), args.NumSongs)
		}
		for i, name := range matched {
			result += fmt.Sprintf("%d. %s\n", i+1, name)
		}
		return ToolResult{
			Summary: result,
			Artifacts: map[string]interface{}{
				"type":        "playlist_create",
				"theme":       args.Theme,
				"playlist_id": playlist.ID,
			},
		}.String(), nil

	case "enhance":
		if args.PlaylistID == "" {
			return "", fmt.Errorf("playlist_id required for 'enhance' action")
		}
		// Add the AI-curated tracks to existing playlist
		trackPayload, _ := json.Marshal(map[string]interface{}{"uris": uris})
		tracksEndpoint := fmt.Sprintf("/playlists/%s/tracks", args.PlaylistID)
		_, err = spotifyPost(ctx, client, tracksEndpoint, trackPayload)
		if err != nil {
			return "", fmt.Errorf("failed to add tracks to playlist: %w", err)
		}
		result := fmt.Sprintf("✅ Added %d AI-curated tracks to your playlist\nTheme: %s\nTracks added:\n", len(matched), args.Theme)
		for i, name := range matched {
			result += fmt.Sprintf("%d. %s\n", i+1, name)
		}
		return ToolResult{
			Summary: result,
			Artifacts: map[string]interface{}{
				"type":        "playlist_enhance",
				"theme":       args.Theme,
				"playlist_id": args.PlaylistID,
				"added":       len(matched),
			},
		}.String(), nil

	case "remix":
		if args.PlaylistID == "" {
			return "", fmt.Errorf("playlist_id required for 'remix' action")
		}
		// Get current playlist tracks
		endpoint := fmt.Sprintf("/playlists/%s/tracks?limit=50", args.PlaylistID)
		body, err := spotifyGet(ctx, client, endpoint)
		if err != nil {
			return "", fmt.Errorf("failed to get current tracks: %w", err)
		}

		var plData map[string]interface{}
		if err := json.Unmarshal(body, &plData); err != nil {
			return "", fmt.Errorf("failed to parse playlist tracks: %w", err)
		}
		var currentTracks []string
		for _, item := range arrField(plData, "items") {
			entry := asMap(item)
			track := asMap(entry["track"])
			if track == nil {
				// "track" can legitimately be null (e.g. a removed/unavailable item).
				continue
			}
			name := str(track, "name")
			if name == "" {
				continue
			}
			names := artistNames(arrField(track, "artists"))
			if len(names) > 0 {
				currentTracks = append(currentTracks, name+" — "+strings.Join(names, ", "))
			} else {
				currentTracks = append(currentTracks, name)
			}
		}

		if len(currentTracks) == 0 {
			return ToolResult{Summary: "This playlist has no readable tracks to remix."}.String(), nil
		}

		currentList := strings.Join(currentTracks, "\n")
		remixPrompt := fmt.Sprintf(`You are a music expert analyzing a Spotify playlist with theme: "%s"

Current tracks:
%s

Suggest 5-10 improvements:
1. Which songs don't fit the theme? List 2-3 to REMOVE.
2. What songs should be ADDED instead? List 5-7 specific songs.

Return ONLY JSON:
{"analysis": "Overall assessment of playlist quality and theme fit", "remove": ["song 1", "song 2"], "add": [{"name": "New Song", "artist": "Artist"}]}`, args.Theme, currentList)

		remixResponse, err := t.Provider.Generate(ctx, remixPrompt, nil)
		return ToolResult{Summary: fmt.Sprintf("Playlist Remix Analysis:\n%s", remixResponse)}.String(), nil

	default:
		return ToolResult{Summary: fmt.Sprintf("Unknown action: %s. Use 'create', 'enhance', or 'remix'.", args.Action)}.String(), nil
	}
}

type SpotifySmartRecommendTool struct {
	Cfg      *config.Config
	Provider llm.Provider
}

func (t *SpotifySmartRecommendTool) Name() string { return "spotify_smart_recommend" }
func (t *SpotifySmartRecommendTool) Description() string { return "AI-powered song recommendations based on what you're currently playing or a seed track. Analyzes the current song and recommends similar tracks, then optionally plays them." }
func (t *SpotifySmartRecommendTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"action": {"type": "string", "description": "'recommend' (just show suggestions), 'play' (recommendations and play)'"},
			"seed": {"type": "string", "description": "Optional: a song/artist to base recommendations on. If empty, uses currently playing track."}
		},
		"required": ["action"]
	}`
}
func (t *SpotifySmartRecommendTool) RequiresConfirmation() bool { return false }

func (t *SpotifySmartRecommendTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Action string `json:"action"`
		Seed   string `json:"seed"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}

	client, err := auth.GetSpotifyClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	// Determine seed track
	seedText := args.Seed
	if seedText == "" {
		body, err := spotifyGet(ctx, client, "/me/player/currently-playing")
		if err != nil || len(body) == 0 {
			// Spotify returns 204 with an empty body when nothing is playing.
			return "No track is currently playing. Provide a seed with 'seed' parameter.", nil
		}
		var resp struct {
			Item struct {
				Name    string `json:"name"`
				Artists []struct{ Name string } `json:"artists"`
			} `json:"item"`
		}
		if err := json.Unmarshal(body, &resp); err != nil || resp.Item.Name == "" {
			return "Nothing is currently playing to seed from. Provide a seed with 'seed' parameter.", nil
		}
		artistName := "an unknown artist"
		if len(resp.Item.Artists) > 0 {
			artistName = resp.Item.Artists[0].Name
		}
		seedText = resp.Item.Name + " by " + artistName
	}

	// Ask LLM for recommendations
	prompt := fmt.Sprintf(`You are a music expert. Someone is listening to: "%s"

Recommend 10 songs that they would likely enjoy next.

CRITICAL: Return ONLY a JSON array in this format:
[{"name": "Song Title", "artist": "Artist Name", "reason": "Why it fits"}]

Rules:
- Return ONLY the JSON array
- Mix similar artists, similar genres, and unexpected but fitting tracks
- Each recommendation should have a clear reason why it fits the seed`, seedText)

	response, err := t.Provider.Generate(ctx, prompt, nil)
	if err != nil {
		return "", fmt.Errorf("AI recommendation failed: %w", err)
	}

	jsonStr := extractJSON(response)
	if jsonStr == "" {
		return ToolResult{Summary: fmt.Sprintf("AI couldn't generate recommendations for: %s", seedText)}.String(), nil
	}

	var recs []struct {
		Name   string `json:"name"`
		Artist string `json:"artist"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &recs); err != nil {
		return ToolResult{Summary: "AI returned invalid recommendations"}.String(), nil
	}

	// Find on Spotify
	var uris []string
	var matched []string
	for _, rec := range recs {
		q := url.QueryEscape(fmt.Sprintf("track:%s artist:%s", rec.Name, rec.Artist))
		searchURI := fmt.Sprintf("/search?q=%s&type=track&limit=1", q)
		body, err := spotifyGet(ctx, client, searchURI)
		if err != nil || body == nil {
			continue
		}
		var result map[string]interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			continue
		}
		items := arrField(asMap(result["tracks"]), "items")
		if len(items) == 0 {
			continue
		}
		if uri := str(asMap(items[0]), "uri"); uri != "" {
			uris = append(uris, uri)
			matched = append(matched, fmt.Sprintf("%s by %s — %s", rec.Name, rec.Artist, rec.Reason))
		}
	}

	if args.Action == "play" && len(uris) > 0 {
		// Create a smart session playlist
		payload, _ := json.Marshal(map[string]interface{}{
			"name":        fmt.Sprintf("AI Recommendations: %s", seedText),
			"description": fmt.Sprintf("AI-powered recommendations based on '%s'", seedText),
			"public":      false,
		})
		body, err := spotifyPost(ctx, client, "/me/playlists", payload)
		if err != nil {
			return "", err
		}

		var playlist struct {
			ID  string `json:"id"`
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(body, &playlist); err != nil || playlist.ID == "" {
			return "", fmt.Errorf("failed to parse created playlist response")
		}

		trackPayload, _ := json.Marshal(map[string]interface{}{"uris": uris})
		tracksEndpoint := fmt.Sprintf("/playlists/%s/tracks", playlist.ID)
		_, err = spotifyPost(ctx, client, tracksEndpoint, trackPayload)
		if err != nil {
			return "", err
		}

		playPayload, _ := json.Marshal(map[string]interface{}{"context_uri": playlist.URI})
		_, playErr := spotifyPut(ctx, client, "/me/player/play", playPayload)

		header := "🎵 Smart Recommendations Created & Playing"
		trailer := "Now playing:\n"
		if playErr != nil {
			header = "🎵 Smart Recommendations Created (playback couldn't start — no active device?)"
			trailer = "Tracks:\n"
		}

		return ToolResult{
			Summary: fmt.Sprintf("%s\nBased on: %s\nMatched: %d/%d suggestions\n\n%s", header, seedText, len(matched), len(recs), trailer),
			Artifacts: map[string]interface{}{
				"type":        "smart_recommend_play",
				"seed":        seedText,
				"playlist_id": playlist.ID,
			},
		}.String(), nil
	}

	result := fmt.Sprintf("AI Recommendations based on: %s\n\n", seedText)
	for i, m := range matched {
		result += fmt.Sprintf("%d. %s\n", i+1, m)
	}

	return ToolResult{
		Summary: result,
		Artifacts: map[string]interface{}{
			"type": "smart_recommend",
			"seed": seedText,
			"data": matched,
		},
	}.String(), nil
}

func extractJSON(text string) string {
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start != -1 && end > start {
		return text[start : end+1]
	}
	start = strings.Index(text, "{")
	end = strings.LastIndex(text, "}")
	if start != -1 && end > start {
		return text[start : end+1]
	}
	return ""
}
