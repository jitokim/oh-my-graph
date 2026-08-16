package graph

import (
	"reflect"
	"strings"
	"testing"
)

// validLoopYAML is the minimal correct feedback loop the failure cases below
// mutate away from: impl → localrun → review, with review closing the arc
// back to impl and impl reading the review's payload.
const validLoopYAML = `
name: loop
nodes:
  - id: impl
    prompt: "implement; feedback follows (empty on the first pass): {{ feedback.review }}"
  - id: localrun
    depends_on: [impl]
    prompt: run checks
  - id: review
    depends_on: [localrun]
    prompt: judge the work
    success_check: { result_matches: "ready" }
    feedback: { rerun: impl, max: 2 }
`

func TestValidate_Feedback_ValidLoopIsAccepted(t *testing.T) {
	g := parseGraph(t, validLoopYAML)
	if body := g.FeedbackBody("review"); !reflect.DeepEqual(body, []string{"impl", "localrun", "review"}) {
		t.Fatalf("FeedbackBody(review) = %v, want the full impl→localrun→review path", body)
	}
}

// TestValidate_FeedbackPlaceholder_SeesNamespacedTokens is the load-time half
// of ADR 0027's charset move, and the one that fails SILENTLY if forgotten.
// handoff.placeholderPattern gained '/' so a spliced {{ feedback.qa-a/review }}
// interpolates; feedbackTokenPattern must gain it in the same change, or this
// validator — the check that confines a feedback token to its arc's body —
// would stop seeing the token altogether. Not seeing it is not a loud failure:
// the feedback namespace resolves to "" whenever no round has fired, so an
// unconfined token would just be empty forever, in a paid prompt, for the life
// of the graph.
//
// Both halves are asserted: an out-of-body namespaced token is refused, and an
// in-body one is accepted (a check that refused both would pass this test while
// making every loop fragment unloadable).
func TestValidate_FeedbackPlaceholder_SeesNamespacedTokens(t *testing.T) {
	const spliced = `
name: spliced-loop
nodes:
  - id: qa-a/impl
    prompt: "implement; findings follow: {{ feedback.qa-a/review }}"
  - id: qa-a/review
    depends_on: [qa-a/impl]
    prompt: judge the work
    feedback: { rerun: qa-a/impl, max: 1 }
  - id: pr
    depends_on: [qa-a/review]
    prompt: "open a PR"
`
	if _, err := Parse([]byte(spliced)); err != nil {
		t.Fatalf("an in-body namespaced feedback token must be accepted: %v", err)
	}

	outOfBody := strings.Replace(spliced,
		`    prompt: "open a PR"`,
		`    prompt: "open a PR quoting {{ feedback.qa-a/review }}"`, 1)
	_, err := Parse([]byte(outOfBody))
	vErr := asValidationError(t, err)
	if vErr.NodeID != "pr" || !strings.Contains(vErr.Reason, "only legal inside the body") {
		t.Fatalf("an out-of-body namespaced feedback token must be refused at load: %+v", vErr)
	}
}

