package graph

import (
	"reflect"
	"strings"
	"testing"
)

// qaLoopFragment is the shape ADR 0027 exists for: the implement → verify →
// review loop with one repair round that six shipped graphs and thirteen
// operator lanes had written out longhand. Everything the cases below assert
// about namespacing, inheritance, propagation and rewriting is asserted against
// this one file.
const qaLoopFragment = `fragment: qa-loop
description: implement -> verify -> review, with one repair round
substitutions: [task, checks_command]
exit: review
nodes:
  - id: impl
    prompt: |
      {{ with.task }}
      Findings from the previous round follow, empty on the first pass:
      {{ feedback.review }}
  - id: verify
    depends_on: [impl]
    prompt: "check the work described in {{ artifacts.impl }}"
    success_check:
      verify: { command: "{{ with.checks_command }}", timeout: 5m }
  - id: review
    depends_on: [verify]
    prompt: "review it; the checks said {{ artifacts.verify | inline }}"
    feedback: { rerun: impl, max: 1 }
`

// twoLaneGraph cites that one fragment TWICE, which is the property flat
// internal ids were rejected for and the one a namespace has to demonstrate.
const twoLaneGraph = `name: two-lane
inputs: [task_a, task_b, checks]
nodes:
  - { id: plan, prompt: plan the work }
  - id: qa-a
    use: qa-loop
    depends_on: [plan]
    worktree: lane-a
    with: { task: "{{ inputs.task_a }}", checks_command: "{{ inputs.checks }}" }
  - id: qa-b
    use: qa-loop
    depends_on: [plan]
    worktree: lane-b
    with: { task: "{{ inputs.task_b }}", checks_command: "{{ inputs.checks }}" }
  - id: pr
    depends_on: [qa-a, qa-b]
    prompt: "open a PR quoting {{ artifacts.qa-a | inline }} and {{ artifacts.qa-b | inline }}"
`

// TestLoadFile_MultiNodeFragmentSplicesALoop is the whole feature in one
// assertion set: two uses of one loop fragment resolve into six ordinary nodes
// with ordinary ids, wired the way the fragment says internally and the way the
// citing graph says externally.
func TestLoadFile_MultiNodeFragmentSplicesALoop(t *testing.T) {
	path := writeGraphDir(t, twoLaneGraph, map[string]string{"qa-loop": qaLoopFragment})
	res, err := LoadFile(path)
	if err != nil {
		t.Fatalf("a multi-node fragment must resolve: %v", err)
	}

	var ids []string
	for _, n := range res.Graph.Nodes {
		ids = append(ids, n.ID)
	}
	want := []string{"plan", "qa-a/impl", "qa-a/verify", "qa-a/review", "qa-b/impl", "qa-b/verify", "qa-b/review", "pr"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("spliced node ids = %v, want %v", ids, want)
	}

	byID := func(id string) Node {
		t.Helper()
		n, ok := res.Graph.NodeByID(id)
		if !ok {
			t.Fatalf("no node %q in the resolved graph", id)
		}
		return n
	}

	// An entry node — no internal parent — inherits the using node's
	// depends_on, so the loop starts where the citing graph says it starts.
	if got := byID("qa-a/impl").DependsOn; !reflect.DeepEqual(got, []string{"plan"}) {
		t.Errorf("entry node depends_on = %v, want the using node's [plan]", got)
	}
	// Internal edges are namespaced, so lane A's verify follows lane A's impl
	// and nothing of lane B's.
	if got := byID("qa-a/verify").DependsOn; !reflect.DeepEqual(got, []string{"qa-a/impl"}) {
		t.Errorf("internal depends_on = %v, want [qa-a/impl]", got)
	}
	if got := byID("qa-b/verify").DependsOn; !reflect.DeepEqual(got, []string{"qa-b/impl"}) {
		t.Errorf("the second use borrowed the first's internals: %v", got)
	}
	// So is the feedback arc's target.
	if arc := byID("qa-a/review").Feedback; arc == nil || arc.Rerun != "qa-a/impl" || arc.Max != 1 {
		t.Errorf("feedback arc = %+v, want rerun qa-a/impl max 1", arc)
	}
	// And every artifact/feedback token the fragment wrote.
	if got := byID("qa-a/verify").Prompt; !strings.Contains(got, "{{ artifacts.qa-a/impl }}") {
		t.Errorf("internal artifact token was not namespaced: %q", got)
	}
	if got := byID("qa-a/review").Prompt; !strings.Contains(got, "{{ artifacts.qa-a/verify | inline }}") {
		t.Errorf("internal artifact token lost its filter or its namespace: %q", got)
	}
	if got := byID("qa-b/impl").Prompt; !strings.Contains(got, "{{ feedback.qa-b/review }}") {
		t.Errorf("internal feedback token was not namespaced: %q", got)
	}
	// The substitution is the using site's, and lands per lane.
	if got := byID("qa-a/impl").Prompt; !strings.Contains(got, "{{ inputs.task_a }}") {
		t.Errorf("lane A's binding did not substitute: %q", got)
	}
	if got := byID("qa-b/impl").Prompt; !strings.Contains(got, "{{ inputs.task_b }}") {
		t.Errorf("lane B's binding did not substitute: %q", got)
	}
	if got := byID("qa-a/verify").SuccessCheck.Verify.Command; got != "{{ inputs.checks }}" {
		t.Errorf("bound verify command = %q", got)
	}

	// worktree stays on the using node and propagates to every spliced node —
	// exactly what backlog-batch writes by hand, once per lane node.
	for _, id := range []string{"qa-a/impl", "qa-a/verify", "qa-a/review"} {
		if got := byID(id).Worktree; got != "lane-a" {
			t.Errorf("node %q worktree = %q, want lane-a propagated from the using node", id, got)
		}
	}
	if got := byID("qa-b/review").Worktree; got != "lane-b" {
		t.Errorf("lane B's worktree = %q, want lane-b", got)
	}

	// From outside, the loop is ONE thing and its value is its exit's: both the
	// edge and the data reference resolve to `<using>/<exit>`.
	if got := byID("pr").DependsOn; !reflect.DeepEqual(got, []string{"qa-a/review", "qa-b/review"}) {
		t.Errorf("downstream depends_on = %v, want each loop's exit", got)
	}
	if got := byID("pr").Prompt; !strings.Contains(got, "{{ artifacts.qa-a/review | inline }}") ||
		!strings.Contains(got, "{{ artifacts.qa-b/review | inline }}") {
		t.Errorf("downstream artifact tokens did not resolve to the exits: %q", got)
	}

	// The disclosure line has to be able to say one `use:` became three nodes.
	if len(res.Resolutions) != 2 {
		t.Fatalf("want one resolution per use:, got %+v", res.Resolutions)
	}
	if got := res.Resolutions[0].Spliced; !reflect.DeepEqual(got, []string{"qa-a/impl", "qa-a/verify", "qa-a/review"}) {
		t.Errorf("resolution.Spliced = %v", got)
	}
	if res.Resolutions[0].Description != "implement -> verify -> review, with one repair round" {
		t.Errorf("resolution lost the fragment's description: %+v", res.Resolutions[0])
	}
}

