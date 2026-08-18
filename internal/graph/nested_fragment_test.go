package graph

import (
	"reflect"
	"strings"
	"testing"
)

// ADR 0029 — a fragment may cite a fragment, bounded by a chain and a depth.
// Everything here is about the LEVELS: what the resolver does at the second and
// third citation hop that it did not have to do at the first.

// nestedOuter is the shape the whole feature exists for: a loop whose tail is
// itself a loop. Its `gate` node cites another MULTI-node fragment and binds a
// value naming one of the outer fragment's own nodes, which is what makes it
// the pass-through case as well as the namespacing case.
const nestedOuter = `fragment: outer
description: an outer loop whose tail is itself a loop
substitutions: [task]
exit: gate
nodes:
  - { id: build, prompt: "{{ with.task }}" }
  - id: gate
    use: inner
    depends_on: [build]
    with: { evidence: "{{ artifacts.build | inline }}" }
`

// nestedInner is the fragment cited from inside nestedOuter. Its own tokens
// name its own ids, and its `evidence` point receives a value that already
// carries the level above's namespace.
const nestedInner = `fragment: inner
description: a two-node gate
substitutions: [evidence]
exit: sign
nodes:
  - { id: check, prompt: "check {{ with.evidence }}" }
  - { id: sign, depends_on: [check], prompt: "sign off on {{ artifacts.check | inline }}" }
`

const nestedEntry = `name: nested
nodes:
  - { id: plan, prompt: plan the work }
  - id: top
    use: outer
    depends_on: [plan]
    worktree: lane
    with: { task: do the thing }
  - { id: ship, depends_on: [top], prompt: "ship {{ artifacts.top | inline }}" }
`

// TestLoadFile_NestedMultiNodeFragmentsComposeANamespace is the depth-2 feature
// in one assertion set: a loop citing a loop resolves to ordinary nodes with
// ordinary ids, wired internally by each level's own file and externally by the
// citing graph, and every guarantee ADR 0027 pinned at one join holds at two.
func TestLoadFile_NestedMultiNodeFragmentsComposeANamespace(t *testing.T) {
	path := writeGraphDir(t, nestedEntry, map[string]string{"outer": nestedOuter, "inner": nestedInner})
	res, err := LoadFile(path)
	if err != nil {
		t.Fatalf("a nested multi-node fragment must resolve: %v", err)
	}

	var ids []string
	for _, n := range res.Graph.Nodes {
		ids = append(ids, n.ID)
	}
	want := []string{"plan", "top/build", "top/gate/check", "top/gate/sign", "ship"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("resolved node ids = %v, want %v", ids, want)
	}

	byID := func(id string) Node {
		t.Helper()
		n, ok := res.Graph.NodeByID(id)
		if !ok {
			t.Fatalf("no node %q in the resolved graph", id)
		}
		return n
	}

	// §4 — the prefix at each level is the id the level above already minted,
	// and an entry node of the inner fragment inherits the depends_on of the
	// node that cited it, which itself inherited nothing but its own file's.
	if got := byID("top/gate/check").DependsOn; !reflect.DeepEqual(got, []string{"top/build"}) {
		t.Errorf("nested entry node depends_on = %v, want the citing node's [top/build]", got)
	}
	if got := byID("top/gate/sign").DependsOn; !reflect.DeepEqual(got, []string{"top/gate/check"}) {
		t.Errorf("inner internal edge = %v, want [top/gate/check]", got)
	}
	// §6 — exit: is transitive. `top` exits at `gate`, which is itself a loop
	// exiting at `sign`, so a loop still exposes exactly ONE value from the
	// outside at any depth: its transitive exit's artifact.
	if got := byID("ship").DependsOn; !reflect.DeepEqual(got, []string{"top/gate/sign"}) {
		t.Errorf("downstream depends_on = %v, want the TRANSITIVE exit [top/gate/sign]", got)
	}
	if got := byID("ship").Prompt; !strings.Contains(got, "{{ artifacts.top/gate/sign | inline }}") {
		t.Errorf("downstream artifact token = %q, want the transitive exit, filter intact", got)
	}

	// §1 — pass-through, and the guarantee it must not break. The outer file
	// wrote `{{ artifacts.build }}`; the outer level namespaced it to
	// `top/build` and then substituted nothing into it, and the INNER level
	// rewrote only its own file's text before inserting this value. A bound
	// value is never id-rewritten by any level.
	if got := byID("top/gate/check").Prompt; !strings.Contains(got, "check {{ artifacts.top/build | inline }}") {
		t.Errorf("passed-through binding = %q, want the outer level's namespaced token intact", got)
	}
	if got := byID("top/gate/sign").Prompt; !strings.Contains(got, "{{ artifacts.top/gate/check | inline }}") {
		t.Errorf("inner file's own token = %q, want it namespaced at ITS level", got)
	}
	if got := byID("top/build").Prompt; !strings.Contains(got, "do the thing") {
		t.Errorf("the entry graph's binding did not reach depth 1: %q", got)
	}

	// §6 — worktree propagates by value through every level, because each level
	// copies its using node's value onto what it emits and the next level down
	// finds it already there.
	for _, id := range []string{"top/build", "top/gate/check", "top/gate/sign"} {
		if got := byID(id).Worktree; got != "lane" {
			t.Errorf("node %q worktree = %q, want it propagated through every level", id, got)
		}
	}
}

