package interview

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeAsker replies from a script, one entry per call, and records each
// prompt. A call past the script's end fails the test: it is a call the
// interview should not have made.
type fakeAsker struct {
	t       *testing.T
	replies []string
	prompts []string
	cost    float64
}

func (f *fakeAsker) ask(_ context.Context, prompt string) (string, Accounting, error) {
	f.prompts = append(f.prompts, prompt)
	if len(f.prompts) > len(f.replies) {
		f.t.Fatalf("interviewer called %d time(s), the script holds %d", len(f.prompts), len(f.replies))
	}
	return f.replies[len(f.prompts)-1], Accounting{CostUSD: f.cost, Usage: Usage{InputTokens: 10, OutputTokens: 2}}, nil
}

func (f *fakeAsker) calls() int { return len(f.prompts) }

// questions scripts n distinct questions.
func questions(n int) []string {
	replies := make([]string, n)
	for i := range replies {
		replies[i] = fmt.Sprintf("QUESTION: What about edge case number %d?", i+1)
	}
	return replies
}

func run(t *testing.T, asker *fakeAsker, input string) (*Result, string, error) {
	t.Helper()
	var out strings.Builder
	result, err := Run(context.Background(), "add a --verbose flag", asker.ask, strings.NewReader(input), &out)
	if result == nil {
		t.Fatal("Run returned a nil Result")
	}
	return result, out.String(), err
}

// ADR 0044 §5 test 3
func TestEndOfInputBeforeTheFirstAnswerIsRefused(t *testing.T) {
	for name, input := range map[string]string{
		"nothing typed":     "",
		"only a skip typed": SkipLine + "\n",
		"only blank lines":  "\n  \n",
	} {
		t.Run(name, func(t *testing.T) {
			asker := &fakeAsker{t: t, replies: questions(2)}
			result, _, err := run(t, asker, input)
			if !errors.Is(err, ErrNoAnswer) {
				t.Fatalf("want ErrNoAnswer, got %v (ending %q)", err, result.Ending)
			}
			if result.Answered != 0 {
				t.Errorf("Answered = %d, want 0", result.Answered)
			}
		})
	}
}

// ADR 0044 §5 test 7
func TestARepeatedQuestionEndsTheInterview(t *testing.T) {
	asker := &fakeAsker{t: t, replies: []string{
		"QUESTION: Should --verbose also log to a file?",
		"QUESTION:   should VERBOSE   also log, to a FILE!!",
		// A third reply exists so that a third call is counted, not fatal.
		"QUESTION: Something else?",
	}}
	result, _, err := run(t, asker, "no\nyes\n")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Ending != EndRepeat {
		t.Fatalf("ending = %q, want repeat", result.Ending)
	}
	if asker.calls() != 2 {
		t.Fatalf("interviewer called %d times, want 2: a repeat ends the interview with no further call", asker.calls())
	}
	if result.Asked != 1 || len(result.Exchanges) != 1 {
		t.Errorf("asked %d, exchanges %d; the repeat must not be put to the operator", result.Asked, len(result.Exchanges))
	}
	if !strings.Contains(asker.prompts[1], "Should --verbose also log to a file?") ||
		!strings.Contains(asker.prompts[1], "Do\nnot ask any of them again") {
		t.Errorf("the second call does not carry the transcript and the no-repeat rule:\n%s", asker.prompts[1])
	}
}

// ADR 0044 §5 test 8
func TestAMalformedReplyEndsTheInterview(t *testing.T) {
	for _, reply := range []string{
		"QUESTIONS: what about tests?",
		"ENOUGHX",
		"question: lower case is not the token",
		"Sure! QUESTION: a preamble first?",
		"QUESTION:",
		"QUESTION: one?\nQUESTION: two?",
		"",
	} {
		t.Run(fmt.Sprintf("%q", reply), func(t *testing.T) {
			asker := &fakeAsker{t: t, replies: []string{reply, "QUESTION: never asked?"}}
			result, _, err := run(t, asker, "an answer\n")
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if result.Ending != EndMalformed {
				t.Fatalf("ending = %q, want malformed", result.Ending)
			}
			if asker.calls() != 1 {
				t.Errorf("interviewer called %d times; a malformed reply is not retried", asker.calls())
			}
			if result.Asked != 0 {
				t.Errorf("asked = %d, want 0", result.Asked)
			}
		})
	}
}

