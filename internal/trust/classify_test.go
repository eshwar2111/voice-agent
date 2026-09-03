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
		// Safe: opening/reading/searching/creating/launching/automating.
		{"get_datetime", Safe},
		{"list_files", Safe},
		{"read_file", Safe},
		{"open_file", Safe},
		{"open_app", Safe},
		{"create_file", Safe},
		{"write_file", Safe},
		{"keyboard_type", Safe},
		{"native_click", Safe},
		{"find_file", Safe},
		{"some_unknown_tool", Safe}, // unknown default = Safe (narrow risk policy)
		// Risky: destructive, outbound, or arbitrary code.
		{"delete_file", Risky},
		{"move_file", Risky},
		{"gmail_send", Risky},
		{"run_python", Risky},
		{"run_terminal", Risky},
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
	// window_control close is no longer risky (recoverable) → Safe
	cl, _ := json.Marshal(map[string]string{"action": "close"})
	if c.Classify("window_control", cl) != Safe {
		t.Error("window_control close should be Safe")
	}
	// media_control play → Safe
	pl, _ := json.Marshal(map[string]string{"action": "play"})
	if c.Classify("media_control", pl) != Safe {
		t.Error("media_control play should be Safe")
	}
	// system_control lock (non-destructive) → Safe
	lk, _ := json.Marshal(map[string]string{"action": "lock"})
	if c.Classify("system_control", lk) != Safe {
		t.Error("system_control lock should be Safe")
	}
}
