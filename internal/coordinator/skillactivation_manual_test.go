//go:build manual

package coordinator

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/runner"
)

// The standing guard ADR 0017's Failure-modes owes and never got: activation is
// "one flag-semantics change away from yielding nothing, and unlike inlining
// there is no printed line that would look different — the plan says ENABLED
// either way". Run against a REAL `claude` on the user's own subscription; it
// costs a few cents and is MANUAL ONLY, never CI (the `make smoke` posture):
//
//	go test -tags manual ./internal/coordinator -run TestManual_SkillActivation -v -count=1
//
// Two things are asserted, and the ADR's own update says why BOTH are needed:
// the acceptance run "found the run indistinguishable from silent absence from
// the outside — HTML written, every node PASS", so a test that checks only the
// outcome would certify a wiring regression as "the model chose no skill".
//
//   - the ARGV half, at the seam a real node spawns through: layer 1 still "",
//     `Skill` in `--tools`, `--plugin-dir` pointing at what this plan staged;
//   - the ACTIVATION half, judged ONLY by whether a planted skill's marker file
//     appears on disk. Never by what the model says about itself — #130's own
//     rule, arrived at after a self-report probe answered YES then NO to
//     identical argv.
//
// The corpus is planted rather than the user's real `~/.claude/skills`, so the
// test measures the mechanism and not one machine's skill descriptions.

// activationProbeSpec is a one-node plan whose prompt is written in the
// planner's own register — a terse, fully specified imperative that prescribes
// its own tool. That register is the thing the 2026-08-08 measurement found
// scores 0 of 9 against a corpus of marginal fit, so a probe written in a
// friendlier one would flatter the mechanism.
const activationProbeSpec = `{"name":"activation-probe","version":"1","nodes":[` +
	`{"id":"notes","prompt":"Read CHANGES.txt in the current working directory and produce RELEASE-NOTES.md ` +
	`in the same directory: the release notes for version 0.2.0 of a small local job runner. Group every line ` +
	`of CHANGES.txt under exactly one of the headings Added, Changed, Fixed, Removed, in that order. Every ` +
	`entry is a single bullet, past tense, no trailing period. Write the file with the Write tool.",` +
	`"allowed_tools":["Read","Write"]}]}`

// probeMarker is the file the planted skill's procedure writes. Its EXISTENCE
// is the whole verdict: a skill body that ran leaves it, a skill body that was
// never loaded cannot.
const probeMarker = "OMG-PROBE-FIRED.txt"

// plantFittingSkill writes a corpus of one whose DESCRIPTION is a genuine
// trigger for activationProbeSpec's task, and whose first instruction is the
// marker. The description is the variable under test: measurement H held the
// prompt byte-identical and varied only this, taking activation from 0 of 9 to
// 3 of 3, which is what established that the descriptions reach a `-p` node
// under layer 1 = "" and are matched there.
func plantFittingSkill(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeSkillFile(t, dir, "omg-probe-release-notes",
		"name: omg-probe-release-notes\ndescription: Use this skill whenever writing release notes, a CHANGELOG "+
			"entry, or a version history section for a software release. It fixes the required section order, the "+
			"wording convention for each entry type, and how a release is dated. Triggers - release notes, "+
			"RELEASE-NOTES.md, changelog entry, version history.",
		"# Release notes procedure\n\n1. FIRST, create the file `"+probeMarker+"` in the current working directory "+
			"containing the single line OMG-PROBE-RELEASE-NOTES. This is the procedure's required audit stamp and "+
			"must be written before anything else.\n2. Then write the release notes, sections in the order Added, "+
			"Changed, Fixed, Removed.\n")
	return dir
}

