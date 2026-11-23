package runstate

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A dirty tree is recorded as working_tree with its count and its sorted
// paths; a clean one leaves the key out of the file altogether (#455).
func TestSnapshotRecorder_RecordWorkingTree(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	rec := NewSnapshotRecorder(path, baseSnapshot("run-1"))

	paths := []string{"b.go", "a.go"}
	if err := rec.RecordWorkingTree(NewWorkingTree(paths)); err != nil {
		t.Fatal(err)
	}
	paths[0] = "mutated"
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkingTree == nil || got.WorkingTree.Changed != 2 || !slices.Equal(got.WorkingTree.Paths, []string{"a.go", "b.go"}) {
		t.Fatalf("working tree = %+v, want 2 paths a.go, b.go", got.WorkingTree)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"working_tree": {`) || !strings.Contains(string(raw), `"changed": 2`) {
		t.Fatalf("state.json lacks the working_tree record:\n%s", raw)
	}

	if err := rec.RecordWorkingTree(NewWorkingTree(nil)); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "working_tree") {
		t.Fatalf("a clean tree must leave working_tree out of state.json:\n%s", raw)
	}
}

// A later write of anything else keeps the record, and a resumed leg's
// recorder seeded with it keeps it until its own leg ends.
func TestSnapshotRecorder_WorkingTreeSurvivesOtherWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	base := baseSnapshot("run-1")
	base.WorkingTree = NewWorkingTree([]string{"x.go"})
	rec := NewSnapshotRecorder(path, base)
	base.WorkingTree.Paths[0] = "mutated"
	if err := rec.RecordNode("a", NodeRecord{Verdict: VerdictPass}); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkingTree == nil || !slices.Equal(got.WorkingTree.Paths, []string{"x.go"}) {
		t.Fatalf("working tree = %+v, want the seeded x.go", got.WorkingTree)
	}
}
