// Build evidence for a planned node (ADR 0016): the user supplies ONE shell
// command at invocation, and this file is the trusted code that attaches it to
// the plan's sink nodes AFTER validatePlannedNodes has run.
//
// The shape matters more than the mechanism. #119 was a planned verify node
// that declared `Bash(git *)`, checked the branch name, replied `PASS` and
// certified a branch that did not compile — because a Kotlin node cannot
// declare `Bash(./gradlew *)` at all (layer 0 is Go-shaped), AND because its
// PASS was a `result_matches` verdict, which a node earns by emitting the
// right words. Widening plannedToolAllowlist would fix neither half: a node
// holding the build tool can still run the build, watch it fail, and reply
// `PASS`.
//
// So the command never passes through the planner:
//
//   - the user types it (--verify-cmd), so the source is trusted and the
//     answer is complete for every ecosystem — Elixir, Bazel, Nix and a
//     private bin/verify-everything wrapper are all one string, with no table
//     to maintain;
//   - validatePlannedNodeVerify still rejects a planner-authored `verify:`
//     outright and is untouched. The plan the validator saw never contained
//     one; this file sets the field strictly afterwards, exactly as
//     applyAgentMapping and applySkillMapping set `agent:` and append skill
//     text after their own fields were refused to the plan (ADR 0012 §1);
//   - the ENGINE runs it, through verify.ShellVerifier — the second exec seam,
//     already childenv.Scrub'ed — and judges its exit code itself. No node is
//     granted anything, and every layer of the ADR 0004 ceiling stays
//     byte-for-byte as it is.
//
// Detection (DetectBuildSignals, below) exists only to PRINT a suggestion when
// no command was supplied. It never becomes a grant: `Write` and `Edit` are in
// the allowlist, so node 1 of the same run can legitimately create a
// package.json and a per-node detector would then widen node 2's set — a plan
// bootstrapping its own grant, with no attacker anywhere (ADR 0016 §3).
package coordinator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// maxInjectedVerifyTimeout is the ceiling on --verify-timeout, and the default
// when it is not given. It is graph's own maxVerifyTimeout (unexported there),
// restated here so this package can refuse an over-long timeout at flag-parse
// time instead of only at the re-parse below —
// TestInjectedVerifyTimeoutCeilingMatchesGraph pins the two together, so they
// cannot drift apart silently.
//
// The 2-minute default a hand-written verification gets is deliberately NOT
// used: a cold Gradle, Cargo or Maven build is precisely the case that default
// was not sized for, and a build that trips it fails as an Infrastructure
// fault ("could not verify"), which reads as a broken tool rather than a slow
// one.
const maxInjectedVerifyTimeout = 10 * time.Minute

// VerifyCommand is the user-supplied build evidence command — the whole of
// --verify-cmd / --verify-timeout. The zero value means "no command supplied",
// which is the zero-config path: a run then behaves exactly as it does today,
// plus the printed advice line (VerifyAdvice) and the verdict provenance
// qualifier.
type VerifyCommand struct {
	// Command is the shell command line the engine runs at every sink. It is
	// arbitrary user shell with exactly the standing a hand-written graph's
	// `verify:` has had since ADR 0002 — it runs on the same seam and executes
	// repo-authored code (gradlew, Makefile, npm) the same way the user's own
	// terminal does. Stated, not closed; the provenance difference from a
	// repo-file-derived grant is the whole point: the user chose it.
	Command string
	// Timeout bounds one execution of Command. Zero means
	// maxInjectedVerifyTimeout, which is also the ceiling.
	Timeout time.Duration
}

// Supplied reports whether the user gave a command at all. Whitespace-only is
// deliberately NOT "unset": a --verify-cmd '   ' is a typo the user should
// hear about, not a run that silently verifies nothing, so Validate refuses it.
func (v VerifyCommand) Supplied() bool { return v.Command != "" }

