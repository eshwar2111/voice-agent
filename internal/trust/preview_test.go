package trust

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestShouldGate(t *testing.T) {
	if ShouldGate([]Step{{}}, []Risk{Safe}) {
		t.Error("single safe step must not gate")
	}
	if !ShouldGate([]Step{{}, {}}, []Risk{Safe, Safe}) {
		t.Error("two safe steps must gate (multi-step)")
	}
	if !ShouldGate([]Step{{}}, []Risk{Risky}) {
		t.Error("single risky step must gate")
	}
}

func TestBuildPreviewShape(t *testing.T) {
	steps := []Step{
		{Tool: "search", Goal: "find invoice"},
		{Tool: "delete_file", Goal: "remove old invoice"},
	}
	risks := []Risk{Safe, Risky}
	desc := func(tool string, p json.RawMessage) string {
		if tool == "delete_file" {
			return "Delete invoice_old.pdf"
		}
		return "Search files for 'invoice'"
	}
	out := BuildPreview("clean up invoices", steps, risks, desc)

	var card struct {
		Type string `json:"type"`
		Title string `json:"title"`
		Plan struct {
			Goal  string `json:"goal"`
			Steps []struct{ Label, Value string } `json:"steps"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(out), &card); err != nil {
		t.Fatalf("preview is not valid JSON: %v", err)
	}
	if card.Type != "workflow_approval" {
		t.Errorf("type=%q", card.Type)
	}
	if card.Plan.Goal != "clean up invoices" || len(card.Plan.Steps) != 2 {
		t.Fatalf("plan wrong: %+v", card.Plan)
	}
	if !strings.Contains(card.Plan.Steps[1].Label, "Risky") {
		t.Errorf("risky step must be tagged: %q", card.Plan.Steps[1].Label)
	}
	if card.Plan.Steps[1].Value != "Delete invoice_old.pdf" {
		t.Errorf("describe not used: %q", card.Plan.Steps[1].Value)
	}
}

func TestDefaultDescribe(t *testing.T) {
	p, _ := json.Marshal(map[string]string{"path": `C:\x\invoice_old.pdf`})
	if got := DefaultDescribe("delete_file", p); got != "Delete invoice_old.pdf" {
		t.Errorf("got %q", got)
	}
	q, _ := json.Marshal(map[string]string{"query": "budget"})
	if got := DefaultDescribe("search", q); got != "Search for 'budget'" {
		t.Errorf("got %q", got)
	}
}
