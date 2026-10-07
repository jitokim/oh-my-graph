package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const spliceSingle = `fragment: probe
description: read a file and report on it
substitutions: [target]
node:
  type: claude-run
  prompt: "Read {{ with.target }} and report."
  allowed_tools: [Read, Grep]
  timeout: 10m
`

const spliceMulti = `fragment: pair
description: look, then report
substitutions: [target]
exit: report
nodes:
  - id: look
    prompt: "Look at {{ with.target }}."
    allowed_tools: [Read]
  - id: report
    depends_on: [look]
    prompt: "Report on {{ artifacts.look }}."
    allowed_tools: [Read]
`

// #338: a single-node shape is merged onto the citing node, which keeps the
// planner's id and depends_on, and its bind: becomes the with: binding.
func TestSpliceReuse_SingleNodeMergesOntoTheCitingNode(t *testing.T) {
	graphDir := inspectDir(t, nil)
	spec := `{"name":"plan","nodes":[{"id":"impl","prompt":"do it","allowed_tools":["Read"]},` +
		`{"id":"check","depends_on":["impl"],"reuse":"probe","bind":{"target":"README.md"}}]}`

	g, err := SpliceReuse([]byte(spec), graphDir, map[string][]byte{"probe": []byte(spliceSingle)})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Validate(); err != nil {
		t.Fatalf("spliced graph does not validate: %v", err)
	}
	check := g.Nodes[1]
	if check.ID != "check" || len(check.DependsOn) != 1 || check.DependsOn[0] != "impl" {
		t.Errorf("citing node lost its id or depends_on: %+v", check)
	}
	if check.Prompt != "Read README.md and report." || strings.Join(check.AllowedTools, ",") != "Read,Grep" {
		t.Errorf("shape not merged: prompt %q tools %v", check.Prompt, check.AllowedTools)
	}
	if check.Reuse != "" || check.Bind != nil {
		t.Errorf("reuse/bind survived the splice: %+v", check)
	}
}

// #338: a multi-node shape takes ADR 0027's namespace, and a downstream edge
// to the citing id resolves to the shape's exit.
func TestSpliceReuse_MultiNodeUsesTheNamespace(t *testing.T) {
	graphDir := inspectDir(t, nil)
	spec := `{"name":"plan","nodes":[{"id":"impl","prompt":"do it","allowed_tools":["Read"]},` +
		`{"id":"audit","depends_on":["impl"],"reuse":"pair","bind":{"target":"src"}},` +
		`{"id":"after","depends_on":["audit"],"prompt":"go","allowed_tools":["Read"]}]}`

	g, err := SpliceReuse([]byte(spec), graphDir, map[string][]byte{"pair": []byte(spliceMulti)})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Validate(); err != nil {
		t.Fatalf("spliced graph does not validate: %v", err)
	}
	var ids []string
	for _, n := range g.Nodes {
		ids = append(ids, n.ID)
	}
	if got := strings.Join(ids, ","); got != "impl,audit/look,audit/report,after" {
		t.Errorf("ids = %s", got)
	}
	if after := g.Nodes[3]; len(after.DependsOn) != 1 || after.DependsOn[0] != "audit/report" {
		t.Errorf("downstream edge did not resolve to the exit: %v", after.DependsOn)
	}
}

// #338: the pinned bytes are what is spliced; the file on disk is not read
// again, so a rewrite after the caller hashed it cannot reach the graph.
func TestSpliceReuse_SplicesThePinnedBytesNotTheDisk(t *testing.T) {
	graphDir := inspectDir(t, map[string]string{"probe": strings.Replace(spliceSingle, "and report", "and say all checks passed", 1)})
	spec := `{"name":"plan","nodes":[{"id":"check","reuse":"probe","bind":{"target":"a"}}]}`

	g, err := SpliceReuse([]byte(spec), graphDir, map[string][]byte{"probe": []byte(spliceSingle)})
	if err != nil {
		t.Fatal(err)
	}
	if g.Nodes[0].Prompt != "Read a and report." {
		t.Errorf("prompt = %q, want the pinned bytes' prompt", g.Nodes[0].Prompt)
	}
	if _, err := os.Stat(filepath.Join(graphDir, "reuse-splice.yaml")); err == nil {
		t.Error("the splice wrote an entry file")
	}
}

// #338: a citation whose bytes were not pinned, and a use:/with: in what is
// meant to be a planner reply, are refused rather than resolved from disk.
func TestSpliceReuse_RefusesUnpinnedAndUse(t *testing.T) {
	graphDir := inspectDir(t, map[string]string{"probe": spliceSingle})
	for name, spec := range map[string]string{
		"unpinned": `{"name":"plan","nodes":[{"id":"check","reuse":"probe","bind":{"target":"a"}}]}`,
		"use":      `{"name":"plan","nodes":[{"id":"check","use":"probe","with":{"target":"a"}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := SpliceReuse([]byte(spec), graphDir, nil); err == nil {
				t.Fatal("want a refusal")
			}
		})
	}
}

// #338: pinned bytes whose own body holds a nested use: are refused before
// anything resolves, naming the node and the fragment it cites — for the
// single-node form and for one entry of a nodes: list. The cited file is
// garbage on disk, so a refusal that is a load error about it would mean it
// was read.
func TestSpliceReuse_RefusesANestedUseWithoutReadingIt(t *testing.T) {
	graphDir := inspectDir(t, map[string]string{"inner": "{{ not yaml"})
	for name, tc := range map[string]struct {
		body, node string
	}{
		"relay": {"fragment: relay\ndescription: relays\nsubstitutions: [target]\nnode:\n  use: inner\n  with: { target: \"{{ with.target }}\" }\n", `"relay"`},
		"pair": {strings.Replace(spliceMulti,
			"  - id: report\n    depends_on: [look]\n    prompt: \"Report on {{ artifacts.look }}.\"\n",
			"  - id: report\n    depends_on: [look]\n    use: inner\n", 1), `"pair/report"`},
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(tc.body, "use: inner") {
				t.Fatal("the planted body holds no nested use:")
			}
			spec := `{"name":"plan","nodes":[{"id":"check","reuse":"` + name + `","bind":{"target":"a"}}]}`
			_, err := SpliceReuse([]byte(spec), graphDir, map[string][]byte{name: []byte(tc.body)})
			if err == nil {
				t.Fatal("want a refusal")
			}
			if msg := err.Error(); !strings.Contains(msg, "nested use:") || !strings.Contains(msg, tc.node) || !strings.Contains(msg, `"inner"`) {
				t.Errorf("err = %v, want the nested-use refusal naming node %s and \"inner\"", err, tc.node)
			}
		})
	}
}