// TestValidate_Feedback_Failures is the failure-first table: every shape
// ADR 0010's load validation must refuse, each named by the substring its
// error must carry so the test proves the RIGHT rule fired.
func TestValidate_Feedback_Failures(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string // substring of the validation error
	}{
		{
			name: "max missing",
			yaml: `
name: g
nodes:
  - id: impl
    prompt: p
  - id: review
    depends_on: [impl]
    prompt: p
    feedback: { rerun: impl }
`,
			want: "feedback.max must be declared and >= 1",
		},
		{
			name: "max zero",
			yaml: `
name: g
nodes:
  - id: impl
    prompt: p
  - id: review
    depends_on: [impl]
    prompt: p
    feedback: { rerun: impl, max: 0 }
`,
			want: "feedback.max must be declared and >= 1",
		},
		{
			name: "max negative",
			yaml: `
name: g
nodes:
  - id: impl
    prompt: p
  - id: review
    depends_on: [impl]
    prompt: p
    feedback: { rerun: impl, max: -3 }
`,
			want: "feedback.max must be declared and >= 1",
		},
		{
			name: "rerun missing",
			yaml: `
name: g
nodes:
  - id: impl
    prompt: p
  - id: review
    depends_on: [impl]
    prompt: p
    feedback: { max: 2 }
`,
			want: "feedback needs a rerun target",
		},
		{
			name: "rerun names unknown node",
			yaml: `
name: g
nodes:
  - id: impl
    prompt: p
  - id: review
    depends_on: [impl]
    prompt: p
    feedback: { rerun: ghost, max: 2 }
`,
			want: `feedback.rerun names unknown node "ghost"`,
		},
		{
			name: "rerun is a self-loop",
			yaml: `
name: g
nodes:
  - id: review
    prompt: p
    feedback: { rerun: review, max: 2 }
`,
			want: "not a proper depends_on-ancestor",
		},
		{
			name: "rerun points forward at a descendant",
			yaml: `
name: g
nodes:
  - id: review
    prompt: p
    feedback: { rerun: later, max: 2 }
  - id: later
    depends_on: [review]
    prompt: p
`,
			want: "not a proper depends_on-ancestor",
		},
		{
			name: "rerun crosses to an unrelated branch",
			yaml: `
name: g
nodes:
  - id: impl
    prompt: p
  - id: other
    prompt: p
  - id: review
    depends_on: [impl]
    prompt: p
    feedback: { rerun: other, max: 2 }
`,
			want: "not a proper depends_on-ancestor",
		},
		{
			name: "body has a side exit",
			yaml: `
name: g
nodes:
  - id: impl
    prompt: p
  - id: metrics
    depends_on: [impl]
    prompt: p
  - id: review
    depends_on: [impl]
    prompt: p
    feedback: { rerun: impl, max: 2 }
`,
			want: "side exit",
		},
		{
			name: "gate in the body",
			yaml: `
name: g
nodes:
  - id: impl
    prompt: p
  - id: approve
    type: gate
    depends_on: [impl]
  - id: review
    depends_on: [approve]
    prompt: p
    feedback: { rerun: impl, max: 2 }
`,
			want: "contains gate",
		},
		{
			name: "gate as the declarer",
			yaml: `
name: g
nodes:
  - id: impl
    prompt: p
  - id: review
    type: gate
    depends_on: [impl]
    feedback: { rerun: impl, max: 2 }
`,
			want: "contains gate",
		},
		{
			name: "session body node with out-of-body parent",
			yaml: `
name: g
nodes:
  - id: setup
    prompt: p
  - id: impl
    depends_on: [setup]
    handoff: session
    prompt: p
  - id: review
    depends_on: [impl]
    prompt: p
    feedback: { rerun: impl, max: 2 }
`,
			want: "out-of-body parent",
		},
		{
			name: "overlapping bodies",
			yaml: `
name: g
nodes:
  - id: impl
    prompt: p
  - id: mid
    depends_on: [impl]
    prompt: p
    feedback: { rerun: impl, max: 1 }
  - id: review
    depends_on: [mid]
    prompt: p
    feedback: { rerun: mid, max: 1 }
`,
			want: "must be disjoint",
		},
		{
			name: "feedback placeholder outside the body",
			yaml: `
name: g
nodes:
  - id: impl
    prompt: p
  - id: review
    depends_on: [impl]
    prompt: p
    feedback: { rerun: impl, max: 2 }
  - id: report
    depends_on: [review]
    prompt: "summarize {{ feedback.review }}"
`,
			want: "only legal inside the body",
		},
		{
			name: "feedback placeholder naming a non-declarer",
			yaml: `
name: g
nodes:
  - id: impl
    prompt: "redo per {{ feedback.localrun }}"
  - id: localrun
    depends_on: [impl]
    prompt: p
  - id: review
    depends_on: [localrun]
    prompt: p
    feedback: { rerun: impl, max: 2 }
`,
			want: "declares no feedback edge",
		},
		{
			name: "feedback placeholder with a filter",
			yaml: `
name: g
nodes:
  - id: impl
    prompt: "redo per {{ feedback.review | inline }}"
  - id: review
    depends_on: [impl]
    prompt: p
    feedback: { rerun: impl, max: 2 }
`,
			want: "takes no filter",
		},
		{
			name: "feedback placeholder in a verify command outside the body",
			yaml: `
name: g
nodes:
  - id: impl
    prompt: p
  - id: review
    depends_on: [impl]
    prompt: p
    feedback: { rerun: impl, max: 2 }
  - id: report
    depends_on: [review]
    prompt: p
    success_check:
      verify: { command: "echo {{ feedback.review }}" }
`,
			want: "only legal inside the body",
		},
		{
			name: "feedback placeholder with a dotted declarer id outside the body",
			yaml: `
name: g
nodes:
  - id: impl
    prompt: p
  - id: review.v2
    depends_on: [impl]
    prompt: p
    feedback: { rerun: impl, max: 2 }
  - id: report
    depends_on: [review.v2]
    prompt: "summarize {{ feedback.review.v2 }}"
`,
			want: "only legal inside the body",
		},
		{
			name: "feedback placeholder in cwd outside the body",
			yaml: `
name: g
nodes:
  - id: impl
    prompt: p
  - id: review
    depends_on: [impl]
    prompt: p
    feedback: { rerun: impl, max: 2 }
  - id: report
    depends_on: [review]
    prompt: p
    cwd: "/tmp/{{ feedback.review }}"
`,
			want: "only legal inside the body",
		},
		{
			name: "feedback placeholder in a verify cwd outside the body",
			yaml: `
name: g
nodes:
  - id: impl
    prompt: p
  - id: review
    depends_on: [impl]
    prompt: p
    feedback: { rerun: impl, max: 2 }
  - id: report
    depends_on: [review]
    prompt: p
    success_check:
      verify: { command: echo, cwd: "/tmp/{{ feedback.review }}" }
`,
			want: "only legal inside the body",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml))
			if err == nil {
				t.Fatalf("Parse accepted an invalid feedback shape")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q — a different rule may have fired", err, tc.want)
			}
		})
	}
}

