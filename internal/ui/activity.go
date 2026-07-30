package ui

// UpdateActivity pushes or refreshes a live activity in the island.
// Unknown ids are dropped by the JS registry with a log line, never thrown.
func UpdateActivity(id string, data any) {
	if bridge == nil {
		return
	}
	bridge.Push("activity:update", map[string]any{"id": id, "data": data})
}

// EndActivity removes a live activity.
func EndActivity(id string) {
	if bridge == nil {
		return
	}
	bridge.Push("activity:end", map[string]string{"id": id})
}