// ADR 0044 §5 test 9
func TestAnOverLongAnswerIsRefusedAtThePrompt(t *testing.T) {
	long := strings.Repeat("x", MaxAnswerBytes+1)
	asker := &fakeAsker{t: t, replies: []string{"QUESTION: Which log format?", "ENOUGH"}}
	result, out, err := run(t, asker, long+"\njson\n")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := fmt.Sprintf("That answer is %d bytes, over the %d-byte limit.", MaxAnswerBytes+1, MaxAnswerBytes); !strings.Contains(out, want) {
		t.Fatalf("the refusal does not name the size; output:\n%s", out)
	}
	if strings.Contains(out, long[:MaxAnswerBytes]) {
		t.Error("the refused answer was echoed back")
	}
	if len(result.Exchanges) != 1 || result.Exchanges[0].Answer != "json" {
		t.Fatalf("exchanges = %+v, want the one answer typed after the refusal", result.Exchanges)
	}
	if result.Asked != 1 {
		t.Errorf("asked = %d; a refused answer re-asks the same question, it does not ask a new one", result.Asked)
	}

	t.Run("exactly at the bound is accepted whole", func(t *testing.T) {
		atBound := strings.Repeat("y", MaxAnswerBytes)
		asker := &fakeAsker{t: t, replies: []string{"QUESTION: Which log format?", "ENOUGH"}}
		result, _, err := run(t, asker, atBound+"\r\n")
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(result.Exchanges) != 1 || result.Exchanges[0].Answer != atBound {
			t.Fatalf("an answer of exactly %d bytes was not kept whole", MaxAnswerBytes)
		}
	})
}

func TestDoneAtTheFirstPromptEndsAsOperator(t *testing.T) {
	asker := &fakeAsker{t: t, replies: questions(2)}
	result, _, err := run(t, asker, DoneLine+"\n")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Ending != EndOperator || result.Asked != 1 || result.Answered != 0 {
		t.Fatalf("ending %q, asked %d, answered %d; want operator, 1, 0", result.Ending, result.Asked, result.Answered)
	}
	if asker.calls() != 1 {
		t.Errorf("interviewer called %d times after /done", asker.calls())
	}
}

func TestSkipCountsAgainstTheCapAndIsRecorded(t *testing.T) {
	asker := &fakeAsker{t: t, replies: questions(MaxQuestions)}
	input := SkipLine + "\nyes\n" + SkipLine + "\nno\nmaybe\n"
	result, _, err := run(t, asker, input)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Ending != EndCap || result.Asked != MaxQuestions || result.Answered != 3 || result.Skipped != 2 {
		t.Fatalf("ending %q, asked %d, answered %d, skipped %d; want cap, %d, 3, 2",
			result.Ending, result.Asked, result.Answered, result.Skipped, MaxQuestions)
	}
	if !result.Exchanges[0].Skipped || result.Exchanges[0].Answer != "" || result.Exchanges[1].Answer != "yes" {
		t.Errorf("exchanges = %+v", result.Exchanges)
	}
}

func TestEndOfInputAfterAnAnswerEndsAsEOF(t *testing.T) {
	asker := &fakeAsker{t: t, replies: questions(3)}
	result, _, err := run(t, asker, "yes\nunterminated last line")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Ending != EndEOF || result.Answered != 2 || result.Asked != 3 {
		t.Fatalf("ending %q, asked %d, answered %d; want eof, 3, 2", result.Ending, result.Asked, result.Answered)
	}
	if got := result.Exchanges[1].Answer; got != "unterminated last line" {
		t.Errorf("an unterminated last line is still an answer, got %q", got)
	}
}

func TestAnAskerErrorKeepsTheSpend(t *testing.T) {
	failing := func(context.Context, string) (string, Accounting, error) {
		return "", Accounting{CostUSD: 0.02}, errors.New("spawn failed")
	}
	result, err := Run(context.Background(), "goal", failing, strings.NewReader(""), &strings.Builder{})
	if err == nil {
		t.Fatal("want the asker's error")
	}
	if result == nil || result.Cost.CostUSD != 0.02 {
		t.Fatalf("the failed call's cost must be summed, got %+v", result)
	}
}

func TestParseReply(t *testing.T) {
	for _, tc := range []struct {
		reply    string
		kind     replyKind
		question string
	}{
		{"QUESTION: Which OS?", replyQuestion, "Which OS?"},
		{"  QUESTION:Which OS?  \n", replyQuestion, "Which OS?"},
		{"ENOUGH", replyEnough, ""},
		{"\nENOUGH.\n", replyEnough, ""},
		{"ENOUGH — nothing left", replyEnough, ""},
		{"QUESTIONS: Which OS?", replyMalformed, ""},
		{"ENOUGHX", replyMalformed, ""},
		{"ENOUGH_", replyMalformed, ""},
		{"Enough", replyMalformed, ""},
		{"**QUESTION:** Which OS?", replyMalformed, ""},
		{"QUESTION:   ", replyMalformed, ""},
		{"QUESTION: a?\nand b?", replyMalformed, ""},
		{"I have no questions.", replyMalformed, ""},
	} {
		kind, question := parseReply(tc.reply)
		if kind != tc.kind || question != tc.question {
			t.Errorf("parseReply(%q) = %v, %q; want %v, %q", tc.reply, kind, question, tc.kind, tc.question)
		}
	}
}

func TestNormalise(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Should it LOG to a file?", "should it log to a file"},
		{"  should   it log, to a FILE!! ", "should it log to a file"},
		{"Use\ttabs\nor spaces?", "use tabs or spaces"},
		{"Ünïcode — Größe?", "ünïcode größe"},
		{"v1.2 or v12?", "v12 or v12"},
		{"?!", ""},
	} {
		if got := normalise(tc.in); got != tc.want {
			t.Errorf("normalise(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
