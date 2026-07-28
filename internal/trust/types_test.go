package trust

import "testing"

func TestRiskString(t *testing.T) {
	if Safe.String() != "Safe" || Risky.String() != "Risky" {
		t.Fatalf("risk names wrong: %q %q", Safe.String(), Risky.String())
	}
}

func TestReportZeroValue(t *testing.T) {
	var r Report
	if r.Aborted || r.FailedAt != 0 || len(r.Completed) != 0 {
		t.Fatalf("unexpected zero Report: %+v", r)
	}
}