// ResolvedTimeout is the duration one execution actually gets.
func (v VerifyCommand) ResolvedTimeout() time.Duration {
	if v.Timeout <= 0 {
		return maxInjectedVerifyTimeout
	}
	return v.Timeout
}

// Validate refuses a command that cannot be attached. It runs BEFORE the
// planner call (see plan), so a bad flag costs nothing: discovering an
// unusable timeout after paying for a planning call — and after a plan the
// user then has to re-approve — is a worse error than a slow one.
func (v VerifyCommand) Validate() error {
	if !v.Supplied() {
		if v.Timeout != 0 {
			return fmt.Errorf("verify timeout %s was given with no verify command; --verify-timeout bounds --verify-cmd and means nothing without it", v.Timeout)
		}
		return nil
	}
	if strings.TrimSpace(v.Command) == "" {
		return fmt.Errorf("verify command is blank; a whitespace-only command would run the shell for nothing and report success")
	}
	if v.Timeout < 0 {
		return fmt.Errorf("verify timeout must be positive, got %s", v.Timeout)
	}
	if v.Timeout > maxInjectedVerifyTimeout {
		return fmt.Errorf("verify timeout %s exceeds the %s ceiling every verification has; a check on a node's critical path must not be able to wedge a run", v.Timeout, maxInjectedVerifyTimeout)
	}
	return nil
}

// WithVerifyCommand supplies the build evidence command every plan's sink
// nodes get — the `--verify-cmd` / `--verify-timeout` flags' implementation. A
// zero VerifyCommand is the same as not calling this at all.
func WithVerifyCommand(v VerifyCommand) Option {
	return func(c *Coordinator) { c.verifyCommand = v }
}

// VerifyAttachment records one sink node the coordinator attached the user's
// command to. Like AgentMappings and SkillMappings it is a DISCLOSURE: the
// caller prints these with the plan, because trusted code that quietly adds an
// engine-run shell command to a graph the human is about to approve would
// defeat the reason the attachment lives in trusted code at all.
type VerifyAttachment struct {
	// NodeID is the sink the check was attached to.
	NodeID string
	// Command is what will run there — the user's string verbatim, before
	// {{ }} interpolation, which happens per execution at the verify seam.
	Command string
	// Timeout is the resolved bound on that execution.
	Timeout time.Duration
}

// sinkNodeIDs returns every node no other node depends on, in the graph's
// declared order.
//
// This is the attachment set (ADR 0016 §2). graph.Graph has Roots() and
// DependentsOf but no Sinks(), and the derivation lives here rather than there
// because "where a final gate belongs" is auto-mode policy, not graph
// structure — internal/graph knows about topology, not about ceilings.
//
// A DAG always has at least one sink (a graph where every node is depended
// upon contains a cycle, which graph.Validate rejects), and every node is a
// sink or an ancestor of one — which is what makes sink attachment cover every
// mutating node in the run. Both properties are asserted in
// TestSinkNodeIDs_EveryNodeReachesASink rather than assumed here.
//
// Sinks rather than every node: a full build per node costs up to six per run,
// serialized, and it is WRONG for every goal whose point is to reach green — a
// 3-node fix would fail node 1 for not finishing the job alone. The price is
// attribution: a break introduced mid-run is caught at the sink, not at the
// node that caused it.
func sinkNodeIDs(g *graph.Graph) []string {
	depended := make(map[string]bool, len(g.Nodes))
	for _, n := range g.Nodes {
		for _, parent := range n.DependsOn {
			depended[parent] = true
		}
	}
	sinks := make([]string, 0, 1)
	for _, n := range g.Nodes {
		if !depended[n.ID] {
			sinks = append(sinks, n.ID)
		}
	}
	return sinks
}

