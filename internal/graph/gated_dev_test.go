// An external test package, unlike its neighbours: the full `lint` view
// includes the handoff sweeps, and package handoff imports package graph.
package graph_test

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/graphs"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/handoff"
)

const gatedDev = "gated-dev.yaml"

// The two engine-run commands that carry the approval across the gate
// (graphs/gated-dev.yaml, "The pin"), pinned word for word: a reworded command
// that still loads could pin one branch and check another.
const (
	gatedDevPin   = `git update-ref "refs/omg-approved/$(git symbolic-ref --short HEAD)" HEAD`
	gatedDevCheck = `ref="refs/omg-approved/$(git symbolic-ref --short HEAD)"; test "$(git rev-parse HEAD)" = "$(git rev-parse --verify -q "$ref")"`
	gatedDevSpend = `rm -f .omg-pr-body.md; ref="refs/omg-approved/$(git symbolic-ref --short HEAD)"; pin="$(git rev-parse --verify -q "$ref")" && head="$(gh pr view --json headRefOid --jq .headRefOid)" && test "$head" = "$pin" && git update-ref -d "$ref"`
)

// gatedDevPRGrant is pr's whole grant: push and open, and no tool that
// commits, amends or rebases after check-head has passed.
var gatedDevPRGrant = []string{"Read", "Bash(git push *)", "Bash(gh pr create *)", "Bash(gh pr view *)", "Bash(git status*)", "Bash(git log*)", "Edit(./.omg-pr-body.md)"}

// shippedGraphPath is graphs/<name> in the checkout, the path `run` is given.
func shippedGraphPath(name string) string {
	return filepath.Join("..", "..", "graphs", name)
}

// lintWarnings is every advisory `oh-my-graph lint` prints for an
// already-valid graph (cmd/oh-my-graph/lint.go, warnAdvisories), as text.
func lintWarnings(g *graph.Graph) []handoff.Warning {
	warnings := append(handoff.LintPlaceholders(g), handoff.LintSessions(g)...)
	warnings = append(warnings, handoff.LintToolGrants(g)...)
	warnings = append(warnings, handoff.LintVerifyInlining(g)...)
	warnings = append(warnings, handoff.LintFeedbackQuoting(g)...)
	return append(warnings, handoff.LintVerdicts(g)...)
}

// fragmentOf maps each single-node splice in a loaded graph to the fragment
// it came from.
func fragmentOf(loaded *graph.LoadResult) map[string]string {
	from := make(map[string]string, len(loaded.Resolutions))
	for _, res := range loaded.Resolutions {
		if len(res.Spliced) == 0 {
			from[res.NodeID] = res.Fragment
		}
	}
	return from
}

// carriedWarnings is, per fragment, every warning (field and detail, without
// the node id) that a node splicing it draws in the OTHER shipped graphs.
// That is what "a warning the fragment already carries" means, measured
// rather than listed: if gated-dev drew one no other citer of the same
// fragment draws, the warning is gated-dev's own.
func carriedWarnings(t *testing.T) map[string]map[string]bool {
	t.Helper()
	entries, err := fs.ReadDir(graphs.FS, ".")
	if err != nil {
		t.Fatalf("read embedded graphs: %v", err)
	}
	carried := map[string]map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".yaml") || name == gatedDev {
			continue
		}
		loaded, err := graph.LoadFile(shippedGraphPath(name))
		if err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
		from := fragmentOf(loaded)
		for _, w := range lintWarnings(loaded.Graph) {
			if fragment, ok := from[w.NodeID]; ok {
				if carried[fragment] == nil {
					carried[fragment] = map[string]bool{}
				}
				carried[fragment][w.Field+": "+w.Detail] = true
			}
		}
	}
	return carried
}

func loadGatedDev(t *testing.T) *graph.LoadResult {
	t.Helper()
	loaded, err := graph.LoadFile(shippedGraphPath(gatedDev))
	if err != nil {
		t.Fatalf("load %s: %v", gatedDev, err)
	}
	return loaded
}