// TestLoadFile_MultiNodeFragmentArtifactFilesCannotCollide is the on-disk half
// of "two uses cannot collide", and it is a different claim from distinct ids:
// distinct ids passing validateNodesUnique says nothing about the FILE each
// node's artifact lands in. It is asserted here, against the loader's real
// output, because the pair of facts only means something together.
func TestLoadFile_MultiNodeFragmentArtifactFilesCannotCollide(t *testing.T) {
	path := writeGraphDir(t, twoLaneGraph, map[string]string{"qa-loop": qaLoopFragment})
	res, err := LoadFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// The sanitizer lives in internal/handoff, which imports this package, so
	// the rule is applied here rather than imported: replace '/' with '~'.
	seen := make(map[string]string)
	for _, n := range res.Graph.Nodes {
		file := strings.ReplaceAll(n.ID, "/", "~") + ".out"
		if other, clash := seen[file]; clash {
			t.Errorf("nodes %q and %q both persist to %q", other, n.ID, file)
		}
		seen[file] = n.ID
	}
}

// TestLoadFile_MultiNodeUseCwdPropagates is worktree's twin, asserted directly
// rather than only through a shipped graph's golden. cwd and worktree are the
// two location keys a multi-node use: may carry, they propagate by the same two
// lines of spliceLoop, and a test that covers one of a pair is a test that will
// not notice the day the other stops.
func TestLoadFile_MultiNodeUseCwdPropagates(t *testing.T) {
	const entry = `name: located
nodes:
  - { id: plan, prompt: plan the work }
  - id: qa
    use: qa-loop
    depends_on: [plan]
    cwd: "{{ inputs.repo }}/service"
    with: { task: do it, checks_command: make local }
`
	path := writeGraphDir(t, entry, map[string]string{"qa-loop": qaLoopFragment})
	res, err := LoadFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, id := range []string{"qa/impl", "qa/verify", "qa/review"} {
		n, ok := res.Graph.NodeByID(id)
		if !ok {
			t.Fatalf("no node %q in the resolved graph", id)
		}
		// Verbatim, template and all: cwd interpolates per node at run time.
		if n.Cwd != "{{ inputs.repo }}/service" {
			t.Errorf("node %q cwd = %q, want the using node's, propagated unevaluated", id, n.Cwd)
		}
	}
}

