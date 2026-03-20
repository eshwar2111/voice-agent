package executor

import (
	"encoding/json"
	"os/exec"
	"strings"
)

type StartApp struct {
	Name  string `json:"Name"`
	AppID string `json:"AppID"`
}

var startAppsCache []StartApp
var appsLoaded bool

func getAppsList() ([]StartApp, error) {
	if appsLoaded {
		return startAppsCache, nil
	}

	// Fetch all installed apps from Windows index
	cmd := exec.Command("powershell", "-NoProfile", "-Command", "Get-StartApps | ConvertTo-Json -Depth 1")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var apps []StartApp
	if err := json.Unmarshal(out, &apps); err != nil {
		return nil, err
	}

	startAppsCache = apps
	appsLoaded = true
	return apps, nil
}

// FindApp uses exact and fuzzy matching to resolve an app name to its internal Windows AppID.
func FindApp(query string) (StartApp, bool) {
	apps, err := getAppsList()
	if err != nil || len(apps) == 0 {
		return StartApp{}, false
	}

	query = strings.ToLower(strings.TrimSpace(query))

	// Try exact match first
	for _, app := range apps {
		if strings.ToLower(app.Name) == query {
			return app, true
		}
	}

	// Try fuzzy containment match
	for _, app := range apps {
		if strings.Contains(strings.ToLower(app.Name), query) {
			return app, true
		}
	}

	return StartApp{}, false
}