func gatedDevNode(t *testing.T, g *graph.Graph, id string) graph.Node {
	t.Helper()
	for _, n := range g.Nodes {
		if n.ID == id {
			return n
		}
	}
	t.Fatalf("%s has no node %q", gatedDev, id)
	return graph.Node{}
}

// TestGatedDevLintsCleanWithOneGate_345 holds the shipped gated lane to what
// `lint` says about it: no issue, no gate-description refusal, no fragment
// advisory, and no warning except one a cited fragment already draws wherever
// it is cited. And it holds the one thing the graph exists to add: exactly one
// `type: gate`.
func TestGatedDevLintsCleanWithOneGate_345(t *testing.T) {
	issues, fragmentAdvisories, loaded, err := graph.LintLoadFile(shippedGraphPath(gatedDev))
	if err != nil {
		t.Fatalf("lint %s: %v", gatedDev, err)
	}
	if len(issues) != 0 {
		t.Fatalf("%s has lint issues: %v", gatedDev, issues)
	}
	if refused := handoff.GateDescriptionIssues(loaded.Graph); len(refused) != 0 {
		t.Errorf("%s: lint refuses the gate description: %v", gatedDev, refused)
	}
	if len(fragmentAdvisories) != 0 {
		t.Errorf("%s: fragment advisories: %v", gatedDev, fragmentAdvisories)
	}
	if reach := loaded.Graph.LintFeedbackReach(); len(reach) != 0 {
		t.Errorf("%s: feedback-reach advisories: %v", gatedDev, reach)
	}

	from := fragmentOf(loaded)
	carried := carriedWarnings(t)
	for _, w := range lintWarnings(loaded.Graph) {
		fragment, spliced := from[w.NodeID]
		if !spliced || !carried[fragment][w.Field+": "+w.Detail] {
			t.Errorf("%s: warning %q is the graph's own, not one a cited fragment already carries — fix the graph", gatedDev, w)
		}
	}

	var gates []string
	for _, n := range loaded.Graph.Nodes {
		if n.Type == graph.TypeGate {
			gates = append(gates, n.ID)
		}
	}
	if !slices.Equal(gates, []string{"approve-publish"}) {
		t.Errorf("%s: gates %v, want exactly [approve-publish]", gatedDev, gates)
	}
	if gate := gatedDevNode(t, loaded.Graph, "approve-publish"); gate.Description == "" {
		t.Error("approve-publish carries no description: the approver is not told what they approve (#346)")
	}
}

// TestGatedDevPublishesOnlyThroughTheGateAndTheHeadCheck_345 pins the shape
// the gate is for: every path into pr runs through approve-publish and then
// check-head, pr waits on check-head alone, and check-head on the gate alone.
// A depends_on added to pr would let it publish around the human.
func TestGatedDevPublishesOnlyThroughTheGateAndTheHeadCheck_345(t *testing.T) {
	g := loadGatedDev(t).Graph

	parents := map[string][]string{}
	for _, n := range g.Nodes {
		parents[n.ID] = n.DependsOn
	}
	if got := parents["pr"]; !slices.Equal(got, []string{"check-head"}) {
		t.Errorf("pr depends on %v, want only [check-head]", got)
	}
	if got := parents["check-head"]; !slices.Equal(got, []string{"approve-publish"}) {
		t.Errorf("check-head depends on %v, want only [approve-publish]", got)
	}

	// Every root-to-pr path, walked backwards from pr.
	var paths [][]string
	var walk func(id string, suffix []string)
	walk = func(id string, suffix []string) {
		path := append([]string{id}, suffix...)
		if len(parents[id]) == 0 {
			paths = append(paths, path)
			return
		}
		for _, parent := range parents[id] {
			walk(parent, path)
		}
	}
	walk("pr", nil)
	if len(paths) == 0 {
		t.Fatal("no path reaches pr — this test asserts nothing")
	}
	for _, path := range paths {
		gate := slices.Index(path, "approve-publish")
		check := slices.Index(path, "check-head")
		if gate < 0 || check != gate+1 || check != len(path)-2 {
			t.Errorf("path %v reaches pr without approve-publish → check-head → pr", path)
		}
	}
}

