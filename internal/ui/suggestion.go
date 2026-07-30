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
	UpdateActivity("ambient.nudge", map[string]any{
		"id":      id,
		"icon":    icon,
		"title":   title,
		"message": message,
		"action":  action,
	})
}
