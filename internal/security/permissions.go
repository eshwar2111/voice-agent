package security

type Profile struct {
	Name         string
	AllowedTools map[string]bool
}

func SafeProfile() Profile {
	return Profile{
		Name: "safe",
		AllowedTools: map[string]bool{
			"open_app":            true,
			"web_search":          true,
			"research":            true,
			"open_website":        true,
			"speak":               true,
			"summarize_clipboard": true,
			"rewrite_clipboard":   true,
			"explain_selection":   true,
			"analyze_screen":      true,
		},
	}
}

func DeveloperProfile() Profile {
	return Profile{
		Name: "developer",
		AllowedTools: map[string]bool{
			"open_app":            true,
			"web_search":          true,
			"research":            true,
			"open_website":        true,
			"create_file":         true,
			"open_explorer":       true,
			"list_files":          true,
			"delete_file":         true,
			"move_file":           true,
			"speak":               true,
			"summarize_clipboard": true,
			"rewrite_clipboard":   true,
			"explain_selection":   true,
			"analyze_screen":      true,
			"read_file":           true,
			"write_file":          true,
			"open_file":           true,
			"get_datetime":        true,
			"system_status":       true,
			"start_timer":         true,
			// Memory tools
			"remember":      true,
			"recall":        true,
			"list_memories": true,
			"save_memory":   true,
			// Automation tools
			"native_click":       true,
			"mouse_move":         true,
			"mouse_click":        true,
			"mouse_drag":         true,
			"get_mouse_position": true,
			"keyboard_type":      true,
			"keyboard_press":     true,
			"keyboard_combo":     true,
			"wait":               true,
			"media_control":      true,
			"window_control":     true,
			"system_control":     true,
			"run_python":         true,
			"browser_read_page":  true,
			"browser_navigate":   true,
			// Google Workspace
			"google_calendar_list":       true,
			"google_calendar_add_event":  true,
			"google_calendar_search":     true,
			"google_drive_list":          true,
			"google_docs_read":           true,
			"google_docs_create":         true,
			"google_docs_search":         true,
			"google_sheets_read":         true,
			"google_sheets_write":        true,
			"google_sheets_search":       true,
			"google_slides_read":         true,
			"google_slides_create":       true,
			"google_slides_search":       true,
			"google_gmail_list":          true,
			"google_gmail_send":          true,
			"google_gmail_search":        true,
			"google_gmail_get_email":     true,
			"google_ai_agent":            true,
			"google_workspace_assistant": true,
			// Spotify
			"spotify_now_playing":     true,
			"spotify_play":            true,
			"spotify_pause":           true,
			"spotify_next":            true,
			"spotify_previous":        true,
			"spotify_volume":          true,
			"spotify_search":          true,
			"spotify_playlists":       true,
			"spotify_devices":         true,
			"spotify_queue":           true,
			"spotify_account":         true,
			"spotify_ai_curate":       true,
			"spotify_smart_recommend": true,
			"spotify_assistant":       true,
			// Vision tools
			"find_and_click":      true,
			"scroll_and_find":     true,
			"verify_screen_state": true,
		},
	}
}

func (p Profile) IsAllowed(toolName string) bool {
	return p.AllowedTools[toolName]
}