// TestGatedDevPinsTheApprovedHead_345 pins the mechanism the header argues
// for: one managed worktree for every node, the pin written by review's
// engine-run verify behind its gating verdict and arc, the head check as an
// engine-run verify on a read-only node, and the published head checked and
// the pin spent by an engine-run verify on an inline, push-only pr.
func TestGatedDevPinsTheApprovedHead_345(t *testing.T) {
	g := loadGatedDev(t).Graph
	if len(g.Nodes) != 6 {
		t.Errorf("%s has %d nodes, want 6", gatedDev, len(g.Nodes))
	}
	for _, n := range g.Nodes {
		if n.Worktree != "lane" {
			t.Errorf("node %q runs in worktree %q, want lane — the pin is named after the lane's branch", n.ID, n.Worktree)
		}
	}

	review := gatedDevNode(t, g, "review")
	if review.SuccessCheck.Verify == nil || review.SuccessCheck.Verify.Command != gatedDevPin {
		t.Errorf("review's verify is %+v, want the pin %q", review.SuccessCheck.Verify, gatedDevPin)
	}
	if review.SuccessCheck.ResultMatches != "^[*_`\\s]*(MINOR[*_`\\s]*:|CLEAN\\b)" {
		t.Errorf("review's result_matches is %q, want gated-lane's gating pattern", review.SuccessCheck.ResultMatches)
	}
	if review.Feedback == nil || review.Feedback.Rerun != "dev" || review.Feedback.Max != 1 {
		t.Errorf("review's feedback is %+v, want { rerun: dev, max: 1 }", review.Feedback)
	}

	check := gatedDevNode(t, g, "check-head")
	if check.SuccessCheck.Verify == nil || check.SuccessCheck.Verify.Command != gatedDevCheck {
		t.Errorf("check-head's verify is %+v, want the head check %q", check.SuccessCheck.Verify, gatedDevCheck)
	}
	if !slices.Equal(check.AllowedTools, []string{"Read"}) {
		t.Errorf("check-head's allowed_tools is %v, want [Read]", check.AllowedTools)
	}
	if check.SuccessCheck.ResultMatches != "^[*_`\\s]*DONE\\b" {
		t.Errorf("check-head's result_matches is %q, want dev's anchored DONE", check.SuccessCheck.ResultMatches)
	}

	pr := gatedDevNode(t, g, "pr")
	if pr.SuccessCheck.Verify == nil || pr.SuccessCheck.Verify.Command != gatedDevSpend {
		t.Errorf("pr's verify is %+v, want the published-head check %q", pr.SuccessCheck.Verify, gatedDevSpend)
	}
	if !slices.Equal(pr.AllowedTools, gatedDevPRGrant) {
		t.Errorf("pr's allowed_tools is %v, want %v", pr.AllowedTools, gatedDevPRGrant)
	}
	if pr.SuccessCheck.ResultMatches != "^[*_`\\s]*PR[*_`\\s:]*https?://\\S" {
		t.Errorf("pr's result_matches is %q, want pr-publish's PR <url> pattern", pr.SuccessCheck.ResultMatches)
	}
	for _, grant := range gatedDevPRGrant[1:] {
		if !strings.Contains(pr.Prompt, "`"+grant+"`") {
			t.Errorf("pr's prompt does not state %q: a dontAsk node that does not know its grant gives up at the first denial", grant)
		}
	}
}

// TestGatedDevPRIsInline_345: pr-publish grants `Bash(git *)`, which can
// commit past check-head, so gated-dev's pr must not be spliced from it.
func TestGatedDevPRIsInline_345(t *testing.T) {
	if fragment, ok := fragmentOf(loadGatedDev(t))["pr"]; ok {
		t.Errorf("pr is spliced from %q; it must be inline, holding no commit tools", fragment)
	}
}

// grantAllows reports whether one allowed_tools entry would permit command,
// reading a Bash(...) pattern's `*` as "any text" the way the grant does. A
// bare `Bash` allows every command.
func grantAllows(entry, command string) bool {
	if entry == "Bash" {
		return true
	}
	pattern, ok := strings.CutPrefix(entry, "Bash(")
	if !ok {
		return false
	}
	pattern = strings.TrimSuffix(pattern, ")")
	quoted := regexp.QuoteMeta(pattern)
	re := regexp.MustCompile("^" + strings.ReplaceAll(quoted, `\*`, ".*") + "$")
	return re.MatchString(command)
}

