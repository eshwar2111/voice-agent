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
			"get_datetime":        true,
			// Memory tools
			"remember":      true,
			"recall":        true,
			"list_memories": true,
			"save_memory":   true,
			// Automation tools
			"mouse_move":         true,
			"mouse_click":        true,
			"mouse_drag":         true,
			"get_mouse_position": true,
			"keyboard_type":      true,
			"keyboard_press":     true,
			"keyboard_combo":     true,
			"wait":               true,
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
