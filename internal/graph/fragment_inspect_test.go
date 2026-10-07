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

// TestInspectFragment_NestedUseIsReportedNotResolved (#338): a fragment whose
// own body holds a use: — on the single-node form's node, or on any entry of a
// nodes: list — is reported in NestedUses and never resolved, so the file it
// cites is never opened. Each cited file here is absent or a directory, either
// of which would be a load error had it been read.
func TestInspectFragment_NestedUseIsReportedNotResolved(t *testing.T) {
	dir := inspectDir(t, map[string]string{
		"outer": `fragment: outer
description: a loop forwarding a slot
substitutions: [cmd]
exit: gate
nodes:
  - id: look
    prompt: look around
    allowed_tools: [Read]
  - id: gate
    depends_on: [look]
    use: inner
    with: { command: "{{ with.cmd }}" }
`,
		"relay": "fragment: relay\ndescription: x\nnode: { use: gone }\n",
	})
	if err := os.Mkdir(filepath.Join(dir, "fragments", "inner.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]NestedUse{
		"outer": {NodeID: "outer/gate", Fragment: "inner"},
		"relay": {NodeID: "relay", Fragment: "gone"},
	} {
		got, err := InspectFragment(dir, name)
		if err != nil {
			t.Fatalf("InspectFragment(%q): %v — a load error means the cited file was read", name, err)
		}
		if !reflect.DeepEqual(got.NestedUses, []NestedUse{want}) {
			t.Errorf("%s: NestedUses = %v, want [%v]", name, got.NestedUses, want)
		}
		if got.Strays != nil || got.Nodes != nil || got.Advisories != nil {
			t.Errorf("%s: resolved anyway: strays %v nodes %+v advisories %v", name, got.Strays, got.Nodes, got.Advisories)
		}
		data, err := os.ReadFile(filepath.Join(dir, "fragments", name+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		if again, err := InspectFragmentData(dir, name, data); err != nil || !reflect.DeepEqual(again.NestedUses, got.NestedUses) {
			t.Errorf("%s: InspectFragmentData = %+v, %v; want the same nested use and no load error", name, again, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "fragments", "gone.yaml")); !os.IsNotExist(err) {
		t.Fatal("the cited file must not exist for this case to mean anything")
	}
}

// TestInspectFragment_LoadErrors (#338): a broken file and a missing file are
// each a load error, not an inspection.
func TestInspectFragment_LoadErrors(t *testing.T) {
	dir := inspectDir(t, map[string]string{
		"nameless": "description: x\nnode: { prompt: p }\n",
	})
	for _, name := range []string{"nameless", "absent", "../escape"} {
		if got, err := InspectFragment(dir, name); err == nil {
			t.Errorf("InspectFragment(%q) = %+v, want a load error", name, got)
		}
	}
}
