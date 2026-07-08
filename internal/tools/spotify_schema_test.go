package tools

import (
	"encoding/json"
	"testing"
)

func TestSpotifySmartRecommendSchemaIsValidJSON(t *testing.T) {
	tool := &SpotifySmartRecommendTool{}
	var v map[string]interface{}
	if err := json.Unmarshal([]byte(tool.Parameters()), &v); err != nil {
		t.Fatalf("SpotifySmartRecommendTool.Parameters() is not valid JSON: %v", err)
	}
}