// TestLoadFile_MultiNodeUseDependsOnIsTrimmedBeforeNamespacing pins the one
// word by which resolveLoopReferences and namespaceNode could disagree: the
// loop is looked up TRIMMED, so it must be respelled trimmed too. A quoted
// parent that minted " qa/review" would fail nodeIDPattern with a complaint
// about a shape the author never wrote.
func TestLoadFile_MultiNodeUseDependsOnIsTrimmedBeforeNamespacing(t *testing.T) {
	const entry = `name: padded
nodes:
  - { id: plan, prompt: plan the work }
  - id: qa
    use: qa-loop
    depends_on: [plan]
    with: { task: do it, checks_command: make local }
  - { id: pr, depends_on: [" qa"], prompt: ship it }
`
	path := writeGraphDir(t, entry, map[string]string{"qa-loop": qaLoopFragment})
	res, err := LoadFile(path)
	if err != nil {
		t.Fatalf("a padded reference to a loop must resolve like any other: %v", err)
	}
	pr, ok := res.Graph.NodeByID("pr")
	if !ok {
		t.Fatal("no pr in the resolved graph")
	}
	if !reflect.DeepEqual(pr.DependsOn, []string{"qa/review"}) {
		t.Errorf("depends_on = %q, want the exit with no padding carried into the id", pr.DependsOn)
	}
}

// TestLoadFile_BoundValueResolvesInTheCitingGraphsNamespace pins the rewrite
// ORDER, which is the one place two implementations would otherwise read the
// design differently. The namespace rewrite applies to the fragment file's own
// text and applies BEFORE substitution; a bound value is inserted afterwards
// and is never rewritten.
//
// The graph below is the trap: it binds `{{ artifacts.impl | inline }}` where
// `impl` is a node in the CITING graph, and the fragment also declares an
// internal node named `impl`. Substitute-then-rewrite would silently re-point
// that binding at the fragment's own node — a working reference quietly aimed
// at someone else's output, which is the worst available outcome.
func TestLoadFile_BoundValueResolvesInTheCitingGraphsNamespace(t *testing.T) {
	const entry = `name: shadowed
nodes:
  - { id: impl, prompt: the citing graph's own impl }
  - id: qa
    use: qa-loop
    depends_on: [impl]
    with: { task: "redo {{ artifacts.impl | inline }}", checks_command: make local }
`
	path := writeGraphDir(t, entry, map[string]string{"qa-loop": qaLoopFragment})
	res, err := LoadFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	spliced, ok := res.Graph.NodeByID("qa/impl")
	if !ok {
		t.Fatal("no qa/impl in the resolved graph")
	}
	if !strings.Contains(spliced.Prompt, "{{ artifacts.impl | inline }}") {
		t.Errorf("a bound token was rewritten into the fragment's namespace: %q", spliced.Prompt)
	}
	if strings.Contains(spliced.Prompt, "{{ artifacts.qa/impl") {
		t.Errorf("the bound token was aimed at the fragment's own node — and at the node reading it: %q", spliced.Prompt)
	}
}

