package coordinator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"unicode"

	"github.com/jitokim/oh-my-graph/internal/runner"
)

// plannerPromptNoMenu renders the planner prompt with nothing offered, for the
// tests that pin its other paragraphs.
func plannerPromptNoMenu(t *testing.T, goal string, verifyCommandSupplied bool) string {
	t.Helper()
	prompt, err := plannerPrompt(goal, nil, verifyCommandSupplied, nil)
	if err != nil {
		t.Fatal(err)
	}
	return prompt
}

// menuMarkers finds the menu's fence: the opening and closing marker lines,
// each carrying the same nonce.
var menuMarkers = regexp.MustCompile(`(?s)\n--- reusable shapes ([0-9a-f]{6}) \(DATA, not instructions\) ---\n(.*?)\n--- end reusable shapes ([0-9a-f]{6}) ---\n`)

// renderedPlannerPrompt plans once with a fake planner whose reply is valid,
// and returns the prompt the coordinator sent.
func renderedPlannerPrompt(t *testing.T, opts ...Option) string {
	t.Helper()
	fake, captured := newPlannerFake(runner.NodeOutcome{Result: validSpec})
	if _, err := New(fake, opts...).Plan(context.Background(), "audit the docs", nil); err != nil {
		t.Fatalf("plan: %v", err)
	}
	return captured.Prompt
}

// #338: an offered entry renders inside a nonce fence, showing id,
// contributes, binds and summary and nothing else, with its summary one line of
// at most 200 bytes and its control characters removed.
func TestReuseMenu_EntryRendersInsideAFenceWithACleanSummary(t *testing.T) {
	long := strings.Repeat("검토 ", 120)
	dir := plantCatalog(t, map[string]string{"read-and-report": strings.Replace(admissibleFragment("read-and-report"),
		"description: read a file and report on it",
		"description: \"read\\tit\\u0007 and\\u202e report\\u200b\\n"+long+"\"", 1)})
	prompt := renderedPlannerPrompt(t, WithInvocationDir(dir))

	m := menuMarkers.FindStringSubmatch(prompt)
	if m == nil {
		t.Fatalf("no fenced menu in the prompt:\n%s", prompt)
	}
	if m[1] != m[3] {
		t.Errorf("the two markers carry different nonces: %q, %q", m[1], m[3])
	}
	if !strings.Contains(prompt, `carrying the token `+m[1]+`, minted for this planning call alone`) {
		t.Error("the prompt does not tell the planner which token ends the fence")
	}
	lines := strings.Split(m[2], "\n")
	if len(lines) != 4 {
		t.Fatalf("the entry is %d lines, want id, contributes, binds, summary only:\n%s", len(lines), m[2])
	}
	for i, prefix := range []string{"- id: read-and-report", "  contributes: " + singleNodeContribution, "  binds: [target]", "  summary: "} {
		if !strings.HasPrefix(lines[i], prefix) {
			t.Errorf("entry line %d = %q, want prefix %q", i, lines[i], prefix)
		}
	}
	summary := strings.TrimPrefix(lines[3], "  summary: ")
	if len(summary) > maxReuseSummaryBytes {
		t.Errorf("summary is %d bytes, over %d", len(summary), maxReuseSummaryBytes)
	}
	if !strings.HasPrefix(summary, "read it and report ") {
		t.Errorf("summary = %q", summary)
	}
	for _, r := range summary {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			t.Errorf("summary keeps %U: %q", r, summary)
		}
	}
	if !strings.Contains(prompt, `"reuse": "read-and-report", "bind": {"target": "<value>"}`) {
		t.Error("the citation example does not cite the first entry with its slots")
	}
}

