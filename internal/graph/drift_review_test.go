package graph

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// driftNode returns the one runtime node every shipped template splices from
// fragment's internal node local, failing unless exactly one exists: today
// drift-review is the only citer, and a second would need its own look at
// which binding these pins hold it to.
func driftNode(t *testing.T, fragment, local string) Node {
	t.Helper()
	nodes := splicedNodes(t, fragment, local)
	if len(nodes) != 1 {
		t.Fatalf("%d shipped nodes splice %s's %s, want exactly 1", len(nodes), fragment, local)
	}
	return nodes[0]
}

// driftVerdict is node's shipped result_matches, compiled, refused when it is
// missing or line-anchored (ADR 0019 §3: position is the lock).
func driftVerdict(t *testing.T, n Node) *regexp.Regexp {
	t.Helper()
	pattern := n.SuccessCheck.ResultMatches
	if pattern == "" {
		t.Fatalf("%s declares no result_matches — its verdict is unchecked", n.ID)
	}
	if strings.HasPrefix(pattern, "(?m)") {
		t.Fatalf("%s's verdict is line-anchored (%q); ADR 0019 refused that repo-wide", n.ID, pattern)
	}
	return regexp.MustCompile(pattern)
}

// TestDriftFragmentsLoad_386 loads each drift fragment on its own, the way the
// reuse catalog does, rather than only through drift-review: a fragment whose
// declared slot the body never reads, or whose node list drifted, is caught
// here with the fragment's own name.
func TestDriftFragmentsLoad_386(t *testing.T) {
	root := filepath.Join("..", "..", "graphs")
	for _, tc := range []struct {
		name  string
		binds []string
		ids   []string
	}{
		{"drift-check", []string{"design_path", "requirements_path", "baseline_command", "base", "head", "check_timeout"}, []string{"baseline", "check"}},
		{"drift-nego", []string{"check_report", "design_path", "verify_command"}, []string{"interview", "rule", "fix"}},
	} {
		got, err := InspectFragment(root, tc.name)
		if err != nil {
			t.Errorf("%s failed to load: %v", tc.name, err)
			continue
		}
		if !slices.Equal(got.Binds, tc.binds) {
			t.Errorf("%s binds %v, want %v — every declared slot must be read", tc.name, got.Binds, tc.binds)
		}
		if !slices.Equal(got.IDs, tc.ids) {
			t.Errorf("%s declares nodes %v, want %v", tc.name, got.IDs, tc.ids)
		}
		if len(got.NestedUses) != 0 || len(got.Advisories) != 0 {
			t.Errorf("%s: nested uses %v, advisories %v, want none", tc.name, got.NestedUses, got.Advisories)
		}
	}
}

// TestDriftReviewSplicesFiveNodes_386 pins the wiring drift-review exists for:
// two citations, five nodes in one chain — the baseline before the check — and the check's report reaching both
// the interview and the ruling under the check node's real id. The graph can
// only write the bare citing id `drift` (an entry graph may not type `/`), so
// the rewrite to `drift/check` is the engine's, and is what this holds.
func TestDriftReviewSplicesFiveNodes_386(t *testing.T) {
	loaded, err := LoadFile(filepath.Join("..", "..", "graphs", "drift-review.yaml"))
	if err != nil {
		t.Fatalf("load drift-review: %v", err)
	}
	if len(loaded.Resolutions) != 2 {
		t.Errorf("drift-review resolves %d uses, want 2: %+v", len(loaded.Resolutions), loaded.Resolutions)
	}
	var ids []string
	for _, n := range loaded.Graph.Nodes {
		ids = append(ids, n.ID)
	}
	if want := []string{"drift/baseline", "drift/check", "nego/interview", "nego/rule", "nego/fix"}; !slices.Equal(ids, want) {
		t.Fatalf("drift-review's nodes are %v, want %v", ids, want)
	}
	for id, parents := range map[string][]string{
		"drift/baseline": nil,
		"drift/check":    {"drift/baseline"},
		"nego/interview": {"drift/check"},
		"nego/rule":      {"nego/interview"},
		"nego/fix":       {"nego/rule"},
	} {
		n, _ := loaded.Graph.NodeByID(id)
		if !slices.Equal(n.DependsOn, parents) {
			t.Errorf("%s depends on %v, want %v", id, n.DependsOn, parents)
		}
	}
	for _, id := range []string{"nego/interview", "nego/rule"} {
		n, _ := loaded.Graph.NodeByID(id)
		if !strings.Contains(n.Prompt, "artifacts.drift/check | inline") {
			t.Errorf("%s's prompt never quotes the check's report inline as artifacts.drift/check", id)
		}
	}
}