// TestLoadFile_MultiNodeLoadErrors is the failure-first table: every refusal
// ADR 0027 adds, each named by the substring its error must carry, so the test
// proves the RIGHT rule fired rather than merely that something did.
func TestLoadFile_MultiNodeLoadErrors(t *testing.T) {
	using := func(body string) string {
		return "name: t\nnodes:\n  - { id: plan, prompt: p }\n" + body
	}
	const citeLoop = `  - id: qa
    use: qa-loop
    depends_on: [plan]
    with: { task: do it, checks_command: make local }
`
	// loop builds a multi-node fragment file from its declaration tail, so each
	// case below differs from the working one by exactly what it is testing.
	loop := func(tail string) string {
		return "fragment: qa-loop\ndescription: a loop\nsubstitutions: [task, checks_command]\n" + tail
	}
	const workingTail = `exit: review
nodes:
  - { id: impl, prompt: "{{ with.task }}" }
  - { id: review, depends_on: [impl], prompt: "judge; ran {{ with.checks_command }}" }
`

	cases := []struct {
		name      string
		entry     string
		fragments map[string]string
		wantErr   string
	}{
		{
			name:      "fragment declaring both forms",
			entry:     using(citeLoop),
			fragments: map[string]string{"qa-loop": loop("node: { prompt: \"{{ with.task }}{{ with.checks_command }}\" }\n" + workingTail)},
			wantErr:   "declares both node: and nodes:",
		},
		{
			name:      "nodes that is not a sequence",
			entry:     using(citeLoop),
			fragments: map[string]string{"qa-loop": loop("exit: review\nnodes: { id: impl }\n")},
			wantErr:   "must declare nodes: as a non-empty sequence",
		},
		{
			name:      "empty nodes sequence",
			entry:     using(citeLoop),
			fragments: map[string]string{"qa-loop": loop("exit: review\nnodes: []\n")},
			wantErr:   "must declare nodes: as a non-empty sequence",
		},
		{
			name:      "internal node with no id",
			entry:     using(citeLoop),
			fragments: map[string]string{"qa-loop": loop("exit: review\nnodes:\n  - { prompt: \"{{ with.task }}{{ with.checks_command }}\" }\n")},
			wantErr:   "nodes:[0] declares no id",
		},
		{
			name:      "internal id carrying the namespace separator",
			entry:     using(citeLoop),
			fragments: map[string]string{"qa-loop": loop("exit: review\nnodes:\n  - { id: other/impl, prompt: \"{{ with.task }}{{ with.checks_command }}\" }\n")},
			wantErr:   "which is not one path element",
		},
		{
			name:      "duplicate internal ids",
			entry:     using(citeLoop),
			fragments: map[string]string{"qa-loop": loop("exit: impl\nnodes:\n  - { id: impl, prompt: \"{{ with.task }}\" }\n  - { id: impl, prompt: \"{{ with.checks_command }}\" }\n")},
			wantErr:   `declares duplicate id "impl"`,
		},
		{
			// THE invariant, in its multi-node form: a fragment may name only
			// the ids it declares itself. `dev` exists in plenty of citing
			// graphs, which is exactly why naming it here has to be refused.
			name:      "internal depends_on naming an id the fragment does not declare",
			entry:     using(citeLoop),
			fragments: map[string]string{"qa-loop": loop("exit: review\nnodes:\n  - { id: impl, prompt: \"{{ with.task }}\", depends_on: [dev] }\n  - { id: review, depends_on: [impl], prompt: \"{{ with.checks_command }}\" }\n")},
			wantErr:   `depends_on names "dev", which this fragment does not declare`,
		},
		{
			// Entry-hood is decided by the key's PRESENCE, so an empty
			// sequence is neither an entry node nor an internal child: it
			// would inherit nothing and start at the top of the citing graph,
			// in parallel with the work the using node said it comes after.
			name:      "internal node declaring an empty depends_on",
			entry:     using(citeLoop),
			fragments: map[string]string{"qa-loop": loop("exit: review\nnodes:\n  - { id: impl, prompt: \"{{ with.task }}\", depends_on: [] }\n  - { id: review, depends_on: [impl], prompt: \"{{ with.checks_command }}\" }\n")},
			wantErr:   "declares an empty depends_on",
		},
		{
			name:      "internal feedback.rerun naming an id the fragment does not declare",
			entry:     using(citeLoop),
			fragments: map[string]string{"qa-loop": loop("exit: review\nnodes:\n  - { id: impl, prompt: \"{{ with.task }}\" }\n  - { id: review, depends_on: [impl], prompt: \"{{ with.checks_command }}\", feedback: { rerun: dev, max: 1 } }\n")},
			wantErr:   `feedback.rerun names "dev", which this fragment does not declare`,
		},
		{
			name:      "artifact token naming an id the fragment does not declare",
			entry:     using(citeLoop),
			fragments: map[string]string{"qa-loop": loop("exit: review\nnodes:\n  - { id: impl, prompt: \"{{ with.task }}\" }\n  - { id: review, depends_on: [impl], prompt: \"judge {{ artifacts.dev }} {{ with.checks_command }}\" }\n")},
			wantErr:   `this fragment declares no node "dev"`,
		},
		{
			name:      "multi-node fragment with no exit",
			entry:     using(citeLoop),
			fragments: map[string]string{"qa-loop": loop("nodes:\n  - { id: impl, prompt: \"{{ with.task }}\" }\n  - { id: review, depends_on: [impl], prompt: \"{{ with.checks_command }}\" }\n")},
			wantErr:   "must declare exit: <id>",
		},
		{
			name:      "exit naming an id the fragment does not declare",
			entry:     using(citeLoop),
			fragments: map[string]string{"qa-loop": loop("exit: pr\nnodes:\n  - { id: impl, prompt: \"{{ with.task }}\" }\n  - { id: review, depends_on: [impl], prompt: \"{{ with.checks_command }}\" }\n")},
			wantErr:   `exit: names "pr", which this fragment does not declare`,
		},
		{
			// The rule that keeps a loop fragment's validity from depending on
			// the graph citing it: exiting at a body node other than the arc's
			// declarer makes every downstream edge a side exit, which ADR 0010
			// refuses — in a graph whose author wrote nothing wrong.
			name:  "exit lying strictly inside one of the fragment's own feedback bodies",
			entry: using(citeLoop),
			fragments: map[string]string{"qa-loop": loop(`exit: verify
nodes:
  - { id: impl, prompt: "{{ with.task }}" }
  - { id: verify, depends_on: [impl], prompt: "{{ with.checks_command }}" }
  - { id: review, depends_on: [verify], prompt: judge, feedback: { rerun: impl, max: 1 } }
`)},
			wantErr: "lies inside the feedback body",
		},
		{
			// fragmentFeedbackBodies duplicates Graph.FeedbackBody, and this
			// is the case that proves it duplicates the GUARD too: `side` is
			// declared but is no ancestor of `review`, so that arc has no body
			// at all. Without the guard the fragment would be refused for an
			// exit "inside the feedback body" — a wrong error, reported at the
			// file level, that would hide the true one entirely, since the
			// splice never happens and validateFeedback never runs.
			name:  "exit at a feedback target that is not an ancestor",
			entry: using(citeLoop),
			fragments: map[string]string{"qa-loop": loop(`exit: side
nodes:
  - { id: impl, prompt: "{{ with.task }}" }
  - { id: side, prompt: "{{ with.checks_command }}" }
  - { id: review, depends_on: [impl], prompt: judge, feedback: { rerun: side, max: 1 } }
`)},
			wantErr: "is not a proper depends_on-ancestor",
		},
		{
			// TWO arcs whose bodies both contain the exit, which is what pins
			// the ORDER the bodies are judged in: b lies inside c's body
			// (rerun: a) and inside d's (rerun: b), so which of the two errors
			// is reported first must be the fragment's own declaration order —
			// c, then d — and not a map walk's. LoadFile and LintFile load this
			// file separately, so a shuffled order would not even be shuffled
			// the same way for the two views the assertion below compares.
			name:  "exit lying inside two feedback bodies at once",
			entry: using(citeLoop),
			fragments: map[string]string{"qa-loop": loop(`exit: b
nodes:
  - { id: a, prompt: "{{ with.task }}" }
  - { id: b, depends_on: [a], prompt: "{{ with.checks_command }}" }
  - { id: c, depends_on: [b], prompt: judge, feedback: { rerun: a, max: 1 } }
  - { id: d, depends_on: [c], prompt: judge again, feedback: { rerun: b, max: 1 } }
`)},
			wantErr: `lies inside the feedback body "c" declares (rerun: "a")`,
		},
		{
			name:      "exit on a single-node fragment",
			entry:     using(citeLoop),
			fragments: map[string]string{"qa-loop": loop("exit: whatever\nnode: { prompt: \"{{ with.task }}{{ with.checks_command }}\" }\n")},
			wantErr:   "declares exit: alongside node:",
		},
		{
			name:      "internal node declaring a worktree",
			entry:     using(citeLoop),
			fragments: map[string]string{"qa-loop": loop("exit: review\nnodes:\n  - { id: impl, prompt: \"{{ with.task }}\", worktree: lane }\n  - { id: review, depends_on: [impl], prompt: \"{{ with.checks_command }}\" }\n")},
			wantErr:   `node "impl" declares "worktree"`,
		},
		{
			name:      "internal node declaring a cwd",
			entry:     using(citeLoop),
			fragments: map[string]string{"qa-loop": loop("exit: review\nnodes:\n  - { id: impl, prompt: \"{{ with.task }}\", cwd: /tmp }\n  - { id: review, depends_on: [impl], prompt: \"{{ with.checks_command }}\" }\n")},
			wantErr:   `node "impl" declares "cwd"`,
		},
		{
			// An internal node MAY cite a fragment since ADR 0029; what it may
			// not do is name it from a binding. `prompt2`, not `prompt`: this
			// case's subject is judgeFragmentUse, and the key only has to keep
			// `{{ with.task }}` referenced so the substitution checks stay quiet
			// and the literal-name refusal is what fires. "Correcting" it to
			// `prompt` changes which rule the case proves, and it would still
			// pass — for the wrong reason.
			name:      "internal node citing a fragment named by a binding",
			entry:     using(citeLoop),
			fragments: map[string]string{"qa-loop": loop("exit: review\nnodes:\n  - { id: impl, use: \"{{ with.task }}\", prompt2: \"{{ with.task }}\" }\n  - { id: review, depends_on: [impl], prompt: \"{{ with.checks_command }}\" }\n")},
			wantErr:   "whose name carries a {{ … }} token",
		},
		{
			name: "behavior key on a multi-node use",
			entry: using(`  - id: qa
    use: qa-loop
    depends_on: [plan]
    success_check: { exit_zero: true }
    with: { task: do it, checks_command: make local }
`),
			fragments: map[string]string{"qa-loop": loop(workingTail)},
			wantErr:   `declares "success_check" alongside a multi-node use:`,
		},
		{
			name: "multi-node use with no id to namespace under",
			entry: using(`  - use: qa-loop
    depends_on: [plan]
    with: { task: do it, checks_command: make local }
`),
			fragments: map[string]string{"qa-loop": loop(workingTail)},
			wantErr:   "must declare an id",
		},
		{
			// Encapsulation is a refusal, not an impossibility: after
			// nodeIDPattern widened, reaching into a loop is spellable, and
			// this is where it is refused.
			name: "authored depends_on reaching into a loop",
			entry: using(citeLoop + `  - { id: pr, depends_on: [qa/impl], prompt: sneak in }
`),
			fragments: map[string]string{"qa-loop": loop(workingTail)},
			wantErr:   "depends_on carries a '/'",
		},
		{
			name:      "authored node id carrying the namespace separator",
			entry:     using("  - { id: a/b, prompt: minted by hand }\n"),
			fragments: map[string]string{"qa-loop": loop(workingTail)},
			wantErr:   "node id carries a '/'",
		},
		{
			// The fourth spelling, and the only one that reads the loop's
			// output rather than merely ordering against it: `qa/impl` really
			// exists after the splice AND really is an ancestor of pr, so
			// LintPlaceholders is satisfied too and nothing else would ever
			// object.
			name: "authored artifact token reaching into a loop",
			entry: using(citeLoop + `  - { id: pr, depends_on: [qa], prompt: "ship {{ artifacts.qa/impl | inline }}" }
`),
			fragments: map[string]string{"qa-loop": loop(workingTail)},
			wantErr:   "a placeholder token carries a '/'",
		},
		{
			// Not only a prompt: a binding is authored text too, and a walk
			// over every scalar is what makes that so without a field list.
			name: "a with: binding reaching into a loop",
			entry: using(citeLoop + `  - id: qa2
    use: qa-loop
    depends_on: [qa]
    with: { task: "redo {{ artifacts.qa/impl }}", checks_command: make local }
`),
			fragments: map[string]string{"qa-loop": loop(workingTail)},
			wantErr:   "a placeholder token carries a '/'",
		},
		{
			// The FIFTH spelling, and the one no other pass can see: a
			// single-node fragment's tokens are meant to name the citing
			// graph's nodes, so they are checked against no declared-ids set —
			// and refuseAuthoredNamespaces reads the entry document only. A
			// namespaced token written HERE would resolve, name a real node and
			// a real ancestor, and read a loop's internals from outside.
			name:      "single-node fragment body reaching into a loop",
			entry:     using(citeLoop),
			fragments: map[string]string{"qa-loop": loop("node: { prompt: \"{{ with.task }} {{ with.checks_command }} {{ artifacts.round1/review | inline }}\" }\n")},
			wantErr:   "whose id carries a '/'",
		},
		{
			// The symmetric refusal to feedback.rerun's below, and the reason
			// it is written rather than inherited: post-splice the using node
			// is gone, so validateFeedbackPlaceholders would tell the author
			// that "qa" declares no feedback edge — about a node they wrote as
			// `- id: qa` and are looking straight at.
			name: "feedback token naming a loop",
			entry: using(citeLoop + `  - { id: pr, depends_on: [qa], prompt: "ship {{ feedback.qa }}" }
`),
			fragments: map[string]string{"qa-loop": loop(workingTail)},
			wantErr:   "a loop's feedback arc is declared inside the fragment",
		},
		{
			name: "authored feedback.rerun reaching into a loop",
			entry: using(citeLoop + `  - { id: pr, depends_on: [qa], prompt: p, feedback: { rerun: qa/impl, max: 1 } }
`),
			fragments: map[string]string{"qa-loop": loop(workingTail)},
			wantErr:   "feedback.rerun carries a '/'",
		},
		{
			// Naming the loop ITSELF is refused too, and this one has to be
			// written down rather than left to fall out: rewritten to the exit
			// (as depends_on is), an author who asked to re-run their loop
			// would silently get one node re-run instead.
			name: "feedback.rerun naming a loop",
			entry: using(citeLoop + `  - { id: pr, depends_on: [qa], prompt: p, feedback: { rerun: qa, max: 1 } }
`),
			fragments: map[string]string{"qa-loop": loop(workingTail)},
			wantErr:   "a loop is not a rerun target",
		},
		{
			// The residue of the rewrite order: a bound value belongs to the
			// citing graph, so a using author who binds the FRAGMENT's internal
			// id gets a token naming nothing — which would otherwise survive
			// load and fail after the upstream nodes were paid for.
			name: "bound value naming a fragment-internal id",
			entry: using(`  - id: qa
    use: qa-loop
    depends_on: [plan]
    with: { task: "redo {{ artifacts.impl | inline }}", checks_command: make local }
`),
			fragments: map[string]string{"qa-loop": loop(workingTail)},
			wantErr:   "which is not a node in this graph",
		},
		{
			// The residue of mapping loop→exit BEFORE the existence test: the
			// ref maps to this node's own exit, which exists, so the check
			// above would pass a node quoting a descendant of itself.
			name: "bound value naming the using node itself",
			entry: using(`  - id: qa
    use: qa-loop
    depends_on: [plan]
    with: { task: "redo {{ artifacts.qa | inline }}", checks_command: make local }
`),
			fragments: map[string]string{"qa-loop": loop(workingTail)},
			wantErr:   "which is this node itself",
		},
		{
			// The splice REPLACES the using node, so post-splice uniqueness
			// sees `qa`, `qa/impl`, `qa/review` — all distinct. Without a
			// check of its own, a file that was a loud duplicate-id error
			// before ADR 0027 becomes a graph wired to the wrong producer.
			name:      "a multi-node use: id colliding with a hand-written node",
			entry:     using("  - { id: qa, prompt: the hand-written one }\n" + citeLoop),
			fragments: map[string]string{"qa-loop": loop(workingTail)},
			wantErr:   `shares its id "qa" with this node`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeGraphDir(t, tc.entry, tc.fragments)

			err := loadFileError(t, path, tc.wantErr)
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("LoadFile error = %q, want it to contain %q", err, tc.wantErr)
			}

			// The collect-all view must agree on which problem comes first —
			// the Validate-is-Issues()[0] contract, extended to fragments.
			issues, _, lintErr := LintFile(path)
			if lintErr != nil {
				t.Fatalf("LintFile I/O error: %v", lintErr)
			}
			if len(issues) == 0 {
				t.Fatal("LintFile found nothing where LoadFile failed")
			}
			if issues[0].Error() != err.Error() {
				t.Fatalf("LintFile first issue %q != LoadFile error %q", issues[0], err)
			}
		})
	}
}

