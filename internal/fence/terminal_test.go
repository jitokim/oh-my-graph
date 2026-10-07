package fence

import (
	"testing"
	"unicode/utf8"
)

// TestSanitizeTerminalLine: the terminal sanitiser (#346, #349),
// table-driven. The benign cases pass through unchanged (or only flattened);
// the hostile ones lose every byte that could repaint the terminal.
func TestSanitizeTerminalLine(t *testing.T) {
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
			got := SanitizeTerminalLine(tc.in)
			if got != tc.want {
				t.Fatalf("SanitizeTerminalLine(%q) = %q, want %q", tc.in, got, tc.want)
			}
			assertInertLine(t, got)
		})
	}
}

// FuzzSanitizeTerminalLine holds the sanitiser's contract on any input (#346, #349):
// valid UTF-8, one line, no C0, DEL or C1 control, and no Unicode format
// character or line/paragraph separator left.
func FuzzSanitizeTerminalLine(f *testing.F) {
	for _, seed := range []string{"", "plain", "\x1b[2J", "\x1b]0;t\x07", "\u009b2J", "\x9b", "a\r\nb", "\x1b\x1b[", "\u009d\u009c", "a\u202eb", "a\u2028b", "\u200b\ufeff"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		assertInertLine(t, SanitizeTerminalLine(in))
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
		if IsFormatOrLineSeparator(r) {
			t.Fatalf("sanitised text still carries format character or separator %U: %q", r, s)
		}
	}
}
