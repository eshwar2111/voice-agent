package command

import (
	"strings"
)

type CommandIntent struct {
	Intent string
	Params map[string]string
}

func Parse(input string) CommandIntent {
	input = strings.TrimSpace(input)
	tokens := strings.Split(input, " ")

	if len(tokens) == 0 {
		return CommandIntent{Intent: "unknown"}
	}

	cmd := strings.ToLower(tokens[0])

	switch cmd {
	case "open":
		return CommandIntent{
			Intent: "open_app",
			Params: map[string]string{
				"app_name": strings.Join(tokens[1:], " "),
			},
		}

	case "search":
		return CommandIntent{
			Intent: "web_search",
			Params: map[string]string{
				"query": strings.Join(tokens[1:], " "),
			},
		}

	case "create":
		if len(tokens) > 2 && tokens[1] == "file" {
			return CommandIntent{
				Intent: "create_file",
				Params: map[string]string{
					"filename": strings.Join(tokens[2:], " "),
				},
			}
		}

	case "summarize", "rewrite":
		if len(tokens) > 1 && tokens[1] == "clipboard" {
			return CommandIntent{
				Intent: cmd + "_clipboard",
				Params: map[string]string{},
			}
		}

	case "explain":
		if len(tokens) > 1 && tokens[1] == "selection" {
			return CommandIntent{
				Intent: "explain_selection",
				Params: map[string]string{},
			}
		}

	case "analyze":
		if len(tokens) > 1 && tokens[1] == "screen" {
			return CommandIntent{
				Intent: "analyze_screen",
				Params: map[string]string{},
			}
		}

	case "explain_screen_error", "rewrite_clipboard_email", "summarize_screen_notes":
		return CommandIntent{
			Intent: cmd,
			Params: map[string]string{},
		}
	}

	return CommandIntent{Intent: "unknown"}
}