// attachVerifyCommand is the post-validation mutation itself: it runs strictly
// after applyAgentMapping and strictly BEFORE applySkillActivation, so the
// command lands in the graph that becomes the final Spec and is therefore
// SNAPSHOTTED — `run` and `resume` on the saved graph.json replay the check,
// and `--plan-only` prints it.
//
// The "before activation" half of that ordering is load-bearing and was
// wrong until 2026-08-08: attachVerification re-encodes the graph it is
// handed, so with activation first it marshalled the activation notice into
// plan.Spec and `auto --verify-cmd` wrote a graph.json promising a corpus its
// `run` reader does not have (skillstage.go's rebuildWithNotices).
//
// What that snapshot is worth on resume is what ADR 0016 §4 settles, and both
// halves are in the tree: `resume` calls ReattachVerifyCommand, so a
// snapshot-borne command on an auto graph is never replayed — and `resume`
// registers --verify-cmd, so the user re-supplies it on the resumed leg and an
// auto run carrying build evidence is resumable (#198). The command comes from
// the human on both legs; the run directory is an admissible source on neither.
func (c *Coordinator) attachVerifyCommand(plan *Plan) error {
	if !c.verifyCommand.Supplied() {
		return nil
	}
	g, spec, attachments, err := attachVerification(plan.Graph, c.verifyCommand)
	if err != nil {
		return err
	}
	plan.Graph = g
	plan.Spec = spec
	plan.VerifyAttachments = attachments
	return nil
}

// attachVerification puts v on every sink of g and returns the rebuilt graph,
// its JSON spec, and the attachments it made. A gate sink is skipped: a gate
// reaches a terminal PASS with no subprocess and no predicate — a human
// decided — so there is nothing for an evidence command to be evidence ABOUT,
// and running a build to grade someone's approval would be a category error.
// (Planned graphs cannot contain gates at all; this path is also reached from
// ReattachVerifyCommand, where a hand-written graph can.) A graph whose sinks
// are ALL gates is refused rather than silently left unverified.
//
// The rebuilt graph always goes back through graph.Parse rather than being
// handed over mutated in place. Two things depend on it: Verification.Timeout
// is a STRING whose parsed form is unexported, so a hand-built Verification
// would silently fall back to the 2-minute default at the scheduler; and
// graph.Validate re-checks the timeout ceiling through the same code that
// checks a hand-written graph's.
func attachVerification(g *graph.Graph, v VerifyCommand) (*graph.Graph, []byte, []VerifyAttachment, error) {
	sinks := sinkNodeIDs(g)
	if len(sinks) == 0 {
		// Unreachable for any graph that came through graph.Validate — an
		// acyclic graph always has a sink — so this is a bug in whoever built
		// the graph, and it says so rather than silently attaching nothing.
		return nil, nil, nil, fmt.Errorf("graph %q has no sink node, so the verify command has nowhere to attach", g.Name)
	}
	isSink := make(map[string]bool, len(sinks))
	for _, id := range sinks {
		isSink[id] = true
	}

	timeout := v.ResolvedTimeout()
	attached := make([]VerifyAttachment, 0, len(sinks))
	// Skipping a gate sink is right per-node but wrong for the graph if EVERY
	// sink is one: attaching nothing would hand back a graph the user believes
	// carries their command, and VerifyAdvice prints nothing once a command was
	// supplied — no verification and no warning. Unreachable from Plan (a
	// planned graph cannot contain a gate at all), reachable from
	// ReattachVerifyCommand, which admits hand-written graphs.
	out := *g
	out.Nodes = append([]graph.Node(nil), g.Nodes...)
	for i := range out.Nodes {
		node := &out.Nodes[i]
		if !isSink[node.ID] || node.Type == graph.TypeGate {
			continue
		}
		node.SuccessCheck.Verify = &graph.Verification{
			Command: v.Command,
			Timeout: timeout.String(),
		}
		attached = append(attached, VerifyAttachment{NodeID: node.ID, Command: v.Command, Timeout: timeout})
	}
	if len(attached) == 0 {
		return nil, nil, nil, fmt.Errorf("graph %q ends in gate node(s) only, so the verify command has nowhere to attach: a gate's PASS is a human decision with no subprocess for an evidence command to be evidence about", g.Name)
	}

	spec, err := json.Marshal(&out)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("re-encode verify-attached graph: %w", err)
	}
	reparsed, err := graph.Parse(spec)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("re-parse verify-attached graph: %w", err)
	}
	return reparsed, spec, attached, nil
}