// TestLoadFile_TwoBodyExitErrorOrderIsStable is the assertion one load cannot
// make. Go randomizes map iteration per `range`, so a fragmentFeedbackBodies
// keyed on its arcs map reports the two errors below in whichever order that
// run drew — passing any single check and disagreeing with itself across
// repeats. Loading the same file many times is what turns "it happened to be
// right once" into the property the package promises everywhere else: the first
// error is a deterministic one LoadFile and LintFile agree on.
func TestLoadFile_TwoBodyExitErrorOrderIsStable(t *testing.T) {
	path := writeGraphDir(t, `name: t
nodes:
  - { id: plan, prompt: p }
  - id: qa
    use: qa-loop
    depends_on: [plan]
    with: { task: do it }
`, map[string]string{"qa-loop": `fragment: qa-loop
description: a loop whose exit sits in two bodies
substitutions: [task]
exit: b
nodes:
  - { id: a, prompt: "{{ with.task }}" }
  - { id: b, depends_on: [a], prompt: check }
  - { id: c, depends_on: [b], prompt: judge, feedback: { rerun: a, max: 1 } }
  - { id: d, depends_on: [c], prompt: judge again, feedback: { rerun: b, max: 1 } }
`})

	// Declaration order, so `c`'s arc is judged before `d`'s.
	const want = `lies inside the feedback body "c" declares (rerun: "a")`

	var first string
	for i := 0; i < 64; i++ {
		_, err := LoadFile(path)
		if err == nil {
			t.Fatal("a fragment whose exit lies inside two feedback bodies must not load")
		}
		if i == 0 {
			first = err.Error()
			if !strings.Contains(first, want) {
				t.Fatalf("LoadFile error = %q, want the body %q declares to be reported first", first, "c")
			}
			continue
		}
		if err.Error() != first {
			t.Fatalf("load %d reported %q, load 0 reported %q — the two-body error order is not deterministic", i, err, first)
		}
	}

	// And the collect-all view agrees, which it cannot do by luck: LintFile
	// loads the fragment file on its own, so its map order is independent of
	// every load above.
	for i := 0; i < 64; i++ {
		issues, _, lintErr := LintFile(path)
		if lintErr != nil {
			t.Fatalf("LintFile I/O error: %v", lintErr)
		}
		if len(issues) == 0 {
			t.Fatal("LintFile found nothing where LoadFile failed")
		}
		if issues[0].Error() != first {
			t.Fatalf("LintFile first issue %q != LoadFile error %q", issues[0], first)
		}
	}
}