// TestValidate_Feedback_AcceptedShapes pins the boundaries that must NOT be
// refused: shapes that look loop-adjacent but are exactly what the ADR
// permits.
func TestValidate_Feedback_AcceptedShapes(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{
			// Parents feeding INTO the body from outside are fine — they ran
			// once and their artifacts are stable.
			name: "out-of-body parent feeding the body",
			yaml: `
name: g
nodes:
  - id: setup
    prompt: p
  - id: impl
    depends_on: [setup]
    prompt: p
  - id: review
    depends_on: [impl]
    prompt: p
    feedback: { rerun: impl, max: 1 }
`,
		},
		{
			// The declarer's own dependents legitimately sit outside the loop:
			// they consume its settled, final result.
			name: "dependent hanging off the declarer",
			yaml: `
name: g
nodes:
  - id: impl
    prompt: p
  - id: review
    depends_on: [impl]
    prompt: p
    feedback: { rerun: impl, max: 1 }
  - id: ship
    depends_on: [review]
    prompt: p
`,
		},
		{
			// The declarer referencing its own payload is inside its own body.
			name: "declarer reads its own feedback namespace",
			yaml: `
name: g
nodes:
  - id: impl
    prompt: p
  - id: review
    depends_on: [impl]
    prompt: "prior round said: {{ feedback.review }}"
    feedback: { rerun: impl, max: 1 }
`,
		},
		{
			// An in-body session chain is fine when the parent is in the body
			// too: the parent re-ran first and recorded its new session id.
			name: "in-body session chain",
			yaml: `
name: g
nodes:
  - id: impl
    prompt: p
  - id: check
    depends_on: [impl]
    handoff: session
    prompt: p
  - id: review
    depends_on: [check]
    prompt: p
    feedback: { rerun: impl, max: 1 }
`,
		},
		{
			// Two loops on branches that never share a node are both legal —
			// disjointness rejects only overlap.
			name: "disjoint loops on independent branches",
			yaml: `
name: g
nodes:
  - id: impl-a
    prompt: p
  - id: review-a
    depends_on: [impl-a]
    prompt: p
    feedback: { rerun: impl-a, max: 1 }
  - id: impl-b
    prompt: p
  - id: review-b
    depends_on: [impl-b]
    prompt: p
    feedback: { rerun: impl-b, max: 1 }
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.yaml)); err != nil {
				t.Fatalf("Parse refused a shape ADR 0010 permits: %v", err)
			}
		})
	}
}

// TestFeedbackBody_DiamondIsTheBetweenSet pins the body definition on a
// non-chain shape: the between-set includes every path from target to
// declarer, and nothing outside them.
func TestFeedbackBody_DiamondIsTheBetweenSet(t *testing.T) {
	g := parseGraph(t, `
name: diamond
nodes:
  - id: setup
    prompt: p
  - id: impl
    depends_on: [setup]
    prompt: p
  - id: lint
    depends_on: [impl]
    prompt: p
  - id: test
    depends_on: [impl]
    prompt: p
  - id: review
    depends_on: [lint, test]
    prompt: p
    feedback: { rerun: impl, max: 1 }
`)
	got := g.FeedbackBody("review")
	want := []string{"impl", "lint", "test", "review"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FeedbackBody(review) = %v, want %v (setup feeds the body but is outside it)", got, want)
	}
	if body := g.FeedbackBody("impl"); body != nil {
		t.Fatalf("FeedbackBody on a non-declarer = %v, want nil", body)
	}
}