// TestLoadFile_NestedResolutionsAreDisclosedParentFirst pins §5: one line per
// resolution, a nested resolution IS a resolution, the parent's line comes
// FIRST, and a nested resolution's NodeID is the already-namespaced id of the
// node that cited it — which is what lets a reader learn the shape of the tree
// from the ids alone, without opening a file.
func TestLoadFile_NestedResolutionsAreDisclosedParentFirst(t *testing.T) {
	path := writeGraphDir(t, nestedEntry, map[string]string{"outer": nestedOuter, "inner": nestedInner})
	res, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if len(res.Resolutions) != 2 {
		t.Fatalf("want one line per resolved use:, got %+v", res.Resolutions)
	}
	if got := res.Resolutions[0]; got.NodeID != "top" || got.Fragment != "outer" {
		t.Errorf("first disclosure line = (%q, %q), want the PARENT (top, outer) — children before the parent that explains them is the whole legibility argument", got.NodeID, got.Fragment)
	}
	if got := res.Resolutions[1]; got.NodeID != "top/gate" || got.Fragment != "inner" {
		t.Errorf("second disclosure line = (%q, %q), want (top/gate, inner)", got.NodeID, got.Fragment)
	}
	// Spliced names ids that EXIST in the resolved graph. `top/gate` is a loop,
	// so it is not one — the parent's line deliberately undercounts its own
	// subtree, and the lines below it are what answer "how big did this get".
	if got := res.Resolutions[0].Spliced; !reflect.DeepEqual(got, []string{"top/build"}) {
		t.Errorf("parent Spliced = %v, want only the ids that are nodes ([top/build]) — naming top/gate would name a node the graph does not contain", got)
	}
	if got := res.Resolutions[1].Spliced; !reflect.DeepEqual(got, []string{"top/gate/check", "top/gate/sign"}) {
		t.Errorf("nested Spliced = %v, want the two ids it minted", got)
	}
}

// laneFragment is the shape backlog-batch's lane A converts into and the one
// ADR 0029's depth bound is grounded on: a MULTI-node fragment whose internal
// nodes cite SINGLE-node fragments. It also carries a grant assembled across
// two files at depth, which is the §5 case PR #197's disclosure has to keep
// answering one level in.
const laneFragment = `fragment: lane
description: implement then review, each half cited
substitutions: [task, tools, focus]
exit: review
nodes:
  - id: dev
    use: impl
    with: { task: "{{ with.task }}", tools: "{{ with.tools }}" }
  - id: review
    use: judge
    depends_on: [dev]
    with: { focus: "{{ with.focus }}\nthe work said: {{ artifacts.dev | inline }}" }
`

const implFragment = `fragment: impl
description: do the work
substitutions: [task, tools]
node:
  prompt: "{{ with.task }}"
  allowed_tools: "{{ with.tools }}"
`

const judgeFragment = `fragment: judge
description: judge the work
substitutions: [focus]
node:
  prompt: "review it. {{ with.focus }}"
  allowed_tools: [Read]
`

