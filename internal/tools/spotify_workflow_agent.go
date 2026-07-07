package tools

// SpotifyWorkflowAgentTool — orchestrates deep, multi-step Spotify sessions.
// Unlike SpotifyAssistantTool (single-intent dispatch) or SpotifyAICuratTool
// (playlist creation), this agent accepts a complex natural-language goal,
// decomposes it into a step plan, executes each step while carrying result
// context forward, and returns a final session summary.
//
// Example workflows:
//   "Check what I've been listening to this week, recommend 10 similar tracks,
//    create a playlist from them, then open it."
//   "I'm about to start a 2-hour coding session — curate a focus playlist, set
//    volume to 65%, and tell me what's playing."
//   "Find my 'gym' playlist, remix it with harder tracks, add 5 new bangers,
//    and update the playlist name."

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

// ─────────────────────────────────────────────────────────────────────────────
// Tool definition
// ─────────────────────────────────────────────────────────────────────────────

type SpotifyWorkflowAgentTool struct {
	Cfg      *config.Config
	Provider llm.Provider
}

func (t *SpotifyWorkflowAgentTool) Name() string { return "spotify_workflow_agent" }

func (t *SpotifyWorkflowAgentTool) Description() string {
	return "Advanced Spotify workflow agent that chains playback control, AI recommendation, playlist curation, and remixing together. Understands complex, multi-step goals like: 'curate a coding focus session, queue 5 similar tracks, then set volume to 60%.' Each step's output feeds into the next."
}

func (t *SpotifyWorkflowAgentTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"goal":    {"type": "string",  "description": "High-level natural-language Spotify workflow goal."},
			"context": {"type": "string",  "description": "Optional extra context (e.g., activity, mood, duration, device preference)."},
			"approved_plan": {"type": "string", "description": "JSON string of a plan generated in a previous dry-run. If provided, execution bypasses planning and runs these steps directly."}
		},
		"required": ["goal"]
	}`
}

func (t *SpotifyWorkflowAgentTool) RequiresConfirmation() bool { return false }

// ─────────────────────────────────────────────────────────────────────────────
// Step plan types
// ─────────────────────────────────────────────────────────────────────────────

type spotifyStep struct {
	Step   int    `json:"step"`
	Action string `json:"action"`
	Detail string `json:"detail"`
}

type spotifyPlan struct {
	Steps   []spotifyStep `json:"steps"`
	Summary string        `json:"summary"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Execute
// ─────────────────────────────────────────────────────────────────────────────

var spotifyAllowedActions = []string{
	"now_playing", "recent_tracks", "top_tracks",
	"play", "pause", "next", "previous",
	"volume", "devices",
	"search", "queue_add", "queue_clear",
	"playlist_list", "playlist_create", "playlist_add_tracks", "playlist_rename",
	"ai_recommend", "ai_curate", "ai_remix_analysis",
	"ai_summarise",
}

func (t *SpotifyWorkflowAgentTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Goal         string `json:"goal"`
		Context      string `json:"context"`
		ApprovedPlan string `json:"approved_plan"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Goal) == "" {
		return "", fmt.Errorf("goal is required")
	}

	ui.ShowNotification("Spotify Workflow — planning…")

	var plan *spotifyPlan
	var err error

	if args.ApprovedPlan != "" {
		// Use the plan that the user just reviewed and approved
		plan = &spotifyPlan{}
		if err := json.Unmarshal([]byte(args.ApprovedPlan), plan); err != nil {
			return "", fmt.Errorf("failed to parse approved plan: %w", err)
		}
	} else {
		// Step 1: Generate Plan
		plan, err = t.plan(ctx, args.Goal, args.Context)
		if err != nil {
			return "", fmt.Errorf("planning failed: %w", err)
		}

		// ── Phase 2: Validate plan ──────────────────────────────────────────
		validation := ValidateSpotifyPlan(plan)
		if !validation.Valid {
			summary := fmt.Sprintf("Spotify workflow plan is invalid.\n\n%s", validation.FormatIssues())
			return ToolResult{
				Summary: summary,
				Artifacts: map[string]interface{}{
					"goal":       args.Goal,
					"validation": validation.Issues,
					"status":     "plan_rejected",
				},
			}.String(), nil
		}

		// ── Phase 3: Trigger Approval Boundary ──────────────────────────────
		var steps []map[string]string
		for _, s := range plan.Steps {
			steps = append(steps, map[string]string{
				"label": fmt.Sprintf("Step %d: %s", s.Step, s.Action),
				"value": s.Detail,
			})
		}

		return PlanApprovalResult(
			"Review Spotify Workflow",
			fmt.Sprintf("I've drafted a %d-step plan for your Spotify session: %s", len(plan.Steps), args.Goal),
			map[string]interface{}{
				"goal":   args.Goal,
				"steps":  steps,
				"source": "spotify_workflow_agent",
				"raw":    plan,
			},
		).String(), nil
	}

	// ── Execution Loop ───────────────────────────────────────────────────────
	var log strings.Builder
	log.WriteString(fmt.Sprintf("Executing Spotify Workflow: %s\n\n", args.Goal))

	carryJSON := args.Context
	for _, step := range plan.Steps {
		ui.ShowNotification(fmt.Sprintf("Spotify: step %d — %s", step.Step, step.Action))
		rawResult, err := t.executeStep(ctx, step, carryJSON)
		if err != nil {
			log.WriteString(fmt.Sprintf("Step %d [%s]: ERROR — %v\n\n", step.Step, step.Action, err))
		} else {
			// Parse structured result; only log the human-readable summary
			parsed := ParseToolResult(rawResult)
			log.WriteString(fmt.Sprintf("Step %d [%s]: %s\n", step.Step, step.Action, step.Detail))

			// Check for ambiguity (Phase 2)
			if IsAmbiguous(parsed) {
				log.WriteString(fmt.Sprintf("⚠️ AMBIGUITY DETECTED: %s\n", parsed.Summary))
				log.WriteString("Halting workflow for user clarification.\n")
				return ToolResult{
					Summary:   parsed.Summary,
					Artifacts: parsed.Artifacts,
				}.String(), nil
			}

			preview := parsed.Summary
			if len(preview) > 600 {
				preview = preview[:600] + "…"
			}
			log.WriteString(fmt.Sprintf("Result: %s\n\n", preview))
			// Pass the raw JSON forward so next step can extract typed artifacts
			carryJSON = rawResult
		}
	}

	synthesis, _ := t.synthesise(ctx, args.Goal, log.String())
	log.WriteString("\n─────────────────────────────────────\n")
	log.WriteString("Session ready.\n\n")
	log.WriteString(synthesis)

	return ToolResult{
		Summary: synthesis,
		Artifacts: map[string]interface{}{
			"goal": args.Goal,
			"log":  log.String(),
		},
	}.String(), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Plan
// ─────────────────────────────────────────────────────────────────────────────

func (t *SpotifyWorkflowAgentTool) plan(ctx context.Context, goal, extra string) (*spotifyPlan, error) {
	prompt := fmt.Sprintf(`You are a Spotify workflow planner.

GOAL: %s
CONTEXT: %s

Break this into an ordered list of concrete steps. Each step must use exactly one of these actions:
%s

Return ONLY valid JSON:
{"steps": [{"step": 1, "action": "action_name", "detail": "plain English detail"}], "summary": "one-line summary"}

Rules:
- Maximum 6 steps
- Each step should depend on prior results OR be independent
- Prefer ai_summarise when condensing data between steps
- Only include steps that are truly needed`, goal, extra, strings.Join(spotifyAllowedActions, ", "))

	resp, err := t.Provider.Generate(ctx, prompt, nil)
	if err != nil {
		return nil, err
	}

	var plan spotifyPlan
	if err := json.Unmarshal([]byte(extractJSONText(resp)), &plan); err != nil {
		return &spotifyPlan{
			Steps:   []spotifyStep{{Step: 1, Action: "ai_curate", Detail: goal}},
			Summary: goal,
		}, nil
	}
	return &plan, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Execute step
// ─────────────────────────────────────────────────────────────────────────────

func (t *SpotifyWorkflowAgentTool) executeStep(ctx context.Context, step spotifyStep, carry string) (string, error) {
	// Parse typed artifact from prior step
	carryResult := ParseToolResult(carry)

	switch step.Action {

	case "now_playing":
		return (&SpotifyNowPlayingTool{Cfg: t.Cfg}).Execute(ctx, mustJSON(map[string]any{}))

	case "recent_tracks":
		return t.recentTracks(ctx)

	case "top_tracks":
		return t.topTracks(ctx)

	case "play":
		return (&SpotifyPlayTool{Cfg: t.Cfg}).Execute(ctx, mustJSON(map[string]any{"query": step.Detail}))

	case "pause":
		return (&SpotifyPauseTool{Cfg: t.Cfg}).Execute(ctx, mustJSON(map[string]any{}))

	case "next":
		return (&SpotifyNextTool{Cfg: t.Cfg}).Execute(ctx, mustJSON(map[string]any{}))

	case "previous":
		return (&SpotifyPreviousTool{Cfg: t.Cfg}).Execute(ctx, mustJSON(map[string]any{}))

	case "volume":
		vol := extractFirstInt(step.Detail)
		if vol < 0 {
			vol = extractFirstInt(carryResult.Summary)
		}
		if vol < 0 {
			vol = 50
		}
		return (&SpotifyVolumeTool{Cfg: t.Cfg}).Execute(ctx, mustJSON(map[string]any{"volume_percent": vol}))

	case "devices":
		return (&SpotifyDeviceTool{Cfg: t.Cfg}).Execute(ctx, mustJSON(map[string]any{}))

	case "search":
		return (&SpotifySearchTool{Cfg: t.Cfg}).Execute(ctx, mustJSON(map[string]any{"query": step.Detail, "limit": 5}))

	case "queue_add":
		return t.queueBySearch(ctx, step.Detail)

	case "queue_clear":
		return "Queue clear is not supported by the Spotify Web API — skip and proceed.", nil

	case "playlist_list":
		return (&SpotifyPlaylistsTool{Cfg: t.Cfg}).Execute(ctx, mustJSON(map[string]any{}))

	case "playlist_create":
		return t.createPlaylist(ctx, step.Detail, carryResult.Summary)

	case "playlist_add_tracks":
		return t.addTracksToPlaylist(ctx, step.Detail, carry)

	case "playlist_rename":
		return t.renamePlaylist(ctx, step.Detail, carry)

	case "ai_recommend":
		tool := &SpotifySmartRecommendTool{Cfg: t.Cfg, Provider: t.Provider}
		return tool.Execute(ctx, mustJSON(map[string]any{"action": "recommend", "seed": step.Detail}))

	case "ai_curate":
		tool := &SpotifyAICuratTool{Cfg: t.Cfg, Provider: t.Provider}
		return tool.Execute(ctx, mustJSON(map[string]any{"action": "create", "theme": step.Detail, "num_songs": 15}))

	case "ai_remix_analysis":
		// Use typed artifact first, then string scan fallback
		playlistID := extractArtifactString(carryResult, "playlist_id")
		if playlistID == "" {
			playlistID = extractArtifactString(carryResult, "id")
		}
		if playlistID == "" {
			playlistID = extractFieldFromContext(carry, "playlist_id")
		}
		if playlistID == "" {
			return "No playlist ID found in context to remix.", nil
		}
		tool := &SpotifyAICuratTool{Cfg: t.Cfg, Provider: t.Provider}
		return tool.Execute(ctx, mustJSON(map[string]any{
			"action":      "remix",
			"theme":       step.Detail,
			"playlist_id": playlistID,
		}))

	case "ai_summarise":
		prompt := fmt.Sprintf("Summarise this Spotify data concisely for the next workflow step:\n\n%s\n\nContext: %s", carryResult.Summary, step.Detail)
		return t.Provider.Generate(ctx, prompt, nil)

	default:
		return fmt.Sprintf("Unknown action '%s' — skipping.", step.Action), nil
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func (t *SpotifyWorkflowAgentTool) recentTracks(ctx context.Context) (string, error) {
	client, err := auth.GetSpotifyClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}
	body, err := spotifyGet(ctx, client, "/me/player/recently-played?limit=20")
	if err != nil {
		return "", err
	}
	var resp struct {
		Items []struct {
			Track struct {
				Name    string `json:"name"`
				URI     string `json:"uri"`
				Artists []struct {
					Name string `json:"name"`
				} `json:"artists"`
			} `json:"track"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Recently Played (%d tracks):\n", len(resp.Items)))
	for i, item := range resp.Items {
		if i >= 10 {
			break
		}
		artists := make([]string, len(item.Track.Artists))
		for j, a := range item.Track.Artists {
			artists[j] = a.Name
		}
		sb.WriteString(fmt.Sprintf("  %d. %s by %s\n", i+1, item.Track.Name, strings.Join(artists, ", ")))
	}
	return sb.String(), nil
}

