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

func TestExtractJSON(t *testing.T) {
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
			reply: validSpec + "\nNote: review reads {{ artifacts.scan }} {ok}.",
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
			if got := extractJSON(tc.reply); got != tc.want {
				t.Errorf("extractJSON(%q)\n got: %q\nwant: %q", tc.reply, got, tc.want)
			}
		})
	}
}

func TestExtractJSON_TemplateQuoteReplyParses(t *testing.T) {
	reply := templateQuote + "\n" + validSpec
	if _, err := graph.ParsePlannerReply([]byte(extractJSON(reply))); err != nil {
		t.Fatalf("ParsePlannerReply on extracted reply: %v", err)
	}
}

// TestExtractJSON_PlannerReply387Fixture replays the real rejected planner
// reply from #386 behind a prose line quoting a template placeholder: the
// plan must come out parseable.
func TestExtractJSON_PlannerReply387Fixture(t *testing.T) {
	reply, err := os.ReadFile("testdata/planner-reply-387.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := graph.ParsePlannerReply([]byte(extractJSON(string(reply)))); err != nil {
		t.Fatalf("ParsePlannerReply on extracted fixture reply: %v", err)
	}
}
