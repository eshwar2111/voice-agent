package validator

import (
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"

	"github.com/yourname/voice-agent/internal/intent"
)

var RestrictedPaths = []string{
	"system32",
	"windows",
	"program files",
}

var safeAppRegex = regexp.MustCompile(`^[a-zA-Z0-9\s\-\.]+$`)

func Validate(i intent.ParsedIntent) error {
	var params map[string]string
	if len(i.Parameters) > 0 {
		_ = json.Unmarshal(i.Parameters, &params)
	}

	switch i.Intent {
	case "open_app":
		appName, ok := params["app_name"]
		if !ok || strings.TrimSpace(appName) == "" {
			return errors.New("missing app_name parameter")
		}
		if !safeAppRegex.MatchString(appName) {
			return errors.New("app name contains unsafe characters, possible command injection")
		}

	case "create_file", "open_explorer":
		path, ok := params["path"]
		if !ok || strings.TrimSpace(path) == "" {
			return errors.New("missing path parameter")
		}

		// Verify path exists
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			return errors.New("the specified path does not exist or is not a directory")
		}

		lowerPath := strings.ToLower(path)
		for _, restricted := range RestrictedPaths {
			if strings.Contains(lowerPath, restricted) {
				return errors.New("action blocked: path contains restricted directory")
			}
		}

	case "web_search":
		query, ok := params["query"]
		if !ok || query == "" {
			return errors.New("missing query parameter")
		}

	case "open_website":
		url, ok := params["url"]
		if !ok || url == "" {
			return errors.New("missing url parameter")
		}

	case "speak":
		text, ok := params["text"]
		if !ok || len(strings.TrimSpace(text)) == 0 {
			return errors.New("missing text parameter for speak intent")
		}

	case "clarification_required":
		return errors.New("LLM requested clarification: " + i.Reason)

	default:
		// We allow other un-validated intents for now, or you could return an error here to be strict.
		return nil
	}

	return nil
}
