package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/auth"
)

const spotifyBase = auth.SpotifyAPIBase

type SpotifyNowPlayingTool struct {
	Cfg *config.Config
}

func (t *SpotifyNowPlayingTool) Name() string { return "spotify_now_playing" }
func (t *SpotifyNowPlayingTool) Description() string { return "Get the currently playing track on Spotify with full metadata." }
func (t *SpotifyNowPlayingTool) Parameters() string { return `{"type": "object", "properties": {}, "required": []}` }
func (t *SpotifyNowPlayingTool) RequiresConfirmation() bool { return false }

func (t *SpotifyNowPlayingTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	client, err := auth.GetSpotifyClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	body, err := spotifyGet(ctx, client, "/me/player/currently-playing")
	if err != nil {
		return "", err
	}
	if body == nil {
		return "No track is currently playing.", nil
	}

	var resp struct {
		Item struct {
			Name     string `json:"name"`
			Duration int    `json:"duration_ms"`
			Artists  []struct {
				Name string `json:"name"`
			} `json:"artists"`
			Album struct {
				Name    string `json:"name"`
				Images  []struct{ URL string } `json:"images"`
			} `json:"album"`
		} `json:"item"`
		IsPlaying     bool   `json:"is_playing"`
		ProgressMS    int    `json:"progress_ms"`
		Device        struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"device"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parse error: %w", err)
	}

	if resp.Item.Name == "" {
		return "No track is currently playing.", nil
	}

	artists := make([]string, len(resp.Item.Artists))
	for i, a := range resp.Item.Artists {
		artists[i] = a.Name
	}

	progress := resp.ProgressMS / 1000
	duration := resp.Item.Duration / 1000

	result := fmt.Sprintf("🎵 Now Playing\n")
	result += fmt.Sprintf("Title: %s\n", resp.Item.Name)
	result += fmt.Sprintf("Artist: %s\n", strings.Join(artists, ", "))
	result += fmt.Sprintf("Album: %s\n", resp.Item.Album.Name)
	result += fmt.Sprintf("Device: %s (%s)\n", resp.Device.Name, resp.Device.Type)
	result += fmt.Sprintf("Progress: %d:%02d / %d:%02d\n", progress/60, progress%60, duration/60, duration%60)

	return ToolResult{
		Summary: result,
		Artifacts: map[string]interface{}{
			"type": "now_playing",
			"data": resp,
		},
	}.String(), nil
}

type SpotifyPlayTool struct {
	Cfg *config.Config
}