const laneEntry = `name: lanes
nodes:
  - id: lane-a
    use: lane
    with:
      task: build the feature
      tools: [Read, Edit, "Bash(go *)"]
      focus: scope
  - { id: pr, depends_on: [lane-a], prompt: "open a PR quoting {{ artifacts.lane-a | inline }}" }
`

// TestLoadFile_MultiNodeFragmentCitingSingleNodeFragments is the lane-A shape:
// the citation direction both candidate conversions need, and the one that
// exercises §7's token rule and §5's grant disclosure at depth.
func TestLoadFile_MultiNodeFragmentCitingSingleNodeFragments(t *testing.T) {
	path := writeGraphDir(t, laneEntry, map[string]string{"lane": laneFragment, "impl": implFragment, "judge": judgeFragment})
	res, err := LoadFile(path)
	if err != nil {
		t.Fatalf("a multi-node fragment citing single-node ones must resolve: %v", err)
	}

	var ids []string
	for _, n := range res.Graph.Nodes {
		ids = append(ids, n.ID)
	}
	// A single-node hop mints NO namespace: it splices onto the citing node and
	// keeps that node's id, at depth exactly as at the top level.
	if want := []string{"lane-a/dev", "lane-a/review", "pr"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("resolved node ids = %v, want %v", ids, want)
	}

	byID := func(id string) Node {
		t.Helper()
		n, ok := res.Graph.NodeByID(id)
		if !ok {
			t.Fatalf("no node %q in the resolved graph", id)
		}
		return n
	}
	if got := byID("lane-a/dev").Prompt; got != "build the feature" {
		t.Errorf("pass-through binding through two levels = %q", got)
	}
	if got := byID("lane-a/review").Prompt; !strings.Contains(got, "{{ artifacts.lane-a/dev | inline }}") {
		t.Errorf("the lane fragment's own token = %q, want it namespaced by ITS level before the inner body received it", got)
	}
	// The downstream reference is the loop's exit, and the exit is an ordinary
	// node because a single-node hop mints nothing to resolve through.
	if got := byID("pr").DependsOn; !reflect.DeepEqual(got, []string{"lane-a/review"}) {
		t.Errorf("downstream depends_on = %v, want [lane-a/review]", got)
	}

	// §5 — a grant assembled across THREE files is announced by the level whose
	// substitution produced it, tagged with the id of the node that will run
	// with it. The entry graph bound the list, the lane fragment passed it
	// through, and the `impl` fragment is where substitution touched the field.
	var grants []ResolvedGrant
	for _, r := range res.Resolutions {
		grants = append(grants, r.Grants...)
	}
	if len(grants) != 1 {
		t.Fatalf("want exactly the one grant substitution touched, got %+v", grants)
	}
	if grants[0].NodeID != "lane-a/dev" {
		t.Errorf("grant NodeID = %q, want the fully namespaced id — the prefix is what says who asked", grants[0].NodeID)
	}
	if want := []string{"Read", "Edit", "Bash(go *)"}; !reflect.DeepEqual(grants[0].Tools, want) {
		t.Errorf("resolved grant = %v, want %v", grants[0].Tools, want)
	}
	if got := byID("lane-a/dev").AllowedTools; !reflect.DeepEqual(got, []string{"Read", "Edit", "Bash(go *)"}) {
		t.Errorf("the node's own grant = %v — the disclosure must name what the node runs with", got)
	}
}

// TestLoadFile_SingleNodeAliasChainMintsNothing pins the alias hop: a
// single-node fragment citing a single-node fragment resolves to ONE node with
// the citing node's own id, and both hops are disclosed.
func TestLoadFile_SingleNodeAliasChainMintsNothing(t *testing.T) {
	entry := `name: alias-chain
nodes:
  - { id: gate, use: outer-alias, with: { checks: run the suite } }
`
	fragments := map[string]string{
		"outer-alias": "fragment: outer-alias\ndescription: an alias\nsubstitutions: [checks]\nnode: { use: leaf, with: { checks: \"{{ with.checks }}\" } }\n",
		"leaf":        "fragment: leaf\ndescription: the real behavior\nsubstitutions: [checks]\nnode: { prompt: \"please {{ with.checks }}\", handoff: artifact }\n",
	}
	res, err := LoadFile(writeGraphDir(t, entry, fragments))
	if err != nil {
		t.Fatalf("an alias chain must resolve: %v", err)
	}
	if len(res.Graph.Nodes) != 1 || res.Graph.Nodes[0].ID != "gate" {
		t.Fatalf("an alias mints no namespace — resolved nodes = %+v", res.Graph.Nodes)
	}
	if got := res.Graph.Nodes[0].Prompt; got != "please run the suite" {
		t.Errorf("the binding did not travel through the alias: %q", got)
	}
	if len(res.Resolutions) != 2 || res.Resolutions[0].Fragment != "outer-alias" || res.Resolutions[1].Fragment != "leaf" {
		t.Fatalf("both hops must be disclosed, parent first: %+v", res.Resolutions)
	}
	for _, r := range res.Resolutions {
		if r.NodeID != "gate" {
			t.Errorf("alias resolution NodeID = %q, want the citing node's own id", r.NodeID)
		}
	}
}