// TestLoadFile_SplicedNodesFaceTheGraphsOwnValidationsToo pins the half of the
// design that is deliberately NOT new machinery: a spliced node is an ordinary
// node, so the graph-level rules judge it exactly as they would a hand-written
// one, and the error names the spliced id — which is enough to find the using
// site, because the spliced id begins with it.
//
// Two shapes, both listed in ADR 0027's failure modes as inherent to splicing
// into someone else's graph rather than as new checks:
//
//   - an ENTRY node with `handoff: session` inherits the using node's parents,
//     whose ARITY the fragment cannot know. A using site with two parents
//     therefore produces a session node with two, and the existing
//     session-arity validation refuses it after resolution.
//   - a gate inside a spliced feedback body is refused by the existing
//     gate-in-body rule (ADR 0010 rule 4), post-splice, because whether the
//     body contains one is a property of the resolved graph.
func TestLoadFile_SplicedNodesFaceTheGraphsOwnValidationsToo(t *testing.T) {
	t.Run("session entry node inheriting two parents", func(t *testing.T) {
		const entry = `name: fan-in
nodes:
  - { id: a, prompt: a }
  - { id: b, prompt: b }
  - id: qa
    use: session-loop
    depends_on: [a, b]
    with: { task: do it }
`
		path := writeGraphDir(t, entry, map[string]string{"session-loop": `fragment: session-loop
description: a loop whose entry resumes its parent
substitutions: [task]
exit: review
nodes:
  - { id: impl, prompt: "{{ with.task }}", handoff: session }
  - { id: review, depends_on: [impl], prompt: judge }
`})
		err := loadFileError(t, path, "handoff: session with 2 parents")
		if !strings.Contains(err.Error(), `node "qa/impl"`) {
			t.Errorf("the refusal must name the SPLICED id, which locates the using site: %v", err)
		}
	})

	t.Run("gate inside a spliced feedback body", func(t *testing.T) {
		const entry = `name: gated-loop
nodes:
  - { id: plan, prompt: p }
  - { id: qa, use: gate-loop, depends_on: [plan], with: { task: do it } }
`
		path := writeGraphDir(t, entry, map[string]string{"gate-loop": `fragment: gate-loop
description: a loop with a human gate in its body
substitutions: [task]
exit: review
nodes:
  - { id: impl, prompt: "{{ with.task }}" }
  - { id: approve, type: gate, depends_on: [impl] }
  - { id: review, depends_on: [approve], prompt: judge, feedback: { rerun: impl, max: 1 } }
`})
		err := loadFileError(t, path, "feedback body contains gate")
		if !strings.Contains(err.Error(), `"qa/approve"`) {
			t.Errorf("the refusal must name the spliced gate: %v", err)
		}
	})
}

