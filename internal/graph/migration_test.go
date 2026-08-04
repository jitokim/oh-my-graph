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
// changes it, and that has to show up in a PR diff.
var goldenTemplates = []string{"self-dev.yaml", "dev-review-pr.yaml", "backlog-batch.yaml"}

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
		"e2e":             {"prompt", "budget_usd", "allowed_tools", "success_check.result_matches"},
		"review-security": {"prompt"},
		"review-style":    {"prompt"},
	},
	"dev-review-pr.yaml": {
		"e2e":             {"prompt", "allowed_tools", "success_check.result_matches"}, // Bash(go test *) reshaped into the fragment's narrowed check-gate grant; verdict pattern made markdown-tolerant
		"review-security": {"prompt", "allowed_tools"},                                 // gains Bash(git log*)
		"review-style":    {"prompt", "allowed_tools"},                                 // gains Bash(git log*)
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