// InjectedVerifyNodes is the set of node ids whose verifications a run must
// execute one at a time — schedule.Options.SerializedVerifyNodes.
//
// It reads the GRAPH rather than a Plan's VerifyAttachments so both legs of a
// run can ask the same question with the same answer: a fresh leg holds a Plan,
// a resumed leg holds only a graph rebuilt from the run directory. The
// derivation is exact for a planned graph and ONLY for one — validatePlannedNodeVerify
// refuses a planner-authored `verify:`, so every verification a planned graph
// carries was attached by attachVerification. A hand-written graph's
// verifications are the user's own reviewed artifact and keep running
// concurrently, so the caller passes a planned graph only; its discriminator is
// the auto-mode tool ceiling, non-empty exactly for a plan.
//
// The serialization is LOAD-BEARING, not a nicety, and relaxing it needs a
// replacement argument rather than a benchmark (ADR 0016 §2). Two reasons, and
// the ordering one is NOT among them:
//
//   - flake: two concurrent `./gradlew build` invocations in one project
//     directory contend for the build daemon's locks, and a flaky check is
//     worse than a slow one;
//   - a check must not read a tree another check's build is WRITING. A build
//     is not a read-only observer — it writes build/, target/, node_modules/,
//     generated sources — so two concurrent builds of one directory can each
//     fail on the other's half-written output, and neither result describes
//     the tree the user has.
//
// What it does NOT carry is the ordering claim, which holds with or without
// it: a node's check runs after that node's own subprocess, every non-sink is
// an ancestor of a sink (TestSinkNodeIDs_EveryNodeReachesASink), and an
// ancestor ends before its descendant starts — so by the time the
// last-STARTING check begins, every node's subprocess has already ended,
// serialized or not. That, plus the fact that a failed sink fails the run under
// EITHER failure policy (a continue-on-fail failure still lands in
// prunedFailures, which still returns *RunFailedError, so `on_fail: continue`
// cannot buy a plan a passing run), is what carries "a passing run means the
// final tree passed the user's command".
func InjectedVerifyNodes(g *graph.Graph) map[string]bool {
	ids := make(map[string]bool, len(g.Nodes))
	for _, n := range g.Nodes {
		if n.SuccessCheck.Verify != nil {
			ids[n.ID] = true
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return ids
}

// SnapshotVerifyError is `resume`'s refusal of a verification it found in a
// run directory rather than on the command line (ADR 0016 §4).
//
// Before ADR 0016, "an auto snapshot contains no success_check.verify" was a
// true, cheap, checkable assertion. §2 makes a verify legitimate in a planned
// snapshot and so forecloses it, which changes the consequence of tampering
// with a run directory in KIND, not degree: from "confuse the scheduler" to
// "engine-run shell outside every ceiling" — precisely what
// validatePlannedNodeVerify exists to prevent. And the writer need not be an
// outsider: a planned node may hold bare Write/Edit, and validatePlannedNodeCwd
// already concedes it "does NOT make a write-capable node safe".
//
// ReattachVerifyCommand restores the assertion, and `resume` calls it: a
// snapshot-borne command is dropped, and one that was there while this
// invocation supplies nothing raises this error.
//
// The error is a REMEDY, not a dead end, and that distinction is #198: it names
// --verify-cmd, and `resume` registers --verify-cmd, so the sentence below
// describes a command the tool accepts. It did not until 2026-08-18 — the flag
// lane shipped the pair on `auto` only — and a message that sends someone down
// a dead end costs more than silence, because the next message is not believed
// either. Anything added here must stay checkable against
// cmd/oh-my-graph's resume FlagSet, which
// TestSnapshotVerifyRefusal_NamesOnlyFlagsResumeRegisters does automatically.
type SnapshotVerifyError struct {
	NodeIDs []string
}

func (e *SnapshotVerifyError) Error() string {
	return fmt.Sprintf(
		"the saved graph carries success_check.verify on node(s) %s, which auto mode never accepts from a run directory; re-supply it on this resume with --verify-cmd so the command comes from you rather than from disk",
		strings.Join(e.NodeIDs, ", "),
	)
}

// ReattachVerifyCommand is the resume-side counterpart of attachVerifyCommand:
// it takes a graph reconstructed from a run directory and returns one whose
// injected verifications came from THIS invocation, never from the file.
//
// Every snapshot-borne verification is dropped, and a graph that carried one
// while v supplies nothing is refused outright with a *SnapshotVerifyError —
// silently dropping it would resume a run with strictly weaker checking than
// the leg it continues, which is the failure this whole ADR is about.
//
// When v DOES supply a command the leg gets it at the same sinks a fresh leg's
// would land on, under the same ceiling, through this same trusted code. That
// is the whole of what `resume --verify-cmd` buys, and deliberately not one
// path more: there is no attachment here a fresh `auto` could not have made.
//
// It is deliberately auto-mode-only, and the caller decides that: a
// hand-written graph's `verify:` is the user's own reviewed artifact and must
// keep round-tripping untouched. The auto/hand-written discriminator a resume
// has is the snapshot's ToolPolicies — non-empty exactly for a planned graph.
func ReattachVerifyCommand(g *graph.Graph, v VerifyCommand) (*graph.Graph, []VerifyAttachment, error) {
	if err := v.Validate(); err != nil {
		return nil, nil, err
	}
	stripped, dropped := stripVerifications(g)
	if !v.Supplied() {
		if len(dropped) > 0 {
			return nil, nil, &SnapshotVerifyError{NodeIDs: dropped}
		}
		// Nothing was carried and nothing was asked for: the graph the caller
		// already holds is the answer, byID and parsed timeouts intact.
		return g, nil, nil
	}
	attached, _, attachments, err := attachVerification(stripped, v)
	if err != nil {
		return nil, nil, err
	}
	return attached, attachments, nil
}

// stripVerifications returns g with every success_check.verify removed, and
// the sorted ids of the nodes that carried one.
func stripVerifications(g *graph.Graph) (*graph.Graph, []string) {
	out := *g
	out.Nodes = append([]graph.Node(nil), g.Nodes...)
	var dropped []string
	for i := range out.Nodes {
		if out.Nodes[i].SuccessCheck.Verify == nil {
			continue
		}
		dropped = append(dropped, out.Nodes[i].ID)
		out.Nodes[i].SuccessCheck.Verify = nil
	}
	sort.Strings(dropped)
	return &out, dropped
}

// BuildSignal is one build-system marker found in the invocation directory,
// and the command it suggests.
type BuildSignal struct {
	// File is the marker that was found, relative to the scanned directory.
	File string
	// Ecosystem is how the advice line names it ("a Gradle project").
	Ecosystem string
	// SuggestedCommand is the --verify-cmd value the advice offers.
	SuggestedCommand string
}

// buildSignals is the detection table, in priority order: a wrapper script
// beats the build file it wraps, and a repo raising several signals is
// reported with all of them (a monorepo is undecidable, and picking one
// silently would be wrong rather than merely unhelpful).
//
// This table is ALLOWED to be incomplete, and that is the point of §3: it
// produces PROSE, never policy. A repo whose build is a private wrapper script
// raises no signal any detector will ever know, and it is exactly as well
// served as a Gradle repo — its user types their own command. Detection
// converts "the author's stack leaks into your ceiling" into "the author's
// list of stacks leaks into a printed suggestion", which costs at worst one
// inaccurate line.
var buildSignals = []BuildSignal{
	{File: "gradlew", Ecosystem: "a Gradle project", SuggestedCommand: "./gradlew build"},
	{File: "build.gradle.kts", Ecosystem: "a Gradle project", SuggestedCommand: "gradle build"},
	{File: "build.gradle", Ecosystem: "a Gradle project", SuggestedCommand: "gradle build"},
	{File: "pom.xml", Ecosystem: "a Maven project", SuggestedCommand: "mvn -q verify"},
	{File: "Cargo.toml", Ecosystem: "a Cargo project", SuggestedCommand: "cargo test"},
	{File: "package.json", Ecosystem: "a Node project", SuggestedCommand: "npm test"},
	{File: "pyproject.toml", Ecosystem: "a Python project", SuggestedCommand: "python -m pytest"},
	{File: "Gemfile", Ecosystem: "a Ruby project", SuggestedCommand: "bundle exec rake"},
	{File: "mix.exs", Ecosystem: "an Elixir project", SuggestedCommand: "mix test"},
	{File: "go.mod", Ecosystem: "a Go module", SuggestedCommand: "go build ./..."},
	{File: "Makefile", Ecosystem: "a Makefile project", SuggestedCommand: "make"},
	{File: "WORKSPACE", Ecosystem: "a Bazel workspace", SuggestedCommand: "bazel build //..."},
	{File: "flake.nix", Ecosystem: "a Nix flake", SuggestedCommand: "nix flake check"},
}

// csprojGlob is the one signal that is a pattern rather than a name.
const csprojGlob = "*.csproj"

// DetectBuildSignals reports which build markers dir holds, in the table's
// priority order. It reads nothing but directory entries: the file's CONTENT
// never influences anything, because the output is a printed sentence and a
// suggestion the human then chooses to accept or not.
//
// An unreadable or missing directory yields no signals — "detected nothing" is
// a diagnosable state the advice line states out loud, not an error.
func DetectBuildSignals(dir string) []BuildSignal {
	found := make([]BuildSignal, 0, 1)
	for _, signal := range buildSignals {
		if info, err := os.Stat(filepath.Join(dir, signal.File)); err == nil && !info.IsDir() {
			found = append(found, signal)
		}
	}
	if matches, err := filepath.Glob(filepath.Join(dir, csprojGlob)); err == nil && len(matches) > 0 {
		found = append(found, BuildSignal{
			File:             filepath.Base(matches[0]),
			Ecosystem:        "a .NET project",
			SuggestedCommand: "dotnet build",
		})
	}
	return found
}

// VerifyAdvice is the line printed when no --verify-cmd was supplied: what the
// run will NOT check, and the one flag that would change that.
//
// It prints whether or not a signal was found — "detected nothing" is the
// diagnosable case, the same reasoning ADR 0012 §6 used to print `Found: 0`
// rather than imply it — and it returns "" only when a command WAS supplied,
// so a caller can print unconditionally.
//
// Detection informs; the human authorizes. Nothing here reads a file to decide
// a grant, and nothing here runs.
func VerifyAdvice(v VerifyCommand, signals []BuildSignal) string {
	if v.Supplied() {
		return ""
	}
	var b strings.Builder
	b.WriteString("No build verification configured. ")
	switch len(signals) {
	case 0:
		b.WriteString("Detected no build signal in this directory.\n")
	case 1:
		fmt.Fprintf(&b, "Detected %s (%s).\n", signals[0].Ecosystem, signals[0].File)
	default:
		names := make([]string, 0, len(signals))
		for _, s := range signals {
			names = append(names, s.File)
		}
		fmt.Fprintf(&b, "Detected several build signals (%s), so this is a guess.\n", strings.Join(names, ", "))
	}
	b.WriteString("Nothing in this run will compile or test your code — a planned node cannot\n")
	b.WriteString("run a build command, and no node's PASS carries build evidence.\n")
	b.WriteString("Re-run with\n")
	suggestion := "'<your build command>'"
	if len(signals) > 0 {
		suggestion = "'" + signals[0].SuggestedCommand + "'"
	}
	fmt.Fprintf(&b, "  --verify-cmd %s\n", suggestion)
	b.WriteString("to have the engine verify the result itself.\n")
	return b.String()
}
