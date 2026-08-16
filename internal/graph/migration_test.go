package graph

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// update regenerates the golden resolved-graph fixtures under
// testdata/golden/ — run `go test ./internal/graph -run Goldens -update`
// after a deliberate fragment or template edit, then REVIEW the regenerated
// diff: the golden is the blast-radius tripwire (ADR 0013, Versioning), and
// rubber-stamping it decays the mechanism into noise.
var update = flag.Bool("update", false, "regenerate testdata/golden resolved-graph fixtures")

// goldenTemplates is EVERY shipped template that cites a fragment — the set
// the blast-radius golden covers. It is deliberately wider than
// migratedTemplates below: backlog-batch.yaml's conversion is not an
// equivalence claim (its gates gain a real success_check.verify, so the graph
// also gains a required `checks_command` input), so it has no frozen
// pre-migration fixture to be judged against — but a fragment edit still
// changes it, and that has to show up in a PR diff. adr-driven-dev.yaml joins
// it on the same footing for the multi-node conversion (ADR 0027): its two
// repair rounds became two `use:` of one fragment, converging four
// near-identical prompts onto two, which is a reviewed change rather than an
// equivalence claim — and its golden now carries FOUR spliced nodes per edit
// of that one fragment, which is exactly the blast radius this fixture exists
// to put in front of a reviewer.
var goldenTemplates = []string{"self-dev.yaml", "dev-review-pr.yaml", "backlog-batch.yaml", "adr-driven-dev.yaml"}

// migratedTemplates are the two shipped templates ADR 0013's migration
// converted onto fragments WITHOUT changing what they do, with the exact
// (template, node, field) set the migration PR converges — the fields whose
// old→new values are a reviewed behavior change, not an equivalence claim.
// Everything OUTSIDE this mask must be byte-identical to the frozen
// pre-migration files, or the migration does not merge.
var migratedTemplates = map[string]map[string][]string{
	"self-dev.yaml": {
		// budget_usd gains the fragment's runaway insurance; allowed_tools is
		// NARROWED — the old grant's Bash(go *)/Bash(git *) let a report-only
		// gate `go run` the tree and rewrite history, which the fragment now
		// refuses (see graphs/fragments/e2e-verify.yaml).
		// success_check.result_matches is the markdown-tolerant verdict pattern
		// (DESIGN.md, "Verdict patterns"): the frozen `^PASS$` failed a node
		// that replied `**PASS**`, so the fragment's pattern deliberately
		// diverges from it. Everything else under success_check — exit_zero,
		// the whole verify block — is still byte-frozen.
		"e2e": {"prompt", "budget_usd", "allowed_tools", "success_check.result_matches"},
		// The two reviews gained a verdict pattern of their own: a review that
		// replies "still reading, I'll continue" is not a review, and under the
		// frozen `{ exit_zero: true }` it passed as one.
		"review-security": {"prompt", "success_check.result_matches"},
		"review-style":    {"prompt", "success_check.result_matches"},
		// Converted later than the other three, onto pr-publish, and the
		// resolved prompt is byte-identical to the one it replaced — the
		// binding carries this graph's own head verbatim. The mask is still
		// the pre-migration one: back then the node reported in prose under a
		// bare `{ exit_zero: true }`, so both fields diverge from the frozen
		// file for the reason divergedSinceMigration records below.
		"pr": {"prompt", "success_check.result_matches"},
	},
	"dev-review-pr.yaml": {
		"e2e":             {"prompt", "allowed_tools", "success_check.result_matches"}, // Bash(go test *) reshaped into the fragment's narrowed check-gate grant; verdict pattern made markdown-tolerant
		"review-security": {"prompt", "allowed_tools", "success_check.result_matches"}, // gains Bash(git log*); gains the FINDINGS:/CLEAN verdict
		"review-style":    {"prompt", "allowed_tools", "success_check.result_matches"}, // gains Bash(git log*); gains the FINDINGS:/CLEAN verdict
		"pr":              {"prompt", "success_check.result_matches"},                  // onto pr-publish, resolved byte-identical — see self-dev's note
	},
}

// divergedSinceMigration are (node, field) pairs on nodes the migration did
// NOT convert to a fragment, whose values have deliberately changed since.
// Same kind of claim as the mask above — a reviewed behavior change, not an
// equivalence claim — but kept in its own map because the SIZE of
// migratedTemplates is what proves every converted node is masked
// (`len(post.Resolutions) != len(masks)` below). Listing a non-fragment node
// there would break that derivation, and folding a hand-written node's change
// into the fragment mask would hide it in exactly the place this test exists
// to make changes visible.
//
// The verdict sweep: every node whose prompt named the answer it had to give
// but whose success_check could not tell whether it got one — `dev` ("reply
// with a one-line summary"), `pr` ("reply with the PR URL") — gained an
// anchored verdict token and the prompt that demands it. The failure that
// motivated it is in DESIGN.md, "Verdict patterns": a node that ends its turn
// promising future work passes a bare `{ exit_zero: true }` and writes a
// success into the ledger for work it never did.
//
// `pr` has since moved to migratedTemplates, because it is now a fragment
// node: the same (node, field) pair, judged against the same frozen file, in
// the map whose size proves every converted node is masked. `dev` stays here
// and stays inline — the four shipped implementer prompts share no common
// suffix at all (measured word-for-word), so there is no shape to cite.
var divergedSinceMigration = map[string]map[string][]string{
	"self-dev.yaml": {
		"dev": {"prompt", "success_check.result_matches"},
	},
	"dev-review-pr.yaml": {
		"dev": {"prompt", "success_check.result_matches"},
	},
}