// loadFileError asserts LoadFile refused and returns the refusal.
func loadFileError(t *testing.T, path, want string) error {
	t.Helper()
	if _, err := LoadFile(path); err != nil {
		return err
	}
	t.Fatalf("LoadFile accepted a graph that must fail with %q", want)
	return nil
}

// TestLoadFile_SingleNodeFragmentIsUnchanged is the regression proof that ADR
// 0013's rule was GENERALIZED and not weakened: the same fragment file, cited
// the same way, resolves to one node with the using node's own id, and its
// wiring refusals still fire. Nothing about the single-node form is spliced,
// namespaced or renamed by the multi-node machinery.
func TestLoadFile_SingleNodeFragmentIsUnchanged(t *testing.T) {
	path := writeGraphDir(t, usingGraph(usingE2E), map[string]string{"e2e-verify": testFragment})
	res, err := LoadFile(path)
	if err != nil {
		t.Fatalf("single-node fragment must still resolve: %v", err)
	}
	if len(res.Graph.Nodes) != 2 {
		t.Fatalf("want two nodes, got %d", len(res.Graph.Nodes))
	}
	node, ok := res.Graph.NodeByID("e2e")
	if !ok {
		t.Fatal("the single-node form must keep the using node's own id")
	}
	if !reflect.DeepEqual(node.DependsOn, []string{"dev"}) {
		t.Errorf("depends_on = %v, want the using node's [dev]", node.DependsOn)
	}
	if len(res.Resolutions) != 1 || len(res.Resolutions[0].Spliced) != 0 {
		t.Errorf("a single-node resolution splices no extra ids: %+v", res.Resolutions)
	}
}

