package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
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
		err = withDeviceRecovery(ctx, client, func() error {
			_, e := spotifyPut(ctx, client, "/me/player/play", []byte("{}"))
			return e
		})
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
		if items := arrField(nested(searchResult, "tracks"), "items"); len(items) > 0 {
			if track := asMap(items[0]); track != nil {
				return t.playURI(ctx, client, str(track, "uri"), "Playing: "+str(track, "name"))
			}
		}
	case "artist":
		if items := arrField(nested(searchResult, "artists"), "items"); len(items) > 0 {
			if artist := asMap(items[0]); artist != nil {
				return t.playURI(ctx, client, str(artist, "uri"), "Playing artist: "+str(artist, "name"))
			}
		}
	case "album":
		if items := arrField(nested(searchResult, "albums"), "items"); len(items) > 0 {
			if album := asMap(items[0]); album != nil {
				return t.playURI(ctx, client, str(album, "uri"), "Playing album: "+str(album, "name"))
			}
		}
	case "playlist":
		if items := arrField(nested(searchResult, "playlists"), "items"); len(items) > 0 {
			if pl := asMap(items[0]); pl != nil {
				return t.playURI(ctx, client, str(pl, "uri"), "Playing playlist: "+str(pl, "name"))
			}
		}
	}

	return fmt.Sprintf("Could not find '%s' on Spotify", args.Query), nil
}

func (t *SpotifyPlayTool) playURI(ctx context.Context, client *http.Client, uri string, msg string) (string, error) {
	payload, _ := json.Marshal(map[string]interface{}{"context_uri": uri})
	err := withDeviceRecovery(ctx, client, func() error {
		_, e := spotifyPut(ctx, client, "/me/player/play", payload)
		return e
	})
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

	err = withDeviceRecovery(ctx, client, func() error {
		_, e := spotifyPut(ctx, client, "/me/player/pause", []byte("{}"))
		return e
	})
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

	err = withDeviceRecovery(ctx, client, func() error {
		_, e := spotifyPost(ctx, client, "/me/player/next", nil)
		return e
	})
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

	err = withDeviceRecovery(ctx, client, func() error {
		_, e := spotifyPost(ctx, client, "/me/player/previous", nil)
		return e
	})
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

	err = withDeviceRecovery(ctx, client, func() error {
		_, e := spotifyPut(ctx, client, fmt.Sprintf("/me/player/volume?volume_percent=%d", args.Volume), nil)
		return e
	})
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

	// Field access uses safe accessors (str/asMap/arrField/numField): a Spotify
	// response with a null item or a missing owner/artists/name yields "" or is
	// skipped instead of panicking.
	switch args.Type {
	case "track":
		for i, item := range arrField(nested(data, "tracks"), "items") {
			track := asMap(item)
			if track == nil {
				continue
			}
			uri := str(track, "uri")
			label := fmt.Sprintf("%s — %s", str(track, "name"), strings.Join(artistNames(arrField(track, "artists")), ", "))
			result.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, label))
			choices = append(choices, AmbiguousChoice{ID: uri, Label: label})
			if firstURI == "" {
				firstURI = uri
			}
		}
	case "artist":
		for i, item := range arrField(nested(data, "artists"), "items") {
			artist := asMap(item)
			if artist == nil {
				continue
			}
			uri := str(artist, "uri")
			label := fmt.Sprintf("%s (followers: %.0f)", str(artist, "name"), numField(nested(artist, "followers"), "total"))
			result.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, label))
			choices = append(choices, AmbiguousChoice{ID: uri, Label: label})
			if firstURI == "" {
				firstURI = uri
			}
		}
	case "album":
		for i, item := range arrField(nested(data, "albums"), "items") {
			album := asMap(item)
			if album == nil {
				continue
			}
			uri := str(album, "uri")
			label := fmt.Sprintf("%s — %s (%s)", str(album, "name"), strings.Join(artistNames(arrField(album, "artists")), ", "), str(album, "release_date"))
			result.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, label))
			choices = append(choices, AmbiguousChoice{ID: uri, Label: label})
			if firstURI == "" {
				firstURI = uri
			}
		}
	case "playlist":
		for i, item := range arrField(nested(data, "playlists"), "items") {
			pl := asMap(item)
			if pl == nil {
				continue
			}
			uri := str(pl, "uri")
			label := fmt.Sprintf("%s — %s (%.0f tracks)", str(pl, "name"), str(nested(pl, "owner"), "display_name"), numField(nested(pl, "tracks"), "total"))
			result.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, label))
			choices = append(choices, AmbiguousChoice{ID: uri, Label: label})
			if firstURI == "" {
				firstURI = uri
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

// ─────────────────────────────────────────────────────────────────────────────
// Pure helpers (device pick, seek parse, safe accessors, delete)
// ───────────────────────────────────────────────────────────────────────

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

// parseSeekPosition converts "1:30" (mm:ss) / "90" (bare seconds) / relative "+30s"/"-15s"
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
	for i, k := range keys {
		if cur == nil {
			return nil
		}
		v, exists := cur[k]
		if !exists {
			return nil
		}
		next, ok := v.(map[string]any)
		if !ok {
			// Final segment may be a non-map value (e.g. an images array);
			// the path exists, so return the parent map rather than nil.
			if i == len(keys)-1 {
				return cur
			}
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

// asMap safely casts an arbitrary JSON value to a map; nil if it isn't one.
func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

// arrField returns m[key] as a JSON array; nil if absent or wrong type.
func arrField(m map[string]any, key string) []any {
	if m == nil {
		return nil
	}
	if v, ok := m[key].([]any); ok {
		return v
	}
	return nil
}

// numField returns m[key] as a float64 (JSON numbers); 0 if absent/wrong type.
func numField(m map[string]any, key string) float64 {
	if m == nil {
		return 0
	}
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}

// artistNames extracts the "name" of each entry in an artists array, skipping
// malformed entries. Used by search formatting.
func artistNames(artists []any) []string {
	var names []string
	for _, a := range artists {
		if n := str(asMap(a), "name"); n != "" {
			names = append(names, n)
		}
	}
	return names
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

// ─────────────────────────────────────────────────────────────────────────────
// Device management (ensureActiveDevice + one-shot recovery)
// ───────────────────────────────────────────────────────────────────────

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