// TestLoadFile_ChainAtTheBoundResolves is the positive edge of §3: THREE
// citation hops is the bound, so three must load. Without this the refusal
// test alone would pass just as well against an off-by-one bound of 2.
func TestLoadFile_ChainAtTheBoundResolves(t *testing.T) {
	entry := `name: at-the-bound
nodes:
  - { id: n, use: h1, with: { text: the payload } }
`
	relay := func(name, next string) string {
		return "fragment: " + name + "\ndescription: relays to " + next +
			"\nsubstitutions: [text]\nnode: { use: " + next + ", with: { text: \"{{ with.text }}\" } }\n"
	}
	fragments := map[string]string{
		"h1": relay("h1", "h2"),
		"h2": relay("h2", "h3"),
		"h3": "fragment: h3\ndescription: the third hop, which may not cite\nsubstitutions: [text]\nnode: { prompt: \"{{ with.text }}\" }\n",
	}
	res, err := LoadFile(writeGraphDir(t, entry, fragments))
	if err != nil {
		t.Fatalf("a chain of exactly %d hops is within the bound and must resolve: %v", maxFragmentChain, err)
	}
	if got := res.Graph.Nodes[0].Prompt; got != "the payload" {
		t.Errorf("the binding did not survive %d hops: %q", maxFragmentChain, got)
	}
	if len(res.Resolutions) != maxFragmentChain {
		t.Errorf("want one disclosure line per hop, got %+v", res.Resolutions)
	}
}

// TestLoadFile_DiamondCitationIsLegal is the difference between the cycle rule
// this ADR chose and the global visited-set it rejected. Two internal nodes
// citing the same leaf, and two entry nodes citing the same loop, are the
// normal case fragments exist for; only a repeat on the CURRENT path is a cycle.
func TestLoadFile_DiamondCitationIsLegal(t *testing.T) {
	entry := `name: diamond
nodes:
  - { id: a, use: pair, with: { what: first } }
  - { id: b, use: pair, with: { what: second } }
`
	fragments := map[string]string{
		"pair": "fragment: pair\ndescription: two nodes citing one leaf\nsubstitutions: [what]\nexit: right\nnodes:\n" +
			"  - { id: left, use: leaf, with: { text: \"{{ with.what }} left\" } }\n" +
			"  - { id: right, depends_on: [left], use: leaf, with: { text: \"{{ with.what }} right\" } }\n",
		"leaf": "fragment: leaf\ndescription: the leaf\nsubstitutions: [text]\nnode: { prompt: \"{{ with.text }}\" }\n",
	}
	res, err := LoadFile(writeGraphDir(t, entry, fragments))
	if err != nil {
		t.Fatalf("a diamond is not a cycle and must resolve: %v", err)
	}
	var ids []string
	for _, n := range res.Graph.Nodes {
		ids = append(ids, n.ID)
	}
	if want := []string{"a/left", "a/right", "b/left", "b/right"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("resolved ids = %v, want %v", ids, want)
	}
	// Four citations of `leaf` down four different paths, each disclosed on its
	// own line — plus the two `pair` lines.
	if len(res.Resolutions) != 6 {
		t.Fatalf("want 6 disclosure lines (2 loops + 4 leaves), got %+v", res.Resolutions)
	}
	if got, _ := res.Graph.NodeByID("b/right"); got.Prompt != "second right" {
		t.Errorf("the second diamond arm borrowed the first's binding: %q", got.Prompt)
	}
}