func (t *SpotifyPlayTool) Name() string { return "spotify_play" }
func (t *SpotifyPlayTool) Description() string { return "Play music on Spotify. Resume playback, or play a specific track/artist/album/playlist by name. The AI will search for the best match and play it." }
func (t *SpotifyPlayTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"query": {"type": "string", "description": "What to play - track name, artist, album, or playlist name. If empty, just resumes playback."},
			"type": {"type": "string", "description": "Type of content: 'track', 'artist', 'album', 'playlist'. Default: auto-detect from query."}
		},
		"required": []
	}`
}
func (t *SpotifyPlayTool) RequiresConfirmation() bool { return false }

func (t *SpotifyPlayTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Query string `json:"query"`
		Type  string `json:"type"`
	}
	json.Unmarshal(params, &args)

	client, err := auth.GetSpotifyClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	// If no query, just resume playback
	if args.Query == "" {
		_, err = spotifyPut(ctx, client, "/me/player/play", []byte("{}"))
		if err != nil {
			return "", fmt.Errorf("failed to resume playback: %w", err)
		}
		return "▶️ Resumed Spotify playback", nil
	}

	// Determine search type if not specified
	searchType := args.Type
	if searchType == "" {
		if strings.Contains(strings.ToLower(args.Query), "playlist") {
			searchType = "playlist"
		} else if strings.Contains(strings.ToLower(args.Query), "album") {
			searchType = "album"
		} else {
			searchType = "track"
		}
	}

	// Search for the content
	q := url.QueryEscape(args.Query)
	uri := fmt.Sprintf("/search?q=%s&type=%s&limit=1", q, searchType)
	body, err := spotifyGet(ctx, client, uri)
	if err != nil || body == nil {
		return fmt.Sprintf("Could not find '%s' on Spotify", args.Query), nil
	}

	var searchResult map[string]interface{}
	json.Unmarshal(body, &searchResult)

	// Extract the URN from the first result based on type
	switch searchType {
	case "track":
		if tracks, ok := searchResult["tracks"].(map[string]interface{}); ok {
			if items, ok := tracks["items"].([]interface{}); ok && len(items) > 0 {
				track := items[0].(map[string]interface{})
				name := track["name"].(string)
				uri := track["uri"].(string)
				return t.playURI(ctx, client, uri, "Playing: "+name)
			}
		}
	case "artist":
		if artists, ok := searchResult["artists"].(map[string]interface{}); ok {
			if items, ok := artists["items"].([]interface{}); ok && len(items) > 0 {
				artist := items[0].(map[string]interface{})
				name := artist["name"].(string)
				uri := artist["uri"].(string)
				return t.playURI(ctx, client, uri, "Playing artist: "+name)
			}
		}
	case "album":
		if albums, ok := searchResult["albums"].(map[string]interface{}); ok {
			if items, ok := albums["items"].([]interface{}); ok && len(items) > 0 {
				album := items[0].(map[string]interface{})
				name := album["name"].(string)
				uri := album["uri"].(string)
				return t.playURI(ctx, client, uri, "Playing album: "+name)
			}
		}
	case "playlist":
		if playlists, ok := searchResult["playlists"].(map[string]interface{}); ok {
			if items, ok := playlists["items"].([]interface{}); ok && len(items) > 0 {
				pl := items[0].(map[string]interface{})
				name := pl["name"].(string)
				uri := pl["uri"].(string)
				return t.playURI(ctx, client, uri, "Playing playlist: "+name)
			}
		}
	}

	return fmt.Sprintf("Could not find '%s' on Spotify", args.Query), nil
}

func (t *SpotifyPlayTool) playURI(ctx context.Context, client *http.Client, uri string, msg string) (string, error) {
	payload, _ := json.Marshal(map[string]interface{}{"context_uri": uri})
	_, err := spotifyPut(ctx, client, "/me/player/play", payload)
	if err != nil {
		return "", fmt.Errorf("failed to play: %w", err)
	}
	return ToolResult{
		Summary: fmt.Sprintf("✅ %s", msg),
		Artifacts: map[string]interface{}{
			"type": "spotify_play",
			"uri":  uri,
		},
	}.String(), nil
}

type SpotifyPauseTool struct {
	Cfg *config.Config
}

func (t *SpotifyPauseTool) Name() string { return "spotify_pause" }
func (t *SpotifyPauseTool) Description() string { return "Pause Spotify playback." }
func (t *SpotifyPauseTool) Parameters() string  { return `{"type": "object", "properties": {}, "required": []}` }
func (t *SpotifyPauseTool) RequiresConfirmation() bool { return false }

func (t *SpotifyPauseTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	client, err := auth.GetSpotifyClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	_, err = spotifyPut(ctx, client, "/me/player/pause", []byte("{}"))
	if err != nil {
		return "", fmt.Errorf("failed to pause: %w", err)
	}

	return ToolResult{Summary: "⏸️ Paused Spotify playback"}.String(), nil
}

type SpotifyNextTool struct {
	Cfg *config.Config
}

func (t *SpotifyNextTool) Name() string { return "spotify_next" }
func (t *SpotifyNextTool) Description() string { return "Skip to the next track on Spotify." }
func (t *SpotifyNextTool) Parameters() string  { return `{"type": "object", "properties": {}, "required": []}` }
func (t *SpotifyNextTool) RequiresConfirmation() bool { return false }

func (t *SpotifyNextTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	client, err := auth.GetSpotifyClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	_, err = spotifyPost(ctx, client, "/me/player/next", nil)
	if err != nil {
		return "", fmt.Errorf("failed to skip: %w", err)
	}

	return ToolResult{Summary: "⏭️ Skipped to next track"}.String(), nil
}

type SpotifyPreviousTool struct {
	Cfg *config.Config
}

func (t *SpotifyPreviousTool) Name() string { return "spotify_previous" }
func (t *SpotifyPreviousTool) Description() string { return "Skip to the previous track on Spotify." }
func (t *SpotifyPreviousTool) Parameters() string  { return `{"type": "object", "properties": {}, "required": []}` }
func (t *SpotifyPreviousTool) RequiresConfirmation() bool { return false }

func (t *SpotifyPreviousTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	client, err := auth.GetSpotifyClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	_, err = spotifyPost(ctx, client, "/me/player/previous", nil)
	if err != nil {
		return "", fmt.Errorf("failed to go to previous track: %w", err)
	}

	return ToolResult{Summary: "⏮️ Skipped to previous track"}.String(), nil
}

type SpotifyVolumeTool struct {
	Cfg *config.Config
}

func (t *SpotifyVolumeTool) Name() string { return "spotify_volume" }
func (t *SpotifyVolumeTool) Description() string { return "Set the Spotify playback volume (0-100)." }
func (t *SpotifyVolumeTool) Parameters() string {
	return `{"type": "object", "properties": {"volume_percent": {"type": "integer", "description": "Volume level from 0 to 100"}}, "required": ["volume_percent"]}`
}
func (t *SpotifyVolumeTool) RequiresConfirmation() bool { return false }

func (t *SpotifyVolumeTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Volume int `json:"volume_percent"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}

	client, err := auth.GetSpotifyClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	_, err = spotifyPut(ctx, client, fmt.Sprintf("/me/player/volume?volume_percent=%d", args.Volume), nil)
	if err != nil {
		return "", fmt.Errorf("failed to set volume: %w", err)
	}

	return ToolResult{Summary: fmt.Sprintf("🔊 Volume set to %d%%", args.Volume)}.String(), nil
}

