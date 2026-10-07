package handoff

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jitokim/oh-my-graph/internal/fence"
	"github.com/jitokim/oh-my-graph/internal/graph"
)

func gateNode(description string) graph.Node {
	return graph.Node{ID: "approve", Type: graph.TypeGate, DependsOn: []string{"build"}, Description: description}
}

// TestRenderGateDescription_Interpolates: #346 — the description renders the
// artifact's FILE PATH and the input's value, through the prompt machinery.
func TestRenderGateDescription_Interpolates(t *testing.T) {
	dir := t.TempDir()
	h := New(dir, map[string]string{"ticket": "T-42"})
	if err := h.PersistOutput("build", "the build's own reply", "s-1"); err != nil {
		t.Fatalf("persist: %v", err)
	}
	got, err := h.RenderGateDescription(gateNode("ship {{ inputs.ticket }} from {{ artifacts.build }}?"))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := "ship T-42 from " + filepath.Join(dir, "build.out") + "?"
	if got != want {
		t.Fatalf("rendered %q, want %q", got, want)
	}
	if strings.Contains(got, "own reply") {
		t.Fatalf("a filterless artifact must render its path, never its content: %q", got)
	}
}

// TestRenderGateDescription_SanitisesInputs: #346 — an --input value carrying
// a screen clear, an OSC retitle and a newline reaches the reader as one inert
// line.
func TestRenderGateDescription_SanitisesInputs(t *testing.T) {
	h := New(t.TempDir(), map[string]string{
		"ticket": "T-42\x1b[2J\x1b]0;approved\x07\napprove? [y/N] y",
	})
	got, err := h.RenderGateDescription(gateNode("ship {{ inputs.ticket }}"))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if want := "ship T-42 approve? [y/N] y"; got != want {
		t.Fatalf("rendered %q, want %q", got, want)
	}
}