// TestGatedDevPRHoldsNoCommitGrant_345 checks every entry of pr's declared
// grant: none is a blanket git or Bash grant, and none would allow a command
// that moves HEAD after check-head has passed.
func TestGatedDevPRHoldsNoCommitGrant_345(t *testing.T) {
	pr := gatedDevNode(t, loadGatedDev(t).Graph, "pr")
	if len(pr.AllowedTools) == 0 {
		t.Fatal("pr declares no allowed_tools; the test would check nothing")
	}
	headMoving := []string{
		"git commit -m x",
		"git commit --amend --no-edit",
		"git rebase main",
		"git rebase -i HEAD~2",
		"git reset --hard HEAD~1",
		"git reset --soft HEAD~1",
		"git checkout main",
		"git checkout -b other",
	}
	for _, entry := range pr.AllowedTools {
		switch entry {
		case "Bash(git *)", "Bash", "Bash(*)":
			t.Errorf("pr's allowed_tools holds the blanket grant %q", entry)
		}
		for _, command := range headMoving {
			if grantAllows(entry, command) {
				t.Errorf("pr's grant %q would allow %q", entry, command)
			}
		}
	}
}

// TestGatedDevPRDoesNotUsePRPublish_345 checks the resolved graph's pr node
// against pr-publish's own grant, so a node spliced from it fails here too.
func TestGatedDevPRDoesNotUsePRPublish_345(t *testing.T) {
	loaded := loadGatedDev(t)
	if fragment, ok := fragmentOf(loaded)["pr"]; ok && fragment == "pr-publish" {
		t.Errorf("pr is spliced from %q", fragment)
	}
	for _, r := range loaded.Resolutions {
		if r.Fragment == "pr-publish" {
			t.Errorf("the graph resolves the fragment pr-publish (%+v)", r)
		}
	}
}

// TestGatedDevPRDeclaresAVerifyAndCheckHeadNeverDeletesThePin_345: pr's engine
// verify is what proves the published head, and check-head must leave the pin
// for it.
func TestGatedDevPRDeclaresAVerifyAndCheckHeadNeverDeletesThePin_345(t *testing.T) {
	g := loadGatedDev(t).Graph
	pr := gatedDevNode(t, g, "pr")
	if pr.SuccessCheck.Verify == nil || strings.TrimSpace(pr.SuccessCheck.Verify.Command) == "" {
		t.Error("pr declares no success_check.verify")
	}
	check := gatedDevNode(t, g, "check-head")
	if check.SuccessCheck.Verify == nil {
		t.Fatal("check-head declares no success_check.verify")
	}
	if strings.Contains(check.SuccessCheck.Verify.Command, "update-ref -d") {
		t.Errorf("check-head's verify deletes the pin: %q", check.SuccessCheck.Verify.Command)
	}
}

// TestGatedDevPRPromptNamesOnlyGrantedCommands_345: every `git …` or `gh …`
// command pr's prompt names in backticks is one its grant admits, so a
// dontAsk node is never told to run what it will be denied.
func TestGatedDevPRPromptNamesOnlyGrantedCommands_345(t *testing.T) {
	pr := gatedDevNode(t, loadGatedDev(t).Graph, "pr")
	spans := regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(pr.Prompt, -1)
	named := 0
	for _, span := range spans {
		command := span[1]
		if !strings.HasPrefix(command, "git ") && !strings.HasPrefix(command, "gh ") {
			continue
		}
		named++
		if !slices.ContainsFunc(pr.AllowedTools, func(entry string) bool { return grantAllows(entry, command) }) {
			t.Errorf("pr's prompt names %q, which its grant %v does not admit", command, pr.AllowedTools)
		}
	}
	if named == 0 {
		t.Fatal("pr's prompt names no git or gh command; the test would check nothing")
	}
}
