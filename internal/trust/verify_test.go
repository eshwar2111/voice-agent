package trust

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyToolError(t *testing.T) {
	v := NewStepVerifier(nil)
	ok, _ := v.Verify(context.Background(), Step{Tool: "read_file"}, "", os.ErrNotExist)
	if ok {
		t.Fatal("tool error must not verify")
	}
}

func TestVerifyCreateFilePostcondition(t *testing.T) {
	dir := t.TempDir()
	made := filepath.Join(dir, "out.txt")
	os.WriteFile(made, []byte("hi"), 0o644)
	missing := filepath.Join(dir, "nope.txt")
	v := NewStepVerifier(nil)

	p, _ := json.Marshal(map[string]string{"path": made})
	if ok, _ := v.Verify(context.Background(), Step{Tool: "create_file", Params: p}, "done", nil); !ok {
		t.Error("existing created file should verify")
	}
	pm, _ := json.Marshal(map[string]string{"path": missing})
	if ok, _ := v.Verify(context.Background(), Step{Tool: "create_file", Params: pm}, "done", nil); ok {
		t.Error("missing created file must not verify")
	}
}

func TestVerifyDeleteFilePostcondition(t *testing.T) {
	dir := t.TempDir()
	gone := filepath.Join(dir, "gone.txt") // never created
	v := NewStepVerifier(nil)
	p, _ := json.Marshal(map[string]string{"path": gone})
	if ok, _ := v.Verify(context.Background(), Step{Tool: "delete_file", Params: p}, "done", nil); !ok {
		t.Error("absent file should verify a delete")
	}
}

func TestVerifyFuzzyUsesJudge(t *testing.T) {
	called := false
	judge := func(ctx context.Context, goal, obs string) (bool, string) { called = true; return false, "no" }
	v := NewStepVerifier(judge)
	ok, _ := v.Verify(context.Background(), Step{Tool: "native_click", Goal: "click Save"}, "clicked", nil)
	if !called || ok {
		t.Errorf("fuzzy step must call judge and honor its verdict; called=%v ok=%v", called, ok)
	}
}

func TestVerifyFuzzyNilJudgeTrusts(t *testing.T) {
	v := NewStepVerifier(nil)
	if ok, _ := v.Verify(context.Background(), Step{Tool: "native_click"}, "clicked", nil); !ok {
		t.Error("with no judge, fuzzy step should be trusted (verified)")
	}
}

func TestVerifyDefaultTrue(t *testing.T) {
	v := NewStepVerifier(nil)
	if ok, _ := v.Verify(context.Background(), Step{Tool: "open_file"}, "", nil); !ok {
		t.Error("no applicable check → verified true")
	}
}