func (t *SpotifyWorkflowAgentTool) topTracks(ctx context.Context) (string, error) {
	client, err := auth.GetSpotifyClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}
	body, err := spotifyGet(ctx, client, "/me/top/tracks?limit=10&time_range=short_term")
	if err != nil {
		return "", err
	}
	var resp struct {
		Items []struct {
			Name    string `json:"name"`
			URI     string `json:"uri"`
			Artists []struct {
				Name string `json:"name"`
			} `json:"artists"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Your Top Tracks (last 4 weeks) — %d tracks:\n", len(resp.Items)))
	for i, item := range resp.Items {
		artists := make([]string, len(item.Artists))
		for j, a := range item.Artists {
			artists[j] = a.Name
		}
		sb.WriteString(fmt.Sprintf("  %d. %s by %s\n", i+1, item.Name, strings.Join(artists, ", ")))
	}
	return sb.String(), nil
}

func (t *SpotifyWorkflowAgentTool) queueBySearch(ctx context.Context, query string) (string, error) {
	client, err := auth.GetSpotifyClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}
	body, err := spotifyGet(ctx, client,
		fmt.Sprintf("/search?q=%s&type=track&limit=1", url.QueryEscape(query)))
	if err != nil {
		return "", err
	}
	var result struct {
		Tracks struct {
			Items []struct {
				Name    string `json:"name"`
				URI     string `json:"uri"`
				Artists []struct{ Name string } `json:"artists"`
			} `json:"items"`
		} `json:"tracks"`
	}
	if err := json.Unmarshal(body, &result); err != nil || len(result.Tracks.Items) == 0 {
		return fmt.Sprintf("No track found for '%s'.", query), nil
	}
	track := result.Tracks.Items[0]
	_, err = spotifyPost(ctx, client,
		fmt.Sprintf("/me/player/queue?uri=%s", url.QueryEscape(track.URI)), nil)
	if err != nil {
		return "", err
	}
	artists := make([]string, len(track.Artists))
	for i, a := range track.Artists {
		artists[i] = a.Name
	}
	return fmt.Sprintf("Queued: %s by %s", track.Name, strings.Join(artists, ", ")), nil
}

func (t *SpotifyWorkflowAgentTool) createPlaylist(ctx context.Context, detail, carry string) (string, error) {
	client, err := auth.GetSpotifyClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}
	// Ask LLM for a name if detail doesn't give one
	name := detail
	if len(name) > 60 {
		name = name[:60]
	}
	payload, _ := json.Marshal(map[string]any{
		"name":        name,
		"description": fmt.Sprintf("Created by Voice Agent — %s", detail),
		"public":      false,
	})
	body, err := spotifyPost(ctx, client, "/me/playlists", payload)
	if err != nil {
		return "", err
	}
	var pl struct {
		ID   string `json:"id"`
		URI  string `json:"uri"`
		Name string `json:"name"`
	}
	json.Unmarshal(body, &pl)
	return ToolResult{
		Summary: fmt.Sprintf("Playlist created: %s (ID: %s)\nURL: https://open.spotify.com/playlist/%s", pl.Name, pl.ID, pl.ID),
		Artifacts: map[string]interface{}{
			"type":        "playlist_create",
			"playlist_id": pl.ID,
			"uri":         pl.URI,
			"name":        pl.Name,
		},
	}.String(), nil
}

func (t *SpotifyWorkflowAgentTool) addTracksToPlaylist(ctx context.Context, detail, carry string) (string, error) {
	client, err := auth.GetSpotifyClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}
	carryResult := ParseToolResult(carry)
	playlistID := extractArtifactString(carryResult, "playlist_id")
	if playlistID == "" {
		playlistID = extractArtifactString(carryResult, "id")
	}
	if playlistID == "" {
		playlistID = extractFieldFromContext(carry, "playlist_id")
	}
	if playlistID == "" {
		playlistID = extractFieldFromContext(carry, "id")
	}
	if playlistID == "" {
		return "No playlist ID in context — cannot add tracks.", nil
	}

	// Extract track URIs from carry context (from previous ai_curate / search step)
	var uris []string
	lines := strings.Split(carry, "\n")
	for _, line := range lines {
		if strings.Contains(line, "spotify:track:") {
			fields := strings.Fields(line)
			for _, f := range fields {
				if strings.HasPrefix(f, "spotify:track:") {
					uris = append(uris, f)
				}
			}
		}
	}

	if len(uris) == 0 {
		// Fall back: search for the detail query and add top result
		result, err := t.queueBySearch(ctx, detail)
		return result + "\n(Added to queue — no playlist URIs in context)", err
	}

	trackPayload, _ := json.Marshal(map[string]any{"uris": uris})
	endpoint := fmt.Sprintf("/playlists/%s/tracks", playlistID)
	_, err = spotifyPost(ctx, client, endpoint, trackPayload)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Added %d track(s) to playlist %s.", len(uris), playlistID), nil
}

func (t *SpotifyWorkflowAgentTool) renamePlaylist(ctx context.Context, detail, carry string) (string, error) {
	client, err := auth.GetSpotifyClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}
	carryResult := ParseToolResult(carry)
	playlistID := extractArtifactString(carryResult, "playlist_id")
	if playlistID == "" {
		playlistID = extractArtifactString(carryResult, "id")
	}
	if playlistID == "" {
		playlistID = extractFieldFromContext(carry, "playlist_id")
	}
	if playlistID == "" {
		playlistID = extractFieldFromContext(carry, "id")
	}
	if playlistID == "" {
		return "No playlist ID in context — cannot rename.", nil
	}
	payload, _ := json.Marshal(map[string]any{"name": detail})
	endpoint := fmt.Sprintf("/playlists/%s", playlistID)
	_, err = spotifyPut(ctx, client, endpoint, payload)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Playlist renamed to: %s", detail), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Final synthesis
// ─────────────────────────────────────────────────────────────────────────────

func (t *SpotifyWorkflowAgentTool) synthesise(ctx context.Context, goal, log string) (string, error) {
	if len(log) > 2000 {
		log = log[len(log)-2000:]
	}
	prompt := fmt.Sprintf(`Summarise this completed Spotify workflow in 2-3 sentences.

GOAL: %s
LOG: %s

Mention what's now playing or queued, what was created, and any issues.`, goal, log)
	return t.Provider.Generate(ctx, prompt, nil)
}

// ─────────────────────────────────────────────────────────────────────────────
// SpotifyContextualMoodTool — Reads your current listening habits and
// generates a contextual mood profile, then auto-starts a fitting session.
// ─────────────────────────────────────────────────────────────────────────────

type SpotifyContextualMoodTool struct {
	Cfg      *config.Config
	Provider llm.Provider
}

func (t *SpotifyContextualMoodTool) Name() string { return "spotify_mood_session" }
func (t *SpotifyContextualMoodTool) Description() string {
	return "Reads your recent listening history, infers your current mood and energy level using AI, then automatically curates and starts a perfectly matched listening session without you having to describe anything."
}
func (t *SpotifyContextualMoodTool) Parameters() string {
	return `{"type": "object", "properties": {"override_mood": {"type": "string", "description": "Optional: override the AI mood detection (e.g., 'energetic', 'calm', 'focus', 'sad')."}}, "required": []}`
}
func (t *SpotifyContextualMoodTool) RequiresConfirmation() bool { return false }

func (t *SpotifyContextualMoodTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		OverrideMood string `json:"override_mood"`
	}
	json.Unmarshal(params, &args)

	client, err := auth.GetSpotifyClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	// Step 1: get recent tracks to infer mood
	recentBody, _ := spotifyGet(ctx, client, "/me/player/recently-played?limit=10")
	var recent struct {
		Items []struct {
			Track struct {
				Name    string `json:"name"`
				Artists []struct{ Name string } `json:"artists"`
			} `json:"track"`
		} `json:"items"`
	}
	json.Unmarshal(recentBody, &recent)

	var trackList strings.Builder
	for _, item := range recent.Items {
		artists := make([]string, len(item.Track.Artists))
		for i, a := range item.Track.Artists {
			artists[i] = a.Name
		}
		trackList.WriteString(fmt.Sprintf("- %s by %s\n", item.Track.Name, strings.Join(artists, ", ")))
	}

	mood := args.OverrideMood
	if mood == "" {
		// Step 2: ask LLM to infer mood
		moodPrompt := fmt.Sprintf(`Based on these recently played tracks, infer the listener's current mood and energy state.

TRACKS:
%s

Return ONLY JSON: {"mood": "one of: energetic, calm, focus, melancholic, happy, romantic, hype", "energy": "high/medium/low", "theme": "a free-form description for playlist curation like 'dark electronic focus music'"}`, trackList.String())

		moodResp, err := t.Provider.Generate(ctx, moodPrompt, nil)
		if err != nil {
			mood = "focus"
		} else {
			var inferred struct {
				Mood   string `json:"mood"`
				Energy string `json:"energy"`
				Theme  string `json:"theme"`
			}
			if err := json.Unmarshal([]byte(extractJSONText(moodResp)), &inferred); err == nil {
				if inferred.Theme != "" {
					mood = inferred.Theme
				} else {
					mood = inferred.Mood
				}
			} else {
				mood = "focus"
			}
		}
	}

	ui.ShowNotification(fmt.Sprintf("Mood detected: %s — curating session…", mood))

	// Step 3: curate and launch
	curate := &SpotifyAICuratTool{Cfg: t.Cfg, Provider: t.Provider}
	result, err := curate.Execute(ctx, mustJSON(map[string]any{
		"action":    "create",
		"theme":     mood,
		"num_songs": 20,
	}))
	if err != nil {
		return "", err
	}

	return ToolResult{
		Summary: fmt.Sprintf("Mood Session Started\nDetected mood: %s\n\n%s", mood, ParseToolResult(result).Summary),
		Artifacts: map[string]interface{}{
			"type": "mood_session",
			"mood": mood,
		},
	}.String(), nil
}