// TestDriftNegoOneDirection_386 pins the split drift-nego is built on: the
// check, the interview and the ruling only read — under plan, holding no Edit
// or Write — so no node both argues and acts, and only fix edits. Every node
// is a fresh session handing on an artifact: the interview is NOT the author's
// session resumed (#386's open question), and no node resumes another.
func TestDriftNegoOneDirection_386(t *testing.T) {
	readers := []Node{
		driftNode(t, "drift-check", "check"),
		driftNode(t, "drift-nego", "interview"),
		driftNode(t, "drift-nego", "rule"),
	}
	for _, n := range readers {
		if n.PermissionMode != PermissionPlan {
			t.Errorf("%s runs %q, want plan — it reports or rules, and must not act", n.ID, n.PermissionMode)
		}
		for _, tool := range n.AllowedTools {
			if strings.HasPrefix(tool, "Edit") || strings.HasPrefix(tool, "Write") {
				t.Errorf("%s holds %s — a read-only node holds no write tool", n.ID, tool)
			}
		}
	}
	for _, n := range append(readers, driftNode(t, "drift-nego", "fix")) {
		if n.Handoff == HandoffSession {
			t.Errorf("%s hands off by session — every drift node is a fresh session", n.ID)
		}
	}
	if rule := driftNode(t, "drift-nego", "rule"); slices.Contains(rule.Retry.On, CauseResultMismatch) {
		t.Errorf("rule retries on %s — a second attempt could turn the AMEND_DESIGN: stop into a pass", CauseResultMismatch)
	}
}

// TestDriftCheckVerdictIsTwoValued_386 pins check's grammar: NO_DRIFT or
// DRIFT:, both passing — a check judges, it does not gate — and nothing else,
// with no preamble before the token.
func TestDriftCheckVerdictIsTwoValued_386(t *testing.T) {
	anchored := driftVerdict(t, driftNode(t, "drift-check", "check"))

	for name, reply := range map[string]string{
		"no drift":            "NO_DRIFT",
		"no drift, qualified": "NO_DRIFT — the change only touches tests.",
		"drift with a list":   "DRIFT:\n```json\n[{\"id\": \"D1\"}]\n```",
		"emphasised no drift": "**NO_DRIFT**",
		"emphasised drift":    "**DRIFT:** see below",
		"colon outside bold":  "**DRIFT**: see below",
	} {
		if !anchored.MatchString(reply) {
			t.Errorf("check's pattern REJECTS a real verdict (%s): %q", name, reply)
		}
	}
	for name, reply := range map[string]string{
		"another first token":   "PASS — nothing drifted.",
		"a bare DRIFT":          "DRIFT the gate admits more than DESIGN.md says",
		"a longer word":         "NO_DRIFTING here",
		"a preamble line":       "I held the change against the design.\nNO_DRIFT",
		"a promise of a report": "Still checking; my final reply will be:\nDRIFT: [...]",
	} {
		if anchored.MatchString(reply) {
			t.Errorf("check's pattern ACCEPTS what it must reject (%s): %q", name, reply)
		}
	}
}

// TestDriftRuleRefusesAmendDesign_386 pins the stop drift-nego uses in place
// of a conditional gate: rule passes NO_DRIFT and RESOLVED:, and FAILS on
// AMEND_DESIGN: however it is emphasised, so halt-on-fail stops the run before
// fix for a human to amend the design.
func TestDriftRuleRefusesAmendDesign_386(t *testing.T) {
	anchored := driftVerdict(t, driftNode(t, "drift-nego", "rule"))

	for name, reply := range map[string]string{
		"no drift":          "NO_DRIFT",
		"resolved":          "RESOLVED:\n```json\n[{\"id\": \"D1\", \"outcome\": \"fix-code\"}]\n```",
		"emphasised":        "**RESOLVED:** every item is fix-code",
		"colon outside":     "**RESOLVED**: every item is fix-code",
		"emphasised clean":  "**NO_DRIFT**",
		"resolved, trailer": "RESOLVED: [] — qualified after the verdict",
	} {
		if !anchored.MatchString(reply) {
			t.Errorf("rule's pattern REJECTS a passing ruling (%s): %q", name, reply)
		}
	}
	for name, reply := range map[string]string{
		"amend design":            "AMEND_DESIGN:\n```json\n[{\"id\": \"D1\", \"outcome\": \"amend-design\"}]\n```",
		"emphasised amend design": "**AMEND_DESIGN:** D1 is the design's fault",
		"a bare RESOLVED":         "RESOLVED every item",
		"a preamble line":         "Having weighed both sides:\nRESOLVED: []",
	} {
		if anchored.MatchString(reply) {
			t.Errorf("rule's pattern ACCEPTS what it must refuse (%s): %q", name, reply)
		}
	}
}

