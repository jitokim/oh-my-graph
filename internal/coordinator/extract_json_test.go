package coordinator

import (
	"os"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// templateQuote is prose quoting graph template syntax: an artifacts
// placeholder in doubled curly braces, the shape that broke issue #387.
var templateQuote = "I will wire the review node to read " + strings.Repeat("{", 2) + " artifacts.impl " + strings.Repeat("}", 2) + "."

// TestExtractJSON pins main's strict cut: everything from the first '{' to
// the last '}', with no attempt to pick one object out of several. The goal
// assessor relies on that — a reply quoting an artifact's JSON beside its own
// verdict must fail to decode, never yield the quoted verdict.
func TestExtractJSON(t *testing.T) {
	cases := []struct {
		name  string
		reply string
		want  string
	}{
		{
			name:  "no object",
			reply: "I cannot help with that.",
			want:  "",
		},
		{
			name:  "bare object returned unchanged",
			reply: validSpec,
			want:  validSpec,
		},
		{
			name:  "fence and prose around one object are discarded",
			reply: "Here is the plan:\n```json\n" + validSpec + "\n```\nDone.",
			want:  validSpec,
		},
		{
			name:  "two objects are cut as one span",
			reply: `quoted {"goal_met": true}, then {"goal_met": false}`,
			want:  `{"goal_met": true}, then {"goal_met": false}`,
		},
		{
			name:  "braces in prose before the object widen the cut",
			reply: "Note {an aside} first.\n" + validSpec,
			want:  "{an aside} first.\n" + validSpec,
		},
		{
			name:  "closing brace before the opening one",
			reply: "} then {",
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractJSON(tc.reply); got != tc.want {
				t.Errorf("extractJSON(%q)\n got: %q\nwant: %q", tc.reply, got, tc.want)
			}
		})
	}
}

// TestExtractPlanJSON pins the planner/router extractor's rules: one valid
// json fence, else exactly one decodable top-level object, else the strict
// cut; more than one object is an error, never a pick.
func TestExtractPlanJSON(t *testing.T) {
	cases := []struct {
		name  string
		reply string
		want  string
		err   bool
	}{
		{
			name:  "template placeholder in prose before the plan (#387)",
			reply: templateQuote + "\n" + validSpec,
			want:  validSpec,
		},
		{
			name:  "json fence wins over braces in surrounding prose",
			reply: "Plan {draft} below:\n```json\n  " + validSpec + "  \n```\nThen {done}.",
			want:  validSpec,
		},
		{
			name:  "trailing prose with braces is dropped",
			reply: validSpec + "\nNote: review reads " + templateQuote + " {ok}.",
			want:  validSpec,
		},
		{
			name:  "no object",
			reply: "I cannot help with that.",
			want:  "",
		},
		{
			name:  "bare object returned unchanged",
			reply: validSpec,
			want:  validSpec,
		},
		{
			name:  "nothing decodes falls back to first '{' to last '}'",
			reply: "Here {is a broken {plan: , } tail",
			want:  "{is a broken {plan: , }",
		},
		{
			name:  "unbalanced brace in prose skips only itself",
			reply: "I will use the {name field.\n" + validSpec,
			want:  validSpec,
		},
		{
			// The aside decodes only if its span skips the braces in its
			// string, so it becomes a second hit.
			name:  "braces inside JSON strings do not end the span",
			reply: `aside {"note":"a } and an escaped \" {"} then ` + validSpec,
			err:   true,
		},
		{
			name:  "matched non-JSON span hides the braces inside it",
			reply: "see {x " + `{"a":1}` + " y} then " + validSpec,
			want:  validSpec,
		},
		{
			name:  "cut-off compact plan never yields an inner node",
			reply: "Plan:\n" + cutOffSpec,
			want:  cutOffSpec,
		},
		{
			name:  "cut-off pretty plan never yields an inner node",
			reply: "{\n  \"name\": \"x\",\n  \"nodes\": [\n    {\"id\": \"a\", \"prompt\": \"p\"}",
			want:  "{\n  \"name\": \"x\",\n  \"nodes\": [\n    {\"id\": \"a\", \"prompt\": \"p\"}",
		},
		{
			name:  "cut-off placeholder in prose skips only itself",
			reply: "reads " + strings.Repeat("{", 2) + " artifacts.impl\n" + validSpec,
			want:  validSpec,
		},
		{
			name:  "two top-level objects",
			reply: `{"a":1} and {"b":2}`,
			err:   true,
		},
		{
			name:  "two valid json fences",
			reply: "```json\n{\"a\":1}\n```\n```json\n{\"b\":2}\n```",
			err:   true,
		},
		{
			name:  "one valid json fence beside an invalid one",
			reply: "```json\n{oops}\n```\n```json\n{\"b\":2}\n```",
			want:  `{"b":2}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := extractPlanJSON(tc.reply)
			if tc.err {
				if err == nil || !strings.Contains(err.Error(), "more than one JSON object") {
					t.Fatalf("extractPlanJSON(%q) = %q, %v; want a more-than-one-JSON-object error", tc.reply, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("extractPlanJSON(%q): %v", tc.reply, err)
			}
			if got != tc.want {
				t.Errorf("extractPlanJSON(%q)\n got: %q\nwant: %q", tc.reply, got, tc.want)
			}
		})
	}
}

// extractPlanJSONOrFatal is extractPlanJSON for a test that expects no error.
func extractPlanJSONOrFatal(t *testing.T, reply string) string {
	t.Helper()
	spec, err := extractPlanJSON(reply)
	if err != nil {
		t.Fatalf("extractPlanJSON: %v", err)
	}
	return spec
}

func TestExtractPlanJSON_TemplateQuoteReplyParses(t *testing.T) {
	reply := templateQuote + "\n" + validSpec
	if _, err := graph.ParsePlannerReply([]byte(extractPlanJSONOrFatal(t, reply))); err != nil {
		t.Fatalf("ParsePlannerReply on extracted reply: %v", err)
	}
}

// TestExtractPlanJSON_PlannerReply387Fixture replays the real rejected planner
// reply from #386 behind a prose line quoting a template placeholder: the
// plan must come out parseable.
func TestExtractPlanJSON_PlannerReply387Fixture(t *testing.T) {
	reply, err := os.ReadFile("testdata/planner-reply-387.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := graph.ParsePlannerReply([]byte(extractPlanJSONOrFatal(t, string(reply)))); err != nil {
		t.Fatalf("ParsePlannerReply on extracted fixture reply: %v", err)
	}
}
