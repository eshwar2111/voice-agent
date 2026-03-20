package executor

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/yourname/voice-agent/internal/intent"
)

func Execute(i intent.ParsedIntent) error {
	var params map[string]string
	if len(i.Parameters) > 0 {
		_ = json.Unmarshal(i.Parameters, &params)
	}

	fmt.Printf(">> Executing Intent: [%s] Parameters: %v\n", i.Intent, params)

	switch i.Intent {
	case "open_app":
		appName := params["app_name"]

		fmt.Printf("Searching for installed app: %s...\n", appName)
		app, found := FindApp(appName)
		if found {
			fmt.Printf("Found match: %s (AppID: %s)\n", app.Name, app.AppID)
			// Use the Windows AppsFolder trick for reliable UWP and registered app launching
			cmd := exec.Command("explorer.exe", `shell:AppsFolder\`+app.AppID)
			return cmd.Start()
		}

		// Fallback to basic CMD start if not found via PowerShell enumeration
		fmt.Printf("Could not find exact match for %s, falling back to basic shell execution...\n", appName)
		cmd := exec.Command("cmd", "/C", "start", appName)
		return cmd.Start()

	case "web_search":
		query := params["query"]
		searchURL := "https://duckduckgo.com/?q=" + url.QueryEscape(query)
		cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", searchURL)
		return cmd.Start()

	case "open_website":
		websiteURL := params["url"]
		fmt.Printf("Opening browser to: %s\n", websiteURL)
		cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", websiteURL)
		return cmd.Start()

	case "create_file":
		path := params["path"]
		filename := params["filename"]
		if filename == "" {
			filename = "new_file.txt"
		}
		fullPath := filepath.Join(path, filename)

		// Create empty file
		err := os.WriteFile(fullPath, []byte(""), 0644)
		if err != nil {
			return err
		}
		fmt.Printf("File created successfully at: %s\n", fullPath)

		// Open explorer to that file
		cmd := exec.Command("explorer", `/select,`, fullPath)
		return cmd.Start()

	case "open_explorer":
		path := params["path"]
		fmt.Printf("Opening explorer to: %s\n", path)
		cmd := exec.Command("explorer", path)
		return cmd.Start()

	case "speak":
		text := params["text"]
		return Speak(text)

	default:
		return fmt.Errorf("no executor defined for intent: %s", i.Intent)
	}
}