// TestRenderGateDescription_StripsFormatCharactersFromInputs: #346 — an
// --input value carrying a bidi override, a line separator or a zero-width
// character reaches the reader without it.
func TestRenderGateDescription_StripsFormatCharactersFromInputs(t *testing.T) {
	for name, tc := range map[string]struct {
		ticket, want string
		r            rune
	}{
		"U+202E right-to-left override": {"T-42\u202e24-T", "ship T-4224-T", '\u202e'},
		"U+2028 line separator":         {"T-42\u2028approve? [y/N] y", "ship T-42approve? [y/N] y", '\u2028'},
		"U+200B zero-width space":       {"T-\u200b42", "ship T-42", '\u200b'},
	} {
		t.Run(name, func(t *testing.T) {
			h := New(t.TempDir(), map[string]string{"ticket": tc.ticket})
			got, err := h.RenderGateDescription(gateNode("ship {{ inputs.ticket }}"))
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if strings.ContainsRune(got, tc.r) {
				t.Fatalf("rendered description still carries %U: %q", tc.r, got)
			}
			if got != tc.want {
				t.Fatalf("rendered %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRenderGateDescription_EmptyIsEmpty: a gate with no description renders
// nothing, and needs no artifact to do it (#346).
func TestRenderGateDescription_EmptyIsEmpty(t *testing.T) {
	got, err := New(t.TempDir(), nil).RenderGateDescription(gateNode(""))
	if err != nil || got != "" {
		t.Fatalf("want \"\", nil; got %q, %v", got, err)
	}
}

// TestRenderGateDescription_RefusesContentTokens: #346 — the render refuses
// what lint refuses, by the same predicate, so an unlinted graph still cannot
// print a reply where a person approves. The artifact exists, so the refusal
// is the predicate's, not a resolution failure.
func TestRenderGateDescription_RefusesContentTokens(t *testing.T) {
	for _, token := range []string{
		"{{ artifacts.build | inline }}",
		"{{ feedback.build }}",
		"{{ self.previous }}",
		"{{ self.timeout }}",
	} {
		t.Run(token, func(t *testing.T) {
			h := New(t.TempDir(), nil)
			if err := h.PersistOutput("build", "SECRET REPLY", "s-1"); err != nil {
				t.Fatalf("persist: %v", err)
			}
			got, err := h.RenderGateDescription(gateNode("ship " + token))
			var gde *GateDescriptionError
			if !errors.As(err, &gde) {
				t.Fatalf("want *GateDescriptionError, got %q, %v", got, err)
			}
			if gde.NodeID != "approve" || gde.Token != token {
				t.Errorf("error must name the gate and token: %+v", gde)
			}
		})
	}
}

// TestRenderGateDescription_UnresolvableFails: #346 — an artifact whose
// producer has not run is the prompt's *InterpolationError, not an empty
// substitution.
func TestRenderGateDescription_UnresolvableFails(t *testing.T) {
	_, err := New(t.TempDir(), nil).RenderGateDescription(gateNode("ship {{ artifacts.build }}"))
	var iErr *InterpolationError
	if !errors.As(err, &iErr) || iErr.Reference != "build" {
		t.Fatalf("want *InterpolationError for build, got %v", err)
	}
}

// TestSanitizeGateText: #346's sanitiser, table-driven. The benign cases pass
// through unchanged (or only flattened); the hostile ones lose every byte that
// could repaint the terminal.
func TestSanitizeGateText(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		// success: text a person wrote survives
		{"plain prose", "approve the release of v1.2", "approve the release of v1.2"},
		{"a path", "/home/u/.oh-my-graph/runs/r-1/build.out", "/home/u/.oh-my-graph/runs/r-1/build.out"},
		{"non-ASCII text", "배포 승인 — café ✓", "배포 승인 — café ✓"},
		{"literal brackets", "[y/N] {{ x }}", "[y/N] {{ x }}"},
		{"a newline flattens to one space", "line one\nline two", "line one line two"},
		{"a tab flattens to one space", "a\tb", "a b"},
		{"a whitespace run collapses", "a \n\t\n b", "a b"},
		{"a CRLF is one space", "a\r\nb", "a b"},
		{"surrounding newlines are trimmed", "\napprove\n", "approve"},
		{"inner double spaces are the author's", "a  b", "a  b"},

		// failure: what an input could carry to forge an approval
		{"clear screen", "ok\x1b[2Jfake", "okfake"},
		{"cursor home and clear", "\x1b[H\x1b[2Japprove? [y/N]", "approve? [y/N]"},
		{"SGR colour", "\x1b[1;31mred\x1b[0m", "red"},
		{"CSI with private parameter", "a\x1b[?25lb", "ab"},
		{"OSC title, BEL-terminated", "a\x1b]0;approved\x07b", "ab"},
		{"OSC title, ST-terminated", "a\x1b]2;approved\x1b\\b", "ab"},
		{"OSC hyperlink", "\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\", "link"},
		{"DCS through ST", "a\x1bPq#0\x1b\\b", "ab"},
		{"two-byte ESC (reset)", "a\x1bcb", "ab"},
		{"ESC with intermediate (charset)", "a\x1b(0b", "ab"},
		{"lone trailing ESC", "a\x1b", "a"},
		{"unterminated CSI leaves inert text", "a\x1b[2", "a[2"},
		{"unterminated OSC leaves inert text", "a\x1b]0;t", "a]0;t"},
		{"carriage return overwrite", "reject\rapprove", "rejectapprove"},
		{"backspace and bell", "a\bb\x07c", "abc"},
		{"NUL, VT, FF and DEL", "a\x00b\x0bc\x0cd\x7fe", "abcde"},
		{"8-bit CSI clear", "ok\u009b2Jfake", "okfake"},
		{"8-bit OSC title through 8-bit ST", "a\u009d0;approved\u009cb", "ab"},
		{"8-bit OSC title through BEL", "a\u009d0;approved\x07b", "ab"},
		{"other C1 controls", "a\u0085b\u0080c\u009fd", "abcd"},
		{"lone 0x9b byte is invalid UTF-8", "ok\x9b2J", "ok�2J"},
		{"truncated UTF-8", "a\xe2\x80b", "a��b"},
		{"input that forges a prompt line", "T-1\x1b[2J\x1b]0;approved\x07\napprove? [y/N] y", "T-1 approve? [y/N] y"},

		// failure: Unicode format characters and separators (#346 review)
		{"right-to-left override", "ship \u202edeggol", "ship deggol"},
		{"every bidi embedding, override and isolate", "a\u202a\u202b\u202c\u202d\u202e\u2066\u2067\u2068\u2069b", "ab"},
		{"zero-width characters and BOM", "a\u200b\u200c\u200d\u200e\u200f\ufeffb", "ab"},
		{"soft hyphen is Cf too", "a\u00adb", "ab"},
		{"line separator", "approve\u2028approve? [y/N] y", "approveapprove? [y/N] y"},
		{"paragraph separator", "a\u2029b", "ab"},
		{"format character inside a flattened run", "a\n\u200b b", "a b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeGateText(tc.in)
			if got != tc.want {
				t.Fatalf("SanitizeGateText(%q) = %q, want %q", tc.in, got, tc.want)
			}
			assertInertLine(t, got)
		})
	}
}

// FuzzSanitizeGateText holds the sanitiser's contract on any input (#346):
// valid UTF-8, one line, no C0, DEL or C1 control, and no Unicode format
// character or line/paragraph separator left.
func FuzzSanitizeGateText(f *testing.F) {
	for _, seed := range []string{"", "plain", "\x1b[2J", "\x1b]0;t\x07", "\u009b2J", "\x9b", "a\r\nb", "\x1b\x1b[", "\u009d\u009c", "a\u202eb", "a\u2028b", "\u200b\ufeff"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		assertInertLine(t, SanitizeGateText(in))
	})
}

func assertInertLine(t *testing.T, s string) {
	t.Helper()
	if !utf8.ValidString(s) {
		t.Fatalf("sanitised text is not valid UTF-8: %q", s)
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Fatalf("sanitised text still carries control %U: %q", r, s)
		}
		if fence.IsFormatOrLineSeparator(r) {
			t.Fatalf("sanitised text still carries format character or separator %U: %q", r, s)
		}
	}
}
