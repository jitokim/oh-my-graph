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

func TestExtractPlanJSON(t *testing.T) {
	cases := []struct {
		name  string
		reply string
		want  string
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := extractPlanJSON(tc.reply)
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