// TestLoadFile_SingleNodeBoundArtifactTokenIsNotJudgedAsALoop pins the OTHER
// half of "generalized, not weakened": the bound-artifact existence check is a
// consequence of namespacing, so it applies to the multi-node form alone.
//
// A single-node `use:` mints no namespace and rewrites no token — its body
// lands on the using node's own id — so a bound `{{ artifacts.x }}` there is
// the same thing as the same token typed into a plain node's prompt, and this
// package leaves that to the advisory sweep (handoff.LintPlaceholders)
// everywhere else. Judging it here would say two false things out loud: that a
// token naming a nonexistent node is a hard load error for one spelling of an
// ordinary node, and — for a node quoting its OWN id — that the graph contains
// a loop, an exit and a descendant, none of which a single-node splice has.
func TestLoadFile_SingleNodeBoundArtifactTokenIsNotJudgedAsALoop(t *testing.T) {
	cases := []struct {
		name  string
		bound string
	}{
		{name: "naming a node this graph does not have", bound: "run make local, then read {{ artifacts.ghost | inline }}"},
		{name: "naming the using node itself", bound: "run make local, then read {{ artifacts.e2e | inline }}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeGraphDir(t, usingGraph("  - id: e2e\n    use: e2e-verify\n    depends_on: [dev]\n    with:\n      checks: \""+tc.bound+"\"\n"),
				map[string]string{"e2e-verify": testFragment})
			res, err := LoadFile(path)
			if err != nil {
				t.Fatalf("a single-node use: must not face the loop's bound-reference rules: %v", err)
			}
			node, ok := res.Graph.NodeByID("e2e")
			if !ok {
				t.Fatal("the single-node form must keep the using node's own id")
			}
			// And the binding still substituted verbatim — the token survives
			// resolution and is interpolated (or refused) at run time, exactly
			// as one written inline would be.
			if !strings.Contains(node.Prompt, tc.bound) {
				t.Errorf("bound value did not reach the spliced prompt: %q", node.Prompt)
			}
		})
	}
}
