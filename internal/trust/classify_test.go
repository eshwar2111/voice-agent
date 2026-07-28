package trust

import (
	"encoding/json"
	"testing"
)

func TestClassifyTable(t *testing.T) {
	c := NewRiskClassifier()
	cases := []struct {
		tool string
		want Risk
	}{
		{"get_datetime", Safe},
		{"list_files", Safe},
		{"read_file", Safe},
		{"search", Safe},
		{"delete_file", Risky},
		{"keyboard_type", Risky},
		{"native_click", Risky},
		{"run_python", Risky},
		{"google_workflow_agent", Risky},
		{"some_unknown_tool", Risky}, // unknown default = Risky
	}
	for _, tc := range cases {
		if got := c.Classify(tc.tool, nil); got != tc.want {
			t.Errorf("Classify(%q)=%v want %v", tc.tool, got, tc.want)
		}
	}
}

func TestClassifyParamBump(t *testing.T) {
	c := NewRiskClassifier()
	// system_control shutdown → Risky even though base system_control is safe-ish
	sd, _ := json.Marshal(map[string]string{"action": "shutdown"})
	if c.Classify("system_control", sd) != Risky {
		t.Error("system_control shutdown should be Risky")
	}
	// window_control close → Risky
	cl, _ := json.Marshal(map[string]string{"action": "close"})
	if c.Classify("window_control", cl) != Risky {
		t.Error("window_control close should be Risky")
	}
	// media_control play (read-ish action) → Safe
	pl, _ := json.Marshal(map[string]string{"action": "play"})
	if c.Classify("media_control", pl) != Safe {
		t.Error("media_control play should be Safe")
	}
}