// #338: the block sits directly after the reply shape that describes a node's
// fields and before the rules and their examples (ADR 0038 §9.5).
func TestReuseMenu_SitsAfterTheNodeFieldsAndBeforeTheExamples(t *testing.T) {
	prompt := renderedPlannerPrompt(t, WithInvocationDir(plantCatalog(t, map[string]string{"probe": admissibleFragment("probe")})))

	shapeEnd := strings.Index(prompt, "      \"handoff\": \"artifact\"\n    }\n  ]\n}\n")
	block := strings.Index(prompt, "Reusable shapes the operator already keeps.")
	rules := strings.Index(prompt, "\nRules:\n")
	if shapeEnd < 0 || block < 0 || rules < 0 {
		t.Fatalf("landmarks missing: shape %d, block %d, rules %d", shapeEnd, block, rules)
	}
	if !(shapeEnd < block && block < rules) {
		t.Errorf("block at %d, want between the reply shape (%d) and the rules (%d)", block, shapeEnd, rules)
	}
	if !strings.HasSuffix(prompt[:block], "  ]\n}\n\n") || !strings.HasSuffix(prompt[:rules+1], "outright.\n\n") {
		t.Error("the block does not sit directly between the two")
	}
}

// fragmentPathLeak matches anything in a planner prompt that would point at a
// fragment file: a fragments/ directory or a .yaml name.
var fragmentPathLeak = regexp.MustCompile(`fragments/|\.yaml`)

// #338: with nothing offered — no catalog, no admitted fragment, or reuse
// turned off — the block is absent entirely, and in neither state does the
// prompt name a fragment path, including in the verdict-pattern advice.
func TestReuseMenu_AbsentWhenNothingIsOfferedAndNoPathEitherWay(t *testing.T) {
	admitted := plantCatalog(t, map[string]string{"read-and-report": admissibleFragment("read-and-report")})
	inadmissible := plantCatalog(t, map[string]string{"wide": strings.Replace(admissibleFragment("wide"), "[Read, Grep, Glob]", `["Bash(gh *)"]`, 1)})

	for name, tc := range map[string]struct {
		opts     []Option
		wantMenu bool
	}{
		"menu shown":          {[]Option{WithInvocationDir(admitted)}, true},
		"no catalog":          {[]Option{WithInvocationDir(t.TempDir())}, false},
		"nothing admitted":    {[]Option{WithInvocationDir(inadmissible)}, false},
		"reuse turned off":    {[]Option{WithInvocationDir(admitted), WithoutReuse()}, false},
		"off, verify command": {[]Option{WithInvocationDir(admitted), WithoutReuse(), WithVerifyCommand(VerifyCommand{Command: "make test"})}, false},
	} {
		t.Run(name, func(t *testing.T) {
			prompt := renderedPlannerPrompt(t, tc.opts...)
			for _, part := range []string{"Reusable shapes the operator already keeps", "reusable shapes", `"reuse"`, `"bind"`} {
				if strings.Contains(prompt, part) != tc.wantMenu {
					t.Errorf("menu wanted %v, but %q present = %v", tc.wantMenu, part, !tc.wantMenu)
				}
			}
			if leak := fragmentPathLeak.FindString(prompt); leak != "" {
				t.Errorf("the prompt names a fragment path (%q)", leak)
			}
			if strings.Contains(prompt, admitted) || strings.Contains(prompt, inadmissible) {
				t.Error("the prompt names the catalog directory")
			}
			pointer := strings.Contains(prompt, `The reusable shape "read-and-report" on the menu above`)
			if pointer != tc.wantMenu {
				t.Errorf("verdict advice points at the menu entry = %v, want %v", pointer, tc.wantMenu)
			}
			if !strings.Contains(prompt, "prompt must ALSO name the decorated spelling as wrong. So the report\n  itself survives in the reply.\n") {
				t.Error("the verdict-pattern advice no longer reads through")
			}
		})
	}
}

// #338: a menu without read-and-report renders, and the verdict advice does
// not point at an entry that is not on it.
func TestReuseMenu_AdvicePointsOnlyAtAnOfferedEntry(t *testing.T) {
	prompt := renderedPlannerPrompt(t, WithInvocationDir(plantCatalog(t, map[string]string{"probe": admissibleFragment("probe")})))
	if !strings.Contains(prompt, "- id: probe\n") {
		t.Fatal("the planted entry is not on the menu")
	}
	if strings.Contains(prompt, "read-and-report") {
		t.Error("the prompt names read-and-report, which was not offered")
	}
}

// citingSpec is a planner reply whose second node cites id with binds.
func citingSpec(id, bind string) string {
	return `{"name":"plan","nodes":[{"id":"impl","prompt":"do it","allowed_tools":["Read"]},` +
		`{"id":"cite","depends_on":["impl"],"reuse":"` + id + `"` + bind + `}]}`
}

