package graph

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// inspectDir plants fragment files under <tmp>/fragments and returns <tmp>, the
// graph directory InspectFragment resolves them from (#338).
func inspectDir(t *testing.T, fragments map[string]string) string {
	t.Helper()
	return filepath.Dir(writeGraphDir(t, "name: unused\n", fragments))
}

// TestInspectFragment_ReportsWhatTheLoaderDerived pins the facts a reuse
// catalog is built from (#338): the bytes the loader judged, the binds filtered
// to referenced slots, the single-node body resolved under the fragment's own
// name, and a slot that lands only in prompt: producing no stray.
func TestInspectFragment_ReportsWhatTheLoaderDerived(t *testing.T) {
	body := `fragment: ask
description: ask one question
substitutions: [question]
node:
  prompt: "answer {{ with.question }}"
  allowed_tools: [Read]
  timeout: 10m
`
	dir := inspectDir(t, map[string]string{"ask": body})

	got, err := InspectFragment(dir, "ask")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Data) != body {
		t.Errorf("Data = %q, want the file's bytes", got.Data)
	}
	if got.Source != filepath.Join(dir, "fragments", "ask.yaml") {
		t.Errorf("Source = %q", got.Source)
	}
	if !reflect.DeepEqual(got.Binds, []string{"question"}) || got.IDs != nil {
		t.Errorf("Binds = %v, IDs = %v", got.Binds, got.IDs)
	}
	if len(got.Strays) != 0 || len(got.Advisories) != 0 {
		t.Errorf("Strays = %v, Advisories = %v, want none", got.Strays, got.Advisories)
	}
	if len(got.Nodes) != 1 || got.Nodes[0].ID != "ask" || got.Nodes[0].Timeout != "10m" ||
		!strings.Contains(got.Nodes[0].Prompt, "answer ") {
		t.Errorf("Nodes = %+v", got.Nodes)
	}
}

// TestInspectFragment_StrayIsJudgedWhereTheInnerFragmentPutsIt is the ADR 0029
// chain (#338): the outer file writes its slot only into a nested use:'s
// with:, and the inner file puts that binding into success_check.verify — so
// the stray is reported at the inner field, under the namespaced id, and the
// inner file's own advisory travels with it.
func TestInspectFragment_StrayIsJudgedWhereTheInnerFragmentPutsIt(t *testing.T) {
	dir := inspectDir(t, map[string]string{
		"outer": `fragment: outer
description: a loop forwarding a slot
substitutions: [cmd]
exit: gate
nodes:
  - id: gate
    use: inner
    with: { command: "{{ with.cmd }}", unused: x }
`,
		"inner": `fragment: inner
description: a gate
substitutions: [command, unused]
node:
  prompt: check it
  allowed_tools: [Read]
  success_check: { verify: { command: "{{ with.command }}" } }
`,
	})

	got, err := InspectFragment(dir, "outer")
	if err != nil {
		t.Fatal(err)
	}
	want := []SlotLanding{{Slot: "cmd", NodeID: "outer/gate", Field: "success_check.verify.command"}}
	if !reflect.DeepEqual(got.Strays, want) {
		t.Errorf("Strays = %v, want %v", got.Strays, want)
	}
	if got.Nodes != nil {
		t.Errorf("Nodes must be nil while a slot is stray, got %+v", got.Nodes)
	}
	if !reflect.DeepEqual(got.IDs, []string{"gate"}) {
		t.Errorf("IDs = %v", got.IDs)
	}
	if len(got.Advisories) != 1 || got.Advisories[0].Fragment != "inner" {
		t.Errorf("Advisories = %v, want the nested file's unused-slot advisory", got.Advisories)
	}
}

// TestInspectFragment_LoadErrors (#338): a broken file, a broken nested
// citation and a missing file are each a load error, not an inspection.
func TestInspectFragment_LoadErrors(t *testing.T) {
	dir := inspectDir(t, map[string]string{
		"nameless":   "description: x\nnode: { prompt: p }\n",
		"cites-gone": "fragment: cites-gone\ndescription: x\nnode: { use: gone }\n",
	})
	for _, name := range []string{"nameless", "cites-gone", "absent", "../escape"} {
		if got, err := InspectFragment(dir, name); err == nil {
			t.Errorf("InspectFragment(%q) = %+v, want a load error", name, got)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "fragments", "gone.yaml")); !os.IsNotExist(err) {
		t.Fatal("the cited file must not exist for this case to mean anything")
	}
}