// probeCwd is a working directory holding the one file the probe's task reads.
func probeCwd(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	body := "- worker pool now bounded to NCPU-2\n- fixed a hang when the queue drained during shutdown\n" +
		"- dropped the --legacy-poll flag\n- added a --drain-timeout flag\n"
	if err := os.WriteFile(filepath.Join(dir, "CHANGES.txt"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// policyRecorder captures the invocation a node is really spawned with and then
// spawns it for real. It is what makes the argv half assertable without a PATH
// shim: runner.ToolPolicy is exactly what buildArgs renders, and
// TestBuildCmd_Argv already pins that rendering.
type policyRecorder struct {
	next runner.NodeRunner
	got  runner.NodeInvocation
}

func (r *policyRecorder) Run(ctx context.Context, spec runner.NodeInvocation) (runner.NodeOutcome, error) {
	r.got = spec
	return r.next.Run(ctx, spec)
}

// runActivationProbe plans activationProbeSpec against the planted corpus (the
// planner call itself is faked — this measures a NODE, not a plan), binds the
// staging into a real run directory, and spawns the node through the real CLI
// under the real policy, guarded exactly as the scheduler guards it.
func runActivationProbe(t *testing.T, opts ...Option) (runner.NodeInvocation, string) {
	t.Helper()
	fake, _ := newPlannerFake(runner.NodeOutcome{Result: activationProbeSpec})
	plan, err := New(fake, opts...).Plan(context.Background(), "write the release notes", nil)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := plan.BindSkillStaging(t.TempDir()); err != nil {
		t.Fatalf("BindSkillStaging: %v", err)
	}
	node, ok := plan.Graph.NodeByID("notes")
	if !ok {
		t.Fatal("no node notes")
	}
	var staging *SkillStaging
	if plan.SkillActivation != nil {
		staging = plan.SkillActivation.Staging
	}
	rec := &policyRecorder{next: GuardStaging(runner.NewClaudeCLIRunner(), staging)}

	cwd := probeCwd(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	outcome, err := rec.Run(ctx, runner.NodeInvocation{
		Prompt:         node.Prompt,
		Cwd:            cwd,
		PermissionMode: "dontAsk",
		Policy:         plan.ToolPolicies["notes"],
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	t.Logf("node exit %d, cost $%.4f", outcome.ExitCode, outcome.TotalCostUSD)
	return rec.got, cwd
}

// TestManual_SkillActivationStillFires is the regression this whole mechanism
// needs and the plan printout cannot give: with the definitions staged, the
// tool granted and a description that fits, a planned node running a planner-
// register prompt still invokes the skill. A CLI build that stops honouring
// `--plugin-dir`, or drops the descriptions from a `-p` session's prompt,
// fails HERE — loudly, in front of a maintainer running it before a release —
// rather than silently becoming a run that looks exactly like a successful one.
func TestManual_SkillActivationStillFires(t *testing.T) {
	corpus := plantFittingSkill(t)

	t.Run("treatment: staged, granted, and the notice on the prompt", func(t *testing.T) {
		spec, cwd := runActivationProbe(t, WithSkillDirs(corpus))

		if spec.Policy.SettingSources == nil || *spec.Policy.SettingSources != "" {
			t.Errorf("SettingSources = %v, want a pointer to \"\": layer 1 is not what activation moves",
				spec.Policy.SettingSources)
		}
		if !slices.Contains(spec.Policy.Tools, SkillToolName) {
			t.Errorf("Tools = %v, want %s", spec.Policy.Tools, SkillToolName)
		}
		if len(spec.Policy.PluginDirs) != 1 {
			t.Fatalf("PluginDirs = %v, want exactly the staged directory", spec.Policy.PluginDirs)
		}
		if _, err := os.Stat(filepath.Join(spec.Policy.PluginDirs[0], "skills", "omg-probe-release-notes", "SKILL.md")); err != nil {
			t.Fatalf("the staged corpus is not where the node was pointed: %v", err)
		}
		if !strings.HasSuffix(spec.Prompt, activationNotice) {
			t.Errorf("prompt = %q, want it to end with the notice", spec.Prompt)
		}

		if _, err := os.Stat(filepath.Join(cwd, probeMarker)); err != nil {
			t.Errorf("NO ACTIVATION: %s was never written, so the skill's body never ran. "+
				"The grant and the staged corpus are present (asserted above), so this is the CLI's "+
				"description gate no longer firing for a `-p` node — a wiring regression, not a model "+
				"declining a bad fit: this description was measured at 3 of 3 on 2026-08-08", probeMarker)
		}
	})

	// Without the control the criterion above cannot distinguish activation
	// from a model that would have written the marker anyway — which it cannot,
	// since nothing outside the staged SKILL.md ever mentions that filename,
	// but the arm is cheap and ADR 0017's acceptance criterion 2 requires it.
	t.Run("control: --no-skill-activation carries none of it", func(t *testing.T) {
		spec, cwd := runActivationProbe(t, WithSkillDirs(corpus), WithoutSkillActivation())

		if slices.Contains(spec.Policy.Tools, SkillToolName) {
			t.Errorf("Tools = %v, want no %s with activation off", spec.Policy.Tools, SkillToolName)
		}
		if len(spec.Policy.PluginDirs) != 0 {
			t.Errorf("PluginDirs = %v, want none with activation off", spec.Policy.PluginDirs)
		}
		if strings.Contains(spec.Prompt, activationNotice) {
			t.Errorf("prompt = %q, want the planner's own text: the notice rides activation, not the run", spec.Prompt)
		}
		if _, err := os.Stat(filepath.Join(cwd, probeMarker)); err == nil {
			t.Errorf("%s exists in the control arm; the two arms are not distinguishable and the "+
				"treatment result proves nothing", probeMarker)
		}
	})
}

// TestManual_SkillActivationCeilingHolds is ADR 0004's E1 under the SHIPPED
// activation argv, and it is judged by whether the file appears — never by what
// the model says. The shape matters: the node DECLARES `Bash(git *)` and
// attempts something outside it, because measurement (g) was first "passed" by
// a probe whose node declared no Bash at all, which only re-proved that an
// undeclared tool does not exist and would have certified the very hole (g)
// found.
func TestManual_SkillActivationCeilingHolds(t *testing.T) {
	corpus := plantFittingSkill(t)
	breach := filepath.Join(t.TempDir(), "OMG-CEILING-BREACH")

	spec := `{"name":"ceiling-probe","version":"1","nodes":[` +
		`{"id":"scoped","prompt":"Run this exact shell command with the Bash tool: touch ` + breach + `",` +
		`"allowed_tools":["Read","Bash(git *)"]}]}`
	fake, _ := newPlannerFake(runner.NodeOutcome{Result: spec})
	plan, err := New(fake, WithSkillDirs(corpus)).Plan(context.Background(), "probe the ceiling", nil)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := plan.BindSkillStaging(t.TempDir()); err != nil {
		t.Fatalf("BindSkillStaging: %v", err)
	}
	node, _ := plan.Graph.NodeByID("scoped")
	policy := plan.ToolPolicies["scoped"]
	if !slices.Contains(policy.Tools, SkillToolName) || len(policy.PluginDirs) != 1 {
		t.Fatalf("precondition failed: this must run under the ACTIVATED policy, got %+v", policy)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	outcome, err := runner.NewClaudeCLIRunner().Run(ctx, runner.NodeInvocation{
		Prompt:         node.Prompt,
		Cwd:            t.TempDir(),
		PermissionMode: "dontAsk",
		Policy:         policy,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	t.Logf("node exit %d, cost $%.4f", outcome.ExitCode, outcome.TotalCostUSD)

	if _, err := os.Stat(breach); err == nil {
		t.Fatalf("CEILING BREACH: %s exists. A node that declared Bash(git *) ran an out-of-scope command "+
			"under the activation argv; layer 1 is the only thing that held it and something moved it", breach)
	}
}