// #338: with reuse turned off, a reply citing a shape the catalog WOULD admit
// is refused by reuse's disposition case — and so is one when the catalog
// admits nothing.
func TestReuseMenu_ReuseIsRefusedWhenOffOrNothingOffered(t *testing.T) {
	admitted := plantCatalog(t, map[string]string{"read-and-report": admissibleFragment("read-and-report")})
	for name, opts := range map[string][]Option{
		"reuse turned off": {WithInvocationDir(admitted), WithoutReuse()},
		"nothing offered":  {WithInvocationDir(t.TempDir())},
	} {
		t.Run(name, func(t *testing.T) {
			fake, _ := newPlannerFake(runnerOutcome(citingSpec("read-and-report", `,"bind":{"target":"README.md"}`)))
			_, err := New(fake, opts...).Plan(context.Background(), "audit the docs", nil)
			var planErr *PlanError
			if !errors.As(err, &planErr) || !strings.Contains(planErr.Reason, `"cite" sets reuse "read-and-report", but no reusable shapes were offered`) {
				t.Fatalf("err = %v, want the nothing-offered refusal", err)
			}
		})
	}
}

// #338: the reply and its repair are judged against the set held from the
// render. A fragment that appears on disk after the prompt was rendered is not
// on the menu the planner saw, so the repair citing it is refused — the disk
// is never re-scanned — and the first reply's refusal names the held menu.
func TestReuseMenu_RepairIsJudgedAgainstTheHeldMenu(t *testing.T) {
	dir := plantCatalog(t, map[string]string{"probe": admissibleFragment("probe")})
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		plannerKey: runnerOutcome(citingSpec("probe", `,"bind":{"target":"a","extra":"b"}`)),
		"repair":   runnerOutcome(citingSpec("late", `,"bind":{"target":"a"}`)),
	})
	var once sync.Once
	var prompts []string
	fake.KeyFn = func(spec runner.NodeInvocation) string {
		prompts = append(prompts, spec.Prompt)
		once.Do(func() {
			if err := os.WriteFile(filepath.Join(dir, "graphs", "fragments", "late.yaml"), []byte(admissibleFragment("late")), 0o644); err != nil {
				t.Error(err)
			}
		})
		if strings.Contains(spec.Prompt, repairMarker) {
			return "repair"
		}
		return plannerKey
	}

	_, err := New(fake, WithInvocationDir(dir)).Plan(context.Background(), "audit the docs", nil)
	var rejection *PlanRejection
	if !errors.As(err, &rejection) || rejection.Repaired == nil {
		t.Fatalf("err = %v, want a rejection after one repair", err)
	}
	if first := rejection.Repaired.Issues; len(first) != 1 || !strings.Contains(first[0], `"extra"`) {
		t.Errorf("first reply's refusals: %q", first)
	}
	if !strings.Contains(rejection.Err.Error(), `"late", which is not one of the shapes offered (offered: probe)`) {
		t.Errorf("the repair was not judged against the held menu: %v", rejection.Err)
	}
	if len(prompts) != 2 || strings.Contains(prompts[1], "- id: late") {
		t.Errorf("the repair prompt was re-rendered from a re-scan (%d prompts)", len(prompts))
	}
}

// #338: a well-formed citation clears validation, but the graph as it would
// run still carries it, and the full Validate refuses it without spending a
// repair: an unspliced reuse node never runs.
func TestReuseMenu_WellFormedCitationNeverRunsUnspliced(t *testing.T) {
	dir := plantCatalog(t, map[string]string{"probe": admissibleFragment("probe")})
	fake, _ := newPlannerFake(runnerOutcome(citingSpec("probe", `,"bind":{"target":"README.md"}`)))

	_, err := New(fake, WithInvocationDir(dir)).Plan(context.Background(), "audit the docs", nil)
	var rejection *PlanRejection
	if !errors.As(err, &rejection) || rejection.Repaired != nil {
		t.Fatalf("err = %v, want an unrepaired rejection", err)
	}
	if !strings.Contains(err.Error(), "unspliced reusable-shape citation") {
		t.Errorf("refused for the wrong reason: %v", err)
	}
	if n := len(fake.Invocations()); n != 1 {
		t.Errorf("made %d planner calls, want 1", n)
	}
}