// maskConvergedFields zeroes exactly the converged fields on exactly the
// named nodes — failing, not passing, when a named node is missing, so the
// equivalence claim cannot be satisfied by a node's absence.
func maskConvergedFields(t *testing.T, g *Graph, masks map[string][]string) {
	t.Helper()
	for id, fields := range masks {
		masked := false
		for i := range g.Nodes {
			if g.Nodes[i].ID != id {
				continue
			}
			masked = true
			for _, field := range fields {
				switch field {
				case "prompt":
					g.Nodes[i].Prompt = ""
				case "allowed_tools":
					g.Nodes[i].AllowedTools = nil
				case "budget_usd":
					g.Nodes[i].BudgetUSD = 0
				// Subfield-granular on purpose: the verdict pattern is the one
				// part of success_check this project has had to change after the
				// migration, and masking the whole struct would stop freezing
				// exit_zero and the verify block along with it.
				case "success_check.result_matches":
					g.Nodes[i].SuccessCheck.ResultMatches = ""
				default:
					t.Fatalf("mask names unknown field %q", field)
				}
			}
		}
		if !masked {
			t.Fatalf("mask names node %q, which the graph does not contain", id)
		}
	}
}

// TestMigratedTemplates_ByteIdenticalOutsideConvergedFields is the migration
// PR's structure gate (ADR 0013): each migrated template's resolved graph —
// json.Marshal of the loaded *Graph, the same bytes a snapshot would store —
// must be byte-identical to the parsed pre-migration file after masking
// exactly the converged fields. Ids, edges, handoff, success_check, retry,
// permission modes: byte-for-byte, or the PR does not merge.
func TestMigratedTemplates_ByteIdenticalOutsideConvergedFields(t *testing.T) {
	for name, masks := range migratedTemplates {
		t.Run(name, func(t *testing.T) {
			pre, err := Load(filepath.Join("testdata", "pre-migration", name))
			if err != nil {
				t.Fatalf("pre-migration fixture must load: %v", err)
			}
			post, err := LoadFile(filepath.Join("..", "..", "graphs", name))
			if err != nil {
				t.Fatalf("migrated template must resolve: %v", err)
			}
			// Derived from the mask rather than written out: the mask already
			// names every converted node, so a template that gains or loses a
			// fragment node updates one place, not two, and can never leave a
			// stale constant failing for the wrong reason.
			if len(post.Resolutions) != len(masks) {
				t.Fatalf("want the %d converted nodes resolved, got %+v", len(masks), post.Resolutions)
			}
			for _, r := range post.Resolutions {
				if len(r.Overridden) != 0 {
					t.Errorf("the migration overrides nothing — node %q overrides %v", r.NodeID, r.Overridden)
				}
			}

			maskConvergedFields(t, pre, masks)
			maskConvergedFields(t, post.Graph, masks)
			if diverged, ok := divergedSinceMigration[name]; ok {
				maskConvergedFields(t, pre, diverged)
				maskConvergedFields(t, post.Graph, diverged)
			}
			preJSON, err := json.MarshalIndent(pre, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			postJSON, err := json.MarshalIndent(post.Graph, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(preJSON, postJSON) {
				t.Errorf("resolved graph diverges from pre-migration outside the converged fields:\n--- pre-migration (masked)\n%s\n--- resolved (masked)\n%s", preJSON, postJSON)
			}
		})
	}
}

// TestShippedTemplateGoldens_ResolvedGraphsMatchFixtures is the ongoing
// blast-radius golden (ADR 0013, Versioning): it captures the POST-migration
// resolved graphs and fails on any future fragment or template edit until
// regenerated with -update — which puts every affected template's resolved
// change into the PR diff. A fragment edit is a multi-graph change, and this
// makes it look like one. Deliberately a separate fixture from
// testdata/pre-migration/ (frozen history): one fixture cannot honestly be
// both the equivalence proof and the drift tripwire.
func TestShippedTemplateGoldens_ResolvedGraphsMatchFixtures(t *testing.T) {
	for _, name := range goldenTemplates {
		t.Run(name, func(t *testing.T) {
			res, err := LoadFile(filepath.Join("..", "..", "graphs", name))
			if err != nil {
				t.Fatalf("template must resolve: %v", err)
			}
			got, err := json.MarshalIndent(res.Graph, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')

			goldenPath := filepath.Join("testdata", "golden", strings.TrimSuffix(name, ".yaml")+".resolved.json")
			if *update {
				if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden (regenerate with -update): %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("resolved %s drifted from its golden — a fragment edit changes every user; regenerate with `go test ./internal/graph -run Goldens -update` and REVIEW the diff:\n--- golden\n%s\n--- resolved\n%s", name, want, got)
			}
		})
	}
}