// TestLoadFile_NestedFragmentLoadErrors is the refusal set: every shape ADR
// 0029 decided must be a LOAD error with a message, never a hang, a stack
// overflow, or a graph nobody wrote arriving at run time.
func TestLoadFile_NestedFragmentLoadErrors(t *testing.T) {
	// cite is an entry graph whose one node cites `top`, which is where every
	// case below starts its chain.
	const cite = `name: nested-errors
nodes:
  - { id: n, use: top }
`
	// alias builds a single-node fragment that does nothing but cite the next
	// one — the cheapest way to spend a citation hop.
	alias := func(name, next string) string {
		return "fragment: " + name + "\ndescription: an alias for " + next + "\nnode: { use: " + next + " }\n"
	}

	cases := []struct {
		name      string
		entry     string
		fragments map[string]string
		wantErr   string
	}{
		{
			// §2 — a repeat on the CURRENT chain, named in order and charged to
			// the file whose use: line closes it.
			name:  "a citation cycle between two fragments",
			entry: cite,
			fragments: map[string]string{
				"top":  alias("top", "mid"),
				"mid":  alias("mid", "back"),
				"back": alias("back", "top"),
			},
			wantErr: "already on this citation chain (top → mid → back → top)",
		},
		{
			name:      "a fragment citing itself",
			entry:     cite,
			fragments: map[string]string{"top": alias("top", "top")},
			wantErr:   "already on this citation chain (top → top)",
		},
		{
			// §3 — the bound counts citation HOPS, and an alias hop spends it
			// even though it mints nothing, because what is bounded is how far
			// the loader walks.
			name:  "a citation chain one hop past the bound",
			entry: cite,
			fragments: map[string]string{
				"top": alias("top", "h2"),
				"h2":  alias("h2", "h3"),
				"h3":  alias("h3", "h4"),
				"h4":  "fragment: h4\ndescription: the fourth\nnode: { prompt: too deep }\n",
			},
			wantErr: "at citation hop 4 (top → h2 → h3 → h4), and a chain may be at most 3 fragment files deep",
		},
		{
			// §7 — the one direction that is derivable rather than chosen.
			name:  "a single-node fragment citing a multi-node one",
			entry: cite,
			fragments: map[string]string{
				"top":  alias("top", "loop"),
				"loop": "fragment: loop\ndescription: a loop\nexit: b\nnodes:\n  - { id: a, prompt: a }\n  - { id: b, depends_on: [a], prompt: b }\n",
			},
			wantErr: `a single-node fragment may not cite the multi-node fragment "loop"`,
		},
		{
			// §7's load error: inside a fragment there is no citing graph for a
			// single-node body's token to name, so a ref the citing fragment
			// does not declare could only point into whichever graph cited the
			// outer one. Charged to the citing SITE.
			name:  "a single-node body naming an id the citing fragment does not declare",
			entry: cite,
			fragments: map[string]string{
				"top":  "fragment: top\ndescription: a loop\nexit: b\nnodes:\n  - { id: a, prompt: a }\n  - { id: b, depends_on: [a], use: leaf }\n",
				"leaf": "fragment: leaf\ndescription: the leaf\nnode: { prompt: \"reads {{ artifacts.somewhere }}\" }\n",
			},
			wantErr: `declares no node "somewhere"`,
		},
		{
			// §1 — the missing-file error below depth 1 must name the CHAIN, or
			// a reader who never wrote `nope` is told nothing about who did.
			name:      "a nested citation of a fragment that is not there",
			entry:     cite,
			fragments: map[string]string{"top": alias("top", "nope")},
			wantErr:   `reached via top → nope, so the use: naming "nope" is written in the fragment file "top"`,
		},
		{
			// §6 — the real ceiling on what nesting folds, and it is not the
			// depth bound: multiNodeUsingKeys admits wiring only, at every
			// level, so a fragment may hold a nested loop or wrap that node in a
			// feedback arc of its own, never both.
			name:  "an internal node wrapping a nested loop in a feedback arc",
			entry: cite,
			fragments: map[string]string{
				"top": "fragment: top\ndescription: a loop\nexit: gate\nnodes:\n" +
					"  - { id: a, prompt: a }\n" +
					"  - { id: gate, depends_on: [a], use: inner, feedback: { rerun: a, max: 1 } }\n",
				"inner": "fragment: inner\ndescription: the inner loop\nexit: y\nnodes:\n  - { id: x, prompt: x }\n  - { id: y, depends_on: [x], prompt: y }\n",
			},
			wantErr: `declares "feedback" alongside a multi-node use:`,
		},
		{
			// §6 — `feedback.rerun` naming a nested loop is the same refusal the
			// entry graph already gets, one level in: rewritten to the exit it
			// would silently re-run one node instead of the loop.
			name:  "an internal feedback arc naming a nested loop",
			entry: cite,
			fragments: map[string]string{
				"top": "fragment: top\ndescription: a loop\nexit: after\nnodes:\n" +
					"  - { id: gate, use: inner }\n" +
					"  - { id: after, depends_on: [gate], prompt: after, feedback: { rerun: gate, max: 1 } }\n",
				"inner": "fragment: inner\ndescription: the inner loop\nexit: y\nnodes:\n  - { id: x, prompt: x }\n  - { id: y, depends_on: [x], prompt: y }\n",
			},
			wantErr: "which cites a multi-node fragment — a loop is not a rerun target",
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
			// the Validate-is-Issues()[0] contract, at every depth.
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

// TestLoadFile_ChainAtDepthOneIsNotRendered is the other half of the chain
// wording: at depth 1 the using node's id already locates the citing `use:` in
// the reader's own file, so a chain would be noise. It appears only where the
// id stops locating anything.
func TestLoadFile_ChainAtDepthOneIsNotRendered(t *testing.T) {
	path := writeGraphDir(t, "name: g\nnodes:\n  - { id: n, use: nope }\n", map[string]string{
		"other": "fragment: other\ndescription: unused\nnode: { prompt: p }\n",
	})
	err := loadFileError(t, path, "no fragment file")
	if strings.Contains(err.Error(), "reached via") {
		t.Errorf("a depth-1 error must not carry a chain: %q", err)
	}
}

// TestFragmentError_ChainSaysWitnessNotAccusation pins the wording §1 derived
// from the cache: a fragment file's judgment is made once per pass and charged
// to whichever citation document order reached first, so the chain printed is
// ONE witness rather than the reader's own path to the file.
func TestFragmentError_ChainSaysWitnessNotAccusation(t *testing.T) {
	e := &FragmentError{NodeID: "n", Fragment: "leaf", Reason: "broken", Chain: []string{"top", "mid", "leaf"}}
	msg := e.Error()
	if !strings.Contains(msg, "reached via top → mid → leaf") {
		t.Errorf("chain not rendered in order: %q", msg)
	}
	if strings.Contains(msg, "you reached") {
		t.Errorf("the chain is one witness, not the reader's own path: %q", msg)
	}
	if !strings.Contains(msg, `written in the fragment file "mid"`) {
		t.Errorf("the message must name the file whose use: line named the fragment: %q", msg)
	}
}

// TestSplicedNodeIDs_DropsInternalNodesThatAreThemselvesLoops is the §5 rule at
// the unit it is decided in, so the deliberate undercount cannot be "fixed"
// into naming an id the resolved graph does not contain.
func TestSplicedNodeIDs_DropsInternalNodesThatAreThemselvesLoops(t *testing.T) {
	frag := &fragmentFile{ids: []string{"build", "gate", "tail"}}
	got := splicedNodeIDs("top", frag, map[string]string{"top/gate": "sign"})
	if want := []string{"top/build", "top/tail"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("splicedNodeIDs = %v, want %v", got, want)
	}
}

// TestResolveExit_FollowsEveryLevelAndTerminates pins the transitive exit as a
// unit, including the two cases the loop's shape has to survive: an id that is
// not a loop at all, and a chain of them.
func TestResolveExit_FollowsEveryLevelAndTerminates(t *testing.T) {
	out := &fragmentOutcome{loops: map[string]string{"top": "gate", "top/gate": "sign"}}
	if got := out.resolveExit("top"); got != "top/gate/sign" {
		t.Errorf("resolveExit(top) = %q, want the transitive exit top/gate/sign", got)
	}
	if got := out.resolveExit("plan"); got != "plan" {
		t.Errorf("resolveExit on a non-loop must be the identity, got %q", got)
	}
}