type SpotifySearchTool struct {
	Cfg *config.Config
}

func (t *SpotifySearchTool) Name() string { return "spotify_search" }
func (t *SpotifySearchTool) Description() string { return "Search Spotify for tracks, artists, albums, or playlists." }
func (t *SpotifySearchTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"query": {"type": "string", "description": "Search query"},
			"type": {"type": "string", "description": "Type: track, artist, album, playlist (default: track)"},
			"limit": {"type": "integer", "description": "Number of results (default 5)"}
		},
		"required": ["query"]
	}`
}
func (t *SpotifySearchTool) RequiresConfirmation() bool { return false }

func (t *SpotifySearchTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Query string `json:"query"`
		Type  string `json:"type"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}
	if args.Type == "" {
		args.Type = "track"
	}
	if args.Limit <= 0 {
		args.Limit = 5
	}

	client, err := auth.GetSpotifyClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	query := url.QueryEscape(args.Query)
	uri := fmt.Sprintf("/search?q=%s&type=%s&limit=%d", query, args.Type, args.Limit)

	body, err := spotifyGet(ctx, client, uri)
	if err != nil {
		return "", err
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("🎵 Spotify %s results for '%s':\n\n", args.Type, args.Query))

	var data map[string]interface{}
	json.Unmarshal(body, &data)

	var choices []AmbiguousChoice
	var firstURI string

	switch args.Type {
	case "track":
		if tracks, ok := data["tracks"].(map[string]interface{}); ok {
			if items, ok := tracks["items"].([]interface{}); ok {
				for i, item := range items {
					track := item.(map[string]interface{})
					uri := track["uri"].(string)
					artists := track["artists"].([]interface{})
					var names []string
					for _, a := range artists {
						names = append(names, a.(map[string]interface{})["name"].(string))
					}
					label := fmt.Sprintf("%s — %s", track["name"], strings.Join(names, ", "))
					result.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, label))
					choices = append(choices, AmbiguousChoice{ID: uri, Label: label})
					if i == 0 {
						firstURI = uri
					}
				}
			}
		}
	case "artist":
		if artists, ok := data["artists"].(map[string]interface{}); ok {
			if items, ok := artists["items"].([]interface{}); ok {
				for i, item := range items {
					artist := item.(map[string]interface{})
					uri := artist["uri"].(string)
					followers := artist["followers"].(map[string]interface{})
					label := fmt.Sprintf("%s (followers: %.0f)", artist["name"], followers["total"].(float64))
					result.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, label))
					choices = append(choices, AmbiguousChoice{ID: uri, Label: label})
					if i == 0 {
						firstURI = uri
					}
				}
			}
		}
	case "album":
		if albums, ok := data["albums"].(map[string]interface{}); ok {
			if items, ok := albums["items"].([]interface{}); ok {
				for i, item := range items {
					album := item.(map[string]interface{})
					uri := album["uri"].(string)
					var names []string
					for _, a := range album["artists"].([]interface{}) {
						names = append(names, a.(map[string]interface{})["name"].(string))
					}
					label := fmt.Sprintf("%s — %s (%s)", album["name"], strings.Join(names, ", "), album["release_date"])
					result.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, label))
					choices = append(choices, AmbiguousChoice{ID: uri, Label: label})
					if i == 0 {
						firstURI = uri
					}
				}
			}
		}
	case "playlist":
		if playlists, ok := data["playlists"].(map[string]interface{}); ok {
			if items, ok := playlists["items"].([]interface{}); ok {
				for i, item := range items {
					pl := item.(map[string]interface{})
					uri := pl["uri"].(string)
					label := fmt.Sprintf("%s — %s (%.0f tracks)", pl["name"], pl["owner"].(map[string]interface{})["display_name"].(string), pl["tracks"].(map[string]interface{})["total"].(float64))
					result.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, label))
					choices = append(choices, AmbiguousChoice{ID: uri, Label: label})
					if i == 0 {
						firstURI = uri
					}
				}
			}
		}
	}

	if len(choices) > 1 {
		return AmbiguousResult(fmt.Sprintf("I found %d %s results for '%s'. Which one did you mean?", len(choices), args.Type, args.Query), choices).String(), nil
	}

	return ToolResult{
		Summary: result.String(),
		Artifacts: map[string]interface{}{
			"type":      "spotify_search",
			"query":     args.Query,
			"track_uri": firstURI,
			"uri":       firstURI,
		},
	}.String(), nil
}

