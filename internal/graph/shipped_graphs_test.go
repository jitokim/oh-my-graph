package graph

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/graphs"
)

// shippedTemplateNames is the set of graph templates the binary carries, as
// slash-separated payload paths. It walks rather than ReadDir's the root
// because the payload is nested — `graphs/fragments/*.yaml` ships alongside
// `graphs/*.yaml` (ADR 0013) — and skips those fragment files: a fragment is a
// single-node definition, not a graph, and only loads through a template that
// cites it.
func shippedTemplateNames(t *testing.T) []string {
	t.Helper()
	var names []string
	err := fs.WalkDir(graphs.FS, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && !strings.Contains(path, "/") {
			names = append(names, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read embedded graphs: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("no graphs are embedded — the //go:embed patterns in graphs/embed.go matched nothing")
	}
	return names
}

// TestShippedGraphsLoad guards the example graphs as they exist in the repo
// checkout: they must load and pass validation, so a broken example is caught
// in CI rather than at a maintainer's next `oh-my-graph run graphs/...`.
//
// The set is read from the embed FS rather than listed by hand: the embed
// patterns are globs, so a template dropped into graphs/ ships to users
// immediately and must be guarded immediately. The previous hardcoded list had
// silently fallen two files behind.
//
// Loading goes through LoadFile — the path-aware seam `run` itself uses
// (ADR 0013) — rather than Parse over bytes, because a migrated template's
// `use:` resolves against its file's graphs/fragments/ sibling on disk. A
// fragment-free file loads identically either way.
func TestShippedGraphsLoad(t *testing.T) {
	for _, name := range shippedTemplateNames(t) {
		if _, err := LoadFile(filepath.Join("..", "..", "graphs", name)); err != nil {
			t.Errorf("shipped graph %s failed to load: %v", name, err)
		}
	}
}

// TestEmbeddedGraphsLoadFromTheBinarysOwnPayload is the same guarantee for the
// bytes a `go install` user actually receives, which is a different claim: the
// repo checkout has a graphs/fragments/ directory whether or not the binary
// embeds it. Unpacking the embed FS into a scratch directory and loading from
// THERE is what proves the payload is self-sufficient — that every `use:` in a
// shipped template finds its fragment in what the binary carries.
//
// Without this, `//go:embed *.yaml` (which does not descend into
// subdirectories) shipped two templates that died at load with "no fragment
// file at the fragment location", and no test in the repo noticed.
func TestEmbeddedGraphsLoadFromTheBinarysOwnPayload(t *testing.T) {
	root := t.TempDir()
	err := fs.WalkDir(graphs.FS, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := graphs.FS.ReadFile(path)
		if err != nil {
			return err
		}
		target := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("unpack the embedded payload: %v", err)
	}

	names := shippedTemplateNames(t)
	for _, name := range names {
		if _, err := LoadFile(filepath.Join(root, name)); err != nil {
			t.Errorf("embedded graph %s failed to load from the binary's own payload: %v", name, err)
		}
	}
	// A template that cites a fragment is the only reason this test can catch
	// anything the checkout test cannot, so at least one must exist — otherwise
	// the assertion above is satisfiable by a payload with no fragments at all.
	if !anyTemplateCitesAFragment(t, root, names) {
		t.Error("no embedded template cites a fragment — this test can no longer detect a missing fragments/ payload")
	}
}

// TestMergeVerdictRejectsThePromiseFamily pins the one shipped verdict pattern
// whose false PASS is irreversible: `merge-shepherd`'s `merge`, which once
// ended a real run green with nothing merged (ADR 0019 §1).
//
// The pattern is read out of the shipped graph rather than restated here, so
// this test judges what users actually get. It asserts two things at once:
//
//  1. the shipped `^` anchor rejects every promise reply below, and
//  2. the SAME pattern with `(?m)` prefixed ACCEPTS them.
//
// The second assertion is the point. The relaxation to `(?m)` was proposed on
// the theory that the SHA payload is "a fact a promise cannot produce", and it
// is not: seven hex characters are typeable, and `merge-shepherd.yaml` hands
// the model `MERGED 4f2a1c9` as its format example. What `^` actually rejects
// is the preamble a promise cannot do without. If a future change makes these
// replies pass, that theory has been re-adopted by accident.
func TestMergeVerdictRejectsThePromiseFamily(t *testing.T) {
	g, err := LoadFile(filepath.Join("..", "..", "graphs", "merge-shepherd.yaml"))
	if err != nil {
		t.Fatalf("load merge-shepherd: %v", err)
	}
	var pattern string
	for _, n := range g.Graph.Nodes {
		if n.ID == "merge" {
			pattern = n.SuccessCheck.ResultMatches
		}
	}
	if pattern == "" {
		t.Fatal("merge-shepherd's merge node declares no result_matches — the verdict is unchecked")
	}
	if strings.HasPrefix(pattern, "(?m)") {
		t.Fatalf("merge's verdict is line-anchored (%q); ADR 0019 refused that, and the promises below are why", pattern)
	}
	anchored := regexp.MustCompile(pattern)
	perLine := regexp.MustCompile("(?m)" + pattern)

	// Nothing merged in any of these. Each puts a well-formed verdict on a
	// line of its own, which is all a line anchor asks for.
	promises := map[string]string{
		"quotes the reply it will give later":             "CodeRabbit's re-review is mid-flight, so I will merge when it concludes.\nMy final reply will be:\n`MERGED 4f2a1c9 — ADR 0018`",
		"lists both verdicts as plan bullets":             "Plan:\n- `MERGED 4f2a1c9` — the squash merge has landed\n- `WITHHELD <reason>` — I did not merge\nI cannot pick one yet.",
		"quotes the instruction as a code block":          "The instructions say the reply must be:\n\n    MERGED 4f2a1c9\n\nand I cannot produce that until the squash lands.",
		"a to-do list with the SHA filled in later":       "Steps remaining: 1. gh pr merge --squash\nThen:\nMERGED 83edfad — I will fill in the real SHA.",
		"the PR head SHA, which exists before the squash": "Once I squash it the answer will be:\nMERGED 2be7c58 will be the result once I squash it.",
		"a bare WITHHELD with the reason stripped off":    "I have not decided yet.\nWITHHELD:",
	}
	for name, reply := range promises {
		if anchored.MatchString(reply) {
			t.Errorf("merge's verdict pattern ACCEPTS a promise (%s):\n%s", name, reply)
		}
		if name == "a bare WITHHELD with the reason stripped off" {
			continue // rejected for its missing payload, not for its position
		}
		if !perLine.MatchString(reply) {
			t.Errorf("(?m) no longer accepts %q — the counterfactual ADR 0019 rests on has drifted; re-measure before trusting §3", name)
		}
	}

	// The other half: a verdict that opens the reply still passes, both ways.
	for name, reply := range map[string]string{
		"MERGED with a short SHA": "MERGED 52a7373 — ADR 0015 lands.",
		"MERGED with a full SHA":  "MERGED 83edfad11db1ba0281cab9b615c4acadded38512 — ADR 0018 shipped.",
		"WITHHELD with a reason":  "WITHHELD CodeRabbit's re-review has not concluded.",
	} {
		if !anchored.MatchString(reply) {
			t.Errorf("merge's verdict pattern REJECTS a real verdict (%s): %s", name, reply)
		}
	}
}

// TestRecheckVerdictIsThreeValued pins the grammar `merge-shepherd`'s `recheck`
// node exists for. The wait it performs has three outcomes, not two — the
// checks concluded green, something latched that only a person can clear, or a
// self-resolving condition was still moving when the timeout ran out — and the
// whole point of the node is that the third one is neither of the first two:
//
//   - RECHECKED and UNSETTLED pass, so the run reaches the gate either way and
//     the operator decides over a stated verdict;
//   - LATCHED is deliberately NOT in the pattern, so a PR that cannot land
//     halts the run before the gate, the same way triage's BLOCKED does;
//   - every branch carries the SHA, because a verdict about "the checks" that
//     cannot name the commit they ran against is the exact ambiguity this node
//     was added to remove.
//
// The promise family is checked here too, for the same reason as at `merge`:
// position is the lock (ADR 0019 §3), and an UNSETTLED that can be reached by
// preamble is a wait that never happened.
func TestRecheckVerdictIsThreeValued(t *testing.T) {
	g, err := LoadFile(filepath.Join("..", "..", "graphs", "merge-shepherd.yaml"))
	if err != nil {
		t.Fatalf("load merge-shepherd: %v", err)
	}
	var recheck, gate *Node
	for i, n := range g.Graph.Nodes {
		switch n.ID {
		case "recheck":
			recheck = &g.Graph.Nodes[i]
		case "approve-merge":
			gate = &g.Graph.Nodes[i]
		}
	}
	if recheck == nil {
		t.Fatal("merge-shepherd has no recheck node — the wait after triage is gone, and the gate is asking the human to perform it again")
	}
	if gate == nil {
		t.Fatal("merge-shepherd has no approve-merge gate")
	}

	// The chain is the fix: the re-wait sits BETWEEN triage and the gate, so
	// the SHA the operator approves is one something waited on.
	if len(recheck.DependsOn) != 1 || recheck.DependsOn[0] != "triage" {
		t.Errorf("recheck depends on %v, want [triage] — it re-waits for the checks triage's push restarted", recheck.DependsOn)
	}
	if len(gate.DependsOn) != 1 || gate.DependsOn[0] != "recheck" {
		t.Errorf("approve-merge depends on %v, want [recheck] — a gate reached without the re-wait is the defect this node fixes", gate.DependsOn)
	}

	// The narrowest grant that can read check state: `gh pr view` has no
	// mutating form, and nothing else is needed to read a rollup, a head SHA
	// or a review's commit.
	wantTools := []string{"Bash(gh pr view *)", "Bash(sleep *)"}
	if !reflect.DeepEqual(recheck.AllowedTools, wantTools) {
		t.Errorf("recheck's grant is %v, want %v — it only reads check state and sleeps between reads", recheck.AllowedTools, wantTools)
	}

	// The timeout has to outlast the loop budget the prompt hands the node
	// (~25 loops at roughly 40s ≈ 17 minutes). A node killed by its own
	// timeout produces no verdict at all and discards the run — which is the
	// outcome UNSETTLED was invented to prevent, so a timeout that fires
	// before the honest answer is due defeats the node's whole design.
	if got := recheck.TimeoutDuration(); got < 20*time.Minute {
		t.Errorf("recheck's timeout is %s, want at least 20m — its prompt budgets ~17 minutes of polling, and a timeout inside that kills the node before UNSETTLED is due", got)
	}

	pattern := recheck.SuccessCheck.ResultMatches
	if pattern == "" {
		t.Fatal("recheck declares no result_matches — the re-wait's verdict is unchecked")
	}
	if strings.HasPrefix(pattern, "(?m)") {
		t.Fatalf("recheck's verdict is line-anchored (%q); ADR 0019 refused that repo-wide", pattern)
	}
	anchored := regexp.MustCompile(pattern)

	for name, reply := range map[string]string{
		"green, full SHA":  "RECHECKED c80872cfaa805d7e9ec3b3cba9ed4c8cf7f3950f — every rollup entry SUCCESS, coderabbitai APPROVED on that commit.",
		"green, short SHA": "RECHECKED c80872c — nothing was pushed; TRIAGED 0, so the concluded checks are the head's.",
		"timed out":        "UNSETTLED c80872c — stress is still IN_PROGRESS after 20 minutes.",
		// `gh` prints a lowercase oid, but a model retyping one may not, and a
		// false FAIL over the case of a correct verdict buys nothing.
		"green, uppercase SHA": "RECHECKED C80872C — rollup all green.",
	} {
		if !anchored.MatchString(reply) {
			t.Errorf("recheck's pattern REJECTS a real verdict (%s): %s", name, reply)
		}
	}

	for name, reply := range map[string]string{
		// The halt branch. A PR nothing but a person can unstick must not
		// reach the gate, and the way this graph halts a node is by leaving
		// its token out of the pattern. Every one of these carries a
		// well-formed `unblock:` act, so what is being pinned is that the
		// HALT survives a perfectly written LATCHED — not that a malformed
		// one happens to miss.
		"latched on a red check":      "LATCHED c80872c — test concluded FAILURE on the final SHA; unblock: read the job log, fix and push.",
		"latched on the bot's review": "LATCHED c80872c — coderabbitai CHANGES_REQUESTED on that commit; unblock: resolve the review threads, then post @coderabbitai review.",
		"latched on a human's review": "LATCHED c80872c — reviewDecision CHANGES_REQUESTED; unblock: address the review and re-request it.",
		"latched on a rate limit":     "LATCHED c80872c — coderabbitai says the review limit is reached, next review in 14 minutes; unblock: wait 14 minutes, then post @coderabbitai review.",
		"latched on a conflict":       "LATCHED c80872c — mergeStateStatus DIRTY; unblock: rebase onto main and push.",
		"latched on an approval":      "LATCHED c80872c — build_test is QUEUED and never moved; unblock: approve the workflow run in the Actions tab.",
		"the retired halting token":   "BLOCKED c80872c — test concluded FAILURE on the final SHA.",
		"green without a SHA":         "RECHECKED — both checks are green.",
		"timed out without a SHA":     "UNSETTLED — test is still running.",
		"a SHA-shaped word, no id":    "RECHECKED the final commit — test SUCCESS.",
		// A SHA fused to its token is not a verdict anyone writes, and the
		// separator class is `+` rather than `*` so it cannot be read as one.
		"SHA fused to the token": "RECHECKEDc80872c",
		// Position is the lock, exactly as at merge.
		"a promise quoting itself": "test restarted when triage pushed, so I am waiting in the foreground.\nMy final reply will be:\nRECHECKED c80872c",
		"a plan listing both":      "Plan:\n- RECHECKED c80872c if the checks go green\n- UNSETTLED c80872c if they do not\nI cannot pick one yet.",
	} {
		if anchored.MatchString(reply) {
			t.Errorf("recheck's pattern ACCEPTS what it must reject (%s):\n%s", name, reply)
		}
	}
}

// TestBothWaitsSeparateLatchesFromTimeouts pins the half of the latch grammar
// that no regex can hold, which is most of it.
//
// `merge-shepherd` has two polling nodes, and a poll is only honest over a
// condition a machine already at work will clear on its own. The other kind —
// a review that requested changes, a run awaiting a maintainer's approval, a
// required context with no reporter, a conflicting branch, a bot that will not
// review again until it is asked — is cleared by a PERSON, and polling it
// spends the whole timeout to learn nothing and then reports it as slowness.
// ADR 0021 is the rule; this is its enforcement.
//
// Three things are asserted, and none of them is expressible in the
// `result_matches` pattern:
//
//  1. Both waits read the fields GitHub computes for "is this PR ready" —
//     `reviewDecision`, which counts every reviewer including humans, and
//     `mergeStateStatus`. Reading only a `reviews` array filtered to one bot
//     is what let a human's CHANGES_REQUESTED reach `merge` as a RECHECKED.
//  2. Both waits judge the WHOLE check rollup rather than an entry named
//     `test`. The repo this graph was written in carries three entries.
//  3. Both waits name the latch class AND the act that clears it, because a
//     halting verdict that says only what is wrong is what cost this project
//     four hand-repairs in two days.
func TestBothWaitsSeparateLatchesFromTimeouts(t *testing.T) {
	g, err := LoadFile(filepath.Join("..", "..", "graphs", "merge-shepherd.yaml"))
	if err != nil {
		t.Fatalf("load merge-shepherd: %v", err)
	}
	waits := map[string]*Node{}
	for i, n := range g.Graph.Nodes {
		switch n.ID {
		case "ready-and-wait", "recheck":
			waits[n.ID] = &g.Graph.Nodes[i]
		}
	}
	if len(waits) != 2 {
		t.Fatalf("merge-shepherd has %d of its two polling nodes; the latch grammar is stated at both or at neither", len(waits))
	}

	// What both waits must READ. A latch is only classifiable from state the
	// node actually fetched.
	required := map[string]string{
		"reviewDecision":   "GitHub's own answer over EVERY reviewer — a `reviews` array filtered to coderabbitai cannot see a human's CHANGES_REQUESTED",
		"mergeStateStatus": "DIRTY is a latch and BEHIND/BLOCKED is what licenses merge's --admin; neither is visible in the check rollup",
	}
	// What both waits must SAY.
	required["NEVER one check by name"] = "judging an entry called `test` reports a red non-test check as green"
	required["LATCHED"] = "the token that reports a latch as itself instead of as a timeout"
	required["unblock:"] = "a halting verdict that does not name the act is the four-times cost"
	required["@coderabbitai review"] = "the rate-limit latch's act: the clock opens the window, only a re-request opens the review"

	for id, node := range waits {
		for needle, why := range required {
			if !strings.Contains(node.Prompt, needle) {
				t.Errorf("%s's prompt never mentions %q — %s", id, needle, why)
			}
		}
	}

	// The two failing tokens at ready-and-wait are two tokens on purpose: the
	// operator has to tell "do something" from "wait longer" from the first
	// word of the artifact, not from a sentence inside it. Both are rejected
	// by POSITION — neither `L` nor `N` is in the decoration class — so the
	// pattern needs no negative rule to keep them out.
	readyPattern := waits["ready-and-wait"].SuccessCheck.ResultMatches
	if readyPattern == "" {
		t.Fatal("ready-and-wait declares no result_matches — its verdict is unchecked")
	}
	ready := regexp.MustCompile(readyPattern)
	for name, reply := range map[string]string{
		"latched, with the act named": "LATCHED coderabbitai's review limit is reached, next review in 14 minutes; unblock: wait 14 minutes, then post @coderabbitai review.",
		"still in flight":             "NOT READY the `test` check is IN_PROGRESS on 2be7c58 after 15 minutes.",
	} {
		if ready.MatchString(reply) {
			t.Errorf("ready-and-wait's pattern ACCEPTS a failing verdict (%s):\n%s", name, reply)
		}
	}
	if !ready.MatchString("READY test SUCCESS, coderabbitai submitted a review 4 minutes ago.") {
		t.Error("ready-and-wait's pattern REJECTS its own passing verdict")
	}
	if !strings.Contains(waits["ready-and-wait"].Prompt, "NOT READY") {
		t.Error("ready-and-wait names no token for the self-resolving timeout — with only LATCHED written down, a slow check gets reported as a latch or as nothing")
	}

	// merge's --admin licence is the far end of the same read. It may no
	// longer rest on "CodeRabbit concluded", which was true of a construction
	// with no human in it; it rests on recheck having named a mechanical
	// merge state.
	var merge *Node
	for i, n := range g.Graph.Nodes {
		if n.ID == "merge" {
			merge = &g.Graph.Nodes[i]
		}
	}
	if merge == nil {
		t.Fatal("merge-shepherd has no merge node")
	}
	if !strings.Contains(merge.Prompt, "mergeable:") {
		t.Error("merge's --admin clause does not require recheck's mergeable: state — the licence is back to being self-justified")
	}
	if strings.Contains(merge.Prompt, "complete by construction") {
		t.Error("merge still claims the review is \"complete by construction\"; that construction counted CodeRabbit and no human, and it is what licensed --admin past a human's CHANGES_REQUESTED")
	}
}

// findingsVerdict is the reply a review fragment makes when it found a real
// defect — the token its prompt demands, wearing the markdown emphasis a model
// adds unbidden, so a pattern is judged here the way a real reply judges it.
const findingsVerdict = "**FINDINGS:**\n\n- the persisting branch returns without refreshing the projection\n"

// TestAGatingReviewCarriesItsRecoveryArc pins the pair, not the instance
// (issue #151). A review fragment passes on BOTH its verdicts, so `FINDINGS:`
// reaches none of the engine's three "the reviewer rejected the work"
// mechanisms — `retry`, `feedback` and `on_fail` all hang off FAILURE — and a
// graph that wants findings to stop it has exactly one spelling available:
// narrow the review's success_check until `FINDINGS:` fails.
//
// The moment a shipped graph does that, it owes the other half. A narrowed
// check ALONE means the run that most deserves to be believed — the one where
// the reviewer did its job and found something — is the run that reports FAIL,
// with the whole pipeline discarded and nothing repaired. The `feedback:` arc
// is what makes the same failure a repair round instead: dev re-runs with the
// findings, and only an exhausted loop is final. This project already decided
// (merge-shepherd's WITHHELD, docs/LIMITATIONS.md) that a deliberate refusal
// must not read as a broken run; a gating review with no arc is that decision
// reversed by omission.
//
// So the rule is judged by BEHAVIOR, not by authoring form: any node that
// spliced a review fragment and whose effective pattern rejects the findings
// verdict is gating, however it was spelled, and must declare an arc.
//
// Two limits, stated so the name "RecoveryArc" does not over-promise. First,
// this pins the pair's PRESENCE, never that the arc can recover: an arc whose
// `rerun` names a node that cannot repair what the review found passes here,
// and that is the non-converging loop ADR 0010 paid for once (#118). Nothing
// decides it mechanically — `LintFeedbackReach` is the nearest thing and it is
// advisory and fan-in-only; in these graphs it is load rules 3 and 6 that
// happen to refuse every arc that could not reach back. Second, the sweep is
// keyed on the RESOLUTIONS, so it sees only nodes that spliced a `review-*`
// fragment: a review node written out by hand — `adr-driven-dev`'s four
// rounds — is outside it, and would need the fragment (or a widened sweep)
// before this test could speak for it.
func TestAGatingReviewCarriesItsRecoveryArc(t *testing.T) {
	gating := 0
	for _, name := range shippedTemplateNames(t) {
		loaded, err := LoadFile(filepath.Join("..", "..", "graphs", name))
		if err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
		for _, res := range loaded.Resolutions {
			if !strings.HasPrefix(res.Fragment, "review-") {
				continue
			}
			node, ok := loaded.Graph.byID[res.NodeID]
			if !ok {
				t.Fatalf("%s: resolution names node %q, which the graph does not have", name, res.NodeID)
			}
			// Validate compiled this pattern already, so MustCompile cannot
			// fire here — and the match is a SEARCH, the same call the
			// scheduler makes (internal/schedule).
			if regexp.MustCompile(node.SuccessCheck.ResultMatches).MatchString(findingsVerdict) {
				continue // advisory: findings pass, nothing downstream is gated
			}
			gating++
			if node.Feedback == nil {
				t.Errorf("%s: node %q gates on findings (its pattern %q rejects a FINDINGS: reply) but declares no feedback arc — a review that did its job would fail the run instead of buying a repair round; add feedback: { rerun: <implementing node>, max: 1 } or restore the fragment's either-verdict check",
					name, res.NodeID, node.SuccessCheck.ResultMatches)
			}
		}
	}
	if gating == 0 {
		t.Error("no shipped graph gates on review findings any more — this test now asserts nothing; either a lane lost its narrowed success_check, or the gating shape is no longer demonstrated anywhere in graphs/")
	}
}

// qualifierClause is the one unbroken line every shipped prefix verdict carries
// (DESIGN.md, "Verdict patterns"), written on one line precisely so that
// grepping for it is a sweep that cannot silently miss a node.
const qualifierClause = "Anything you need to qualify"

// TestQualifierClauseSweepMatchesDESIGN pins the two numbers DESIGN.md quotes
// for that sweep. They were correct when written and held by nothing: adding a
// prefix-verdict node, or a graph citing one more fragment, moves them, and
// prose that quietly stops being true is worse than prose that was never there
// — the sweep is offered to readers as something they can run and check.
//
// The two counts are different claims, which is why both are here:
//
//   - DECLARATIONS is what the documented grep returns — how many times the
//     clause is WRITTEN across graphs/ and graphs/fragments/.
//   - NODES is how many runtime nodes end up carrying it, which is larger
//     because a fragment states it once for every node that cites the
//     fragment. That gap is the point DESIGN.md is making, so a change that
//     closes it (fragments abandoned, say) should fail here and be re-argued.
func TestQualifierClauseSweepMatchesDESIGN(t *testing.T) {
	const wantDeclarations, wantNodes = 26, 33

	declarations := 0
	for _, dir := range []string{
		filepath.Join("..", "..", "graphs"),
		filepath.Join("..", "..", "graphs", "fragments"),
	} {
		files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
		if err != nil {
			t.Fatalf("glob %s: %v", dir, err)
		}
		for _, file := range files {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read %s: %v", file, err)
			}
			declarations += strings.Count(string(data), qualifierClause)
		}
	}
	if declarations != wantDeclarations {
		t.Errorf("the qualifier-clause sweep counts %d declarations, DESIGN.md says %d — update DESIGN.md's \"Verdict patterns\" section along with the graphs", declarations, wantDeclarations)
	}

	nodes := 0
	for _, name := range shippedTemplateNames(t) {
		loaded, err := LoadFile(filepath.Join("..", "..", "graphs", name))
		if err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
		for _, n := range loaded.Graph.Nodes {
			if strings.Contains(n.Prompt, qualifierClause) {
				nodes++
			}
		}
	}
	if nodes != wantNodes {
		t.Errorf("the clause reaches %d runtime nodes, DESIGN.md says %d — update DESIGN.md's \"Verdict patterns\" section along with the graphs", nodes, wantNodes)
	}
}

// anyTemplateCitesAFragment reports whether any unpacked template resolved at
// least one `use:`, which is what makes the payload's fragments/ directory
// load-bearing.
func anyTemplateCitesAFragment(t *testing.T, root string, names []string) bool {
	t.Helper()
	for _, name := range names {
		loaded, err := LoadFile(filepath.Join(root, name))
		if err == nil && len(loaded.Resolutions) > 0 {
			return true
		}
	}
	return false
}