// TestDriftNegoFixIsToldTheGrantItHolds_386 is #294's rule for drift-nego's
// fix, copied from repair-round's apply. One difference: drift-review binds
// the evidence command from an input, so the shipped command is the token
// `{{ inputs.verify }}`, which grantAdmits cannot judge. What can be held is
// that the prompt names that same command and the grant holds `make`, the
// program the graph's header tells the user to bind.
func TestDriftNegoFixIsToldTheGrantItHolds_386(t *testing.T) {
	fix := driftNode(t, "drift-nego", "fix")
	assertPromptStatesBashGrant(t, fix)
	if fix.SuccessCheck.Verify == nil {
		t.Fatalf("%s declares no verify — the evidence command the prompt names is gone", fix.ID)
	}
	command := fix.SuccessCheck.Verify.Command
	if command != "{{ inputs.verify }}" {
		t.Errorf("%s is judged by %q, want drift-review's input {{ inputs.verify }} — never an artifact", fix.ID, command)
	}
	if !strings.Contains(fix.Prompt, "`"+command+"`") {
		t.Errorf("%s's prompt never names its evidence command `%s` as the test run", fix.ID, command)
	}
	if !slices.Contains(fix.AllowedTools, "Bash(make *)") {
		t.Errorf("%s's grant %v holds no Bash(make *) — it cannot run the make command it is judged by", fix.ID, fix.AllowedTools)
	}
	if !strings.Contains(fix.Prompt, "make -C <dir>") {
		t.Errorf("%s never names `make -C <dir>`, so `cd <dir> && make` is what it reaches for", fix.ID)
	}
	if grantAdmits(fix.AllowedTools, "go test ./...") {
		t.Errorf("grantAdmits admits `go test` under fix's grant %v", fix.AllowedTools)
	}
}

// TestDriftBaselineGatesTheCheck_386 pins the deterministic half of the
// review: drift/baseline runs before drift/check, holds no write tool, and is
// judged by the ENGINE running the bound baseline command — not by a model —
// while the check is pointed at the bound requirements file.
func TestDriftBaselineGatesTheCheck_386(t *testing.T) {
	loaded, err := LoadFile(filepath.Join("..", "..", "graphs", "drift-review.yaml"))
	if err != nil {
		t.Fatalf("load drift-review: %v", err)
	}
	baseline, ok := loaded.Graph.NodeByID("drift/baseline")
	if !ok {
		t.Fatal("drift-review has no drift/baseline node")
	}
	check, _ := loaded.Graph.NodeByID("drift/check")
	if !slices.Contains(check.DependsOn, "drift/baseline") {
		t.Errorf("drift/check depends on %v — it must follow drift/baseline", check.DependsOn)
	}
	for _, tool := range baseline.AllowedTools {
		if strings.HasPrefix(tool, "Edit") || strings.HasPrefix(tool, "Write") {
			t.Errorf("drift/baseline holds %s — the baseline session must not act", tool)
		}
	}
	if baseline.SuccessCheck.Verify == nil {
		t.Fatal("drift/baseline declares no verify — the deterministic half is unchecked")
	}
	if got := baseline.SuccessCheck.Verify.Command; got != "{{ inputs.baseline }}" {
		t.Errorf("drift/baseline is judged by %q, want the bound input {{ inputs.baseline }}", got)
	}
	if !strings.Contains(check.Prompt, "{{ inputs.requirements }}") {
		t.Errorf("drift/check's prompt never names the bound requirements file {{ inputs.requirements }}")
	}
}

// TestDriftRetriesOnlyWhatDoesNotRule_386 pins the drift fragments' retry
// policy: check and interview retry on result_mismatch, because a misplaced
// token is a formatting slip and neither node rules, while rule never does —
// its AMEND_DESIGN: failure is the stop for a human, and a retry on mismatch
// would re-roll the ruling until it passes.
func TestDriftRetriesOnlyWhatDoesNotRule_386(t *testing.T) {
	for _, n := range []Node{
		driftNode(t, "drift-check", "check"),
		driftNode(t, "drift-nego", "interview"),
	} {
		if n.Retry == nil || !slices.Contains(n.Retry.On, CauseResultMismatch) {
			t.Errorf("%s does not retry on %s — one misplaced token halts the run", n.ID, CauseResultMismatch)
		}
	}
	rule := driftNode(t, "drift-nego", "rule")
	if rule.Retry != nil && slices.Contains(rule.Retry.On, CauseResultMismatch) {
		t.Errorf("%s retries on %s %v — a retry could turn AMEND_DESIGN: into a pass", rule.ID, CauseResultMismatch, rule.Retry.On)
	}
}