type SpotifyPlaylistsTool struct {
	Cfg *config.Config
}

func (t *SpotifyPlaylistsTool) Name() string { return "spotify_playlists" }
func (t *SpotifyPlaylistsTool) Description() string { return "List the user's Spotify playlists." }
func (t *SpotifyPlaylistsTool) Parameters() string {
	return `{"type": "object", "properties": {"limit": {"type": "integer", "description": "Number of playlists to return (default 20)"}}, "required": []}`
}
func (t *SpotifyPlaylistsTool) RequiresConfirmation() bool { return false }

func (t *SpotifyPlaylistsTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Limit int `json:"limit"`
	}
	json.Unmarshal(params, &args)
	if args.Limit <= 0 {
		args.Limit = 20
	}

	client, err := auth.GetSpotifyClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	body, err := spotifyGet(ctx, client, fmt.Sprintf("/me/playlists?limit=%d", args.Limit))
	if err != nil {
		return "", err
	}

	var resp struct {
		Items []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Tracks      struct {
				Total int `json:"total"`
			} `json:"tracks"`
			ID   string `json:"id"`
			URI  string `json:"uri"`
		} `json:"items"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parse error: %w", err)
	}

	if len(resp.Items) == 0 {
		return "No playlists found.", nil
	}

	var result strings.Builder
	result.WriteString("Your Spotify Playlists:\n\n")
	for i, pl := range resp.Items {
		result.WriteString(fmt.Sprintf("%d. **%s** (%d tracks)\n", i+1, pl.Name, pl.Tracks.Total))
		if pl.Description != "" {
			result.WriteString(fmt.Sprintf("   %s\n", pl.Description))
		}
	}

	return ToolResult{
		Summary:   result.String(),
		Artifacts: map[string]interface{}{
			"type": "spotify_playlists",
			"data": resp.Items,
		},
	}.String(), nil
}

type SpotifyDeviceTool struct {
	Cfg *config.Config
}

func (t *SpotifyDeviceTool) Name() string { return "spotify_devices" }
func (t *SpotifyDeviceTool) Description() string { return "List available Spotify playback devices." }
func (t *SpotifyDeviceTool) Parameters() string  { return `{"type": "object", "properties": {}, "required": []}` }
func (t *SpotifyDeviceTool) RequiresConfirmation() bool { return false }

func (t *SpotifyDeviceTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	client, err := auth.GetSpotifyClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	body, err := spotifyGet(ctx, client, "/me/player/devices")
	if err != nil {
		return "", err
	}

	var resp struct {
		Devices []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Type     string `json:"type"`
			IsActive bool   `json:"is_active"`
		} `json:"devices"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parse error: %w", err)
	}

	if len(resp.Devices) == 0 {
		return "No Spotify playback devices found.", nil
	}

	var result strings.Builder
	result.WriteString("Spotify Devices:\n\n")
	for _, d := range resp.Devices {
		active := ""
		if d.IsActive {
			active = " (active)"
		}
		result.WriteString(fmt.Sprintf("- %s (%s)%s [ID: %s]\n", d.Name, d.Type, active, d.ID))
	}

	return result.String(), nil
}

