package ui

import (
	"encoding/json"
	"fmt"
)

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
	jid, _ := json.Marshal(id)
	jicon, _ := json.Marshal(icon)
	jt, _ := json.Marshal(title)
	jm, _ := json.Marshal(message)
	ja, _ := json.Marshal(action)
	w.Dispatch(func() {
		w.Eval(fmt.Sprintf("showSuggestion(%s,%s,%s,%s,%s);", string(jid), string(jicon), string(jt), string(jm), string(ja)))
	})
}
