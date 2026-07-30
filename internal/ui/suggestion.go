package ui

// Callbacks wired by the ambient engine.
var (
	OnSuggestionAccept  func(id string)
	OnSuggestionDismiss func(id string)
)

// ShowSuggestion renders a proactive suggestion card in the overlay.
func ShowSuggestion(id, icon, title, message, action string) {
	if w == nil {
		return
	}
	bridge.Push("activity:update", map[string]any{
		"id": "ambient.nudge",
		"data": map[string]any{
			"id":      id,
			"icon":    icon,
			"title":   title,
			"message": message,
			"action":  action,
		},
	})
}