type SpotifyQueueTool struct {
	Cfg *config.Config
}

func (t *SpotifyQueueTool) Name() string { return "spotify_queue" }
func (t *SpotifyQueueTool) Description() string { return "View queue, add a track, or toggle shuffle/repeat." }
func (t *SpotifyQueueTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"action": {"type": "string", "description": "Action: 'view', 'add', 'shuffle', or 'repeat'. Default: 'view'."},
			"uri": {"type": "string", "description": "Spotify URI to add (action='add')"},
			"state": {"type": "string", "description": "State: 'on'/'off' for shuffle, 'context'/'track'/'off' for repeat."}
		},
		"required": []
	}`
}
func (t *SpotifyQueueTool) RequiresConfirmation() bool { return false }

func (t *SpotifyQueueTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Action string `json:"action"`
		URI    string `json:"uri"`
		State  string `json:"state"`
	}
	json.Unmarshal(params, &args)
	if args.Action == "" {
		args.Action = "view"
	}

	client, err := auth.GetSpotifyClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	switch args.Action {
	case "view":
		body, err := spotifyGet(ctx, client, "/me/player/queue")
		if err != nil {
			return "", err
		}

		var resp struct {
			Queue []struct {
				Name    string `json:"name"`
				Artists []struct{ Name string } `json:"artists"`
				URI     string `json:"uri"`
			} `json:"queue"`
			CurrentlyPlaying struct {
				Name    string `json:"name"`
				Artists []struct{ Name string } `json:"artists"`
			} `json:"currently_playing"`
		}

		if err := json.Unmarshal(body, &resp); err != nil {
			return "", fmt.Errorf("parse error: %w", err)
		}

		var result strings.Builder
		result.WriteString("Spotify Queue:\n\n")
		if resp.CurrentlyPlaying.Name != "" {
			artists := make([]string, len(resp.CurrentlyPlaying.Artists))
			for i, a := range resp.CurrentlyPlaying.Artists {
				artists[i] = a.Name
			}
			result.WriteString(fmt.Sprintf("▶ Now Playing: %s by %s\n\n", resp.CurrentlyPlaying.Name, strings.Join(artists, ", ")))
		}

		if len(resp.Queue) == 0 {
			result.WriteString("Queue is empty.")
		} else {
			result.WriteString("Up next:\n")
			for i, track := range resp.Queue {
				artists := make([]string, len(track.Artists))
				for j, a := range track.Artists {
					artists[j] = a.Name
				}
				result.WriteString(fmt.Sprintf("%d. %s by %s\n", i+1, track.Name, strings.Join(artists, ", ")))
			}
		}

		return ToolResult{
			Summary: result.String(),
			Artifacts: map[string]interface{}{
				"type": "spotify_queue",
				"data": resp,
			},
		}.String(), nil

	case "add":
		if args.URI == "" {
			return "", fmt.Errorf("uri is required to add a track to the queue")
		}
		_, err = spotifyPost(ctx, client, fmt.Sprintf("/me/player/queue?uri=%s", url.QueryEscape(args.URI)), nil)
		if err != nil {
			return "", fmt.Errorf("failed to add to queue: %w", err)
		}
		return ToolResult{
			Summary: fmt.Sprintf("✅ Added %s to queue", args.URI),
			Artifacts: map[string]interface{}{
				"type": "spotify_queue_add",
				"uri":  args.URI,
			},
		}.String(), nil

	case "shuffle":
		state := "true"
		if args.State == "off" || args.State == "false" {
			state = "false"
		}
		_, err := spotifyPut(ctx, client, fmt.Sprintf("/me/player/shuffle?state=%s", state), nil)
		if err != nil {
			return "", fmt.Errorf("failed to toggle shuffle: %w", err)
		}
		return fmt.Sprintf("✅ Shuffle %s", args.State), nil

	case "repeat":
		state := "context"
		switch args.State {
		case "off", "false":
			state = "off"
		case "track":
			state = "track"
		case "context", "on", "true":
			state = "context"
		}
		_, err := spotifyPut(ctx, client, fmt.Sprintf("/me/player/repeat?state=%s", state), nil)
		if err != nil {
			return "", fmt.Errorf("failed to toggle repeat: %w", err)
		}
		return fmt.Sprintf("✅ Repeat mode: %s", state), nil
	}

	return fmt.Sprintf("Unknown action: %s. Supported: view, add, shuffle, repeat", args.Action), nil
}

type SpotifyAccountTool struct {
	Cfg *config.Config
}

func (t *SpotifyAccountTool) Name() string { return "spotify_account" }
func (t *SpotifyAccountTool) Description() string { return "Get connected Spotify account info." }
func (t *SpotifyAccountTool) Parameters() string  { return `{"type": "object", "properties": {}, "required": []}` }
func (t *SpotifyAccountTool) RequiresConfirmation() bool { return false }

func (t *SpotifyAccountTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	client, err := auth.GetSpotifyClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	body, err := spotifyGet(ctx, client, "/me")
	if err != nil {
		return "", err
	}

	var resp struct {
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
		Product     string `json:"product"`
		Country     string `json:"country"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parse error: %w", err)
	}

	result := fmt.Sprintf("Spotify Account: %s\n", resp.DisplayName)
	result += fmt.Sprintf("Email: %s\n", resp.Email)
	result += fmt.Sprintf("Plan: %s\n", resp.Product)
	result += fmt.Sprintf("Country: %s", resp.Country)
	return result, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// HTTP Helpers (shared across all Spotify tools)
// ───────────────────────────────────────────────────────────────────────

func spotifyGet(ctx context.Context, client *http.Client, path string) ([]byte, error) {
	resp, err := client.Get(spotifyBase + path)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Spotify error (%s): %s", resp.Status, string(body))
	}

	return io.ReadAll(resp.Body)
}

func spotifyPut(ctx context.Context, client *http.Client, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "PUT", spotifyBase+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create PUT request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("PUT failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyResp, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Spotify error (%s): %s", resp.Status, string(bodyResp))
	}

	return io.ReadAll(resp.Body)
}

func spotifyPost(ctx context.Context, client *http.Client, path string, body []byte) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", spotifyBase+path, reader)
	if err != nil {
		return nil, fmt.Errorf("failed to create POST request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode >= 400 {
		bodyResp, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Spotify error (%s): %s", resp.Status, string(bodyResp))
	}

	return io.ReadAll(resp.Body)
}
