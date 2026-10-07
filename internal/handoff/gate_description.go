package handoff

import (
	"strings"
	"unicode/utf8"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// RenderGateDescription interpolates gate node's `description:` against the
// run as it stands when the gate pauses, and returns it sanitised for a
// terminal (#346): "" for a gate with no description.
//
// It goes through InterpolateAs, the prompt machinery itself, so an artifact
// resolves to its persisted FILE PATH and an input to its bound value exactly
// as they would in a prompt, and an unresolvable reference is the same
// *InterpolationError. Before that, every well-formed token is judged by
// gateDescriptionTokenRefused — the predicate GateDescriptionIssues refuses
// with at lint — and a refused one is a *GateDescriptionError, so a graph that
// reached the scheduler without being linted still cannot print a model's
// reply where a person approves. The interpolated text, which carries the
// operator's own input values verbatim, then goes through SanitizeGateText.
func (h *Handoff) RenderGateDescription(gate graph.Node) (string, error) {
	if gate.Description == "" {
		return "", nil
	}
	for _, token := range placeholderPattern.FindAllString(gate.Description, -1) {
		if reason := gateDescriptionTokenRefused(token); reason != "" {
			return "", &GateDescriptionError{NodeID: gate.ID, Token: token, Reason: reason}
		}
	}
	text, err := h.InterpolateAs(Self{ID: gate.ID}, gate.Description)
	if err != nil {
		return "", err
	}
	return SanitizeGateText(text), nil
}

// SanitizeGateText makes s safe to print as ONE line in front of a person
// deciding a gate (#346): nothing in it — an --input value above all — can
// move the cursor, clear the screen, retitle the window or otherwise repaint
// the terminal into a different-looking approval.
//
// It removes:
//
//   - ESC together with the complete escape sequence it begins: a CSI
//     (ESC [ params intermediates final), a string sequence — OSC (ESC ]),
//     DCS (ESC P), SOS (ESC X), PM (ESC ^), APC (ESC _) — through its BEL or
//     ST terminator, and any other ESC sequence (ESC intermediates final, the
//     two-byte ESC c and its kin). An ESC that begins no complete sequence is
//     removed on its own, leaving the bytes after it as inert printable text;
//   - carriage return, every other C0 control, and DEL;
//   - every C1 control, U+0080 to U+009F. The 8-bit introducers CSI (U+009B),
//     OSC (U+009D), DCS, SOS, PM and APC take their complete sequence with
//     them, exactly as their ESC spellings do.
//
// Newline and tab are FLATTENED, not removed: each becomes a space, and a run
// of them (with any spaces around it) collapses to one, so words either side
// stay apart and the result is a single line. A description that could break
// the line could print a line of its own beneath it — "approve? [y/N]" — that
// the engine never wrote. Leading and trailing space is trimmed.
//
// Invalid UTF-8 never passes through raw: each invalid byte becomes U+FFFD,
// so a lone 0x9B byte (8-bit CSI on a non-UTF-8 terminal) cannot reach the
// screen either.
func SanitizeGateText(s string) string {
	var b strings.Builder
	flattened := false // the last thing written was a space standing for a newline/tab run
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			b.WriteRune(utf8.RuneError)
			flattened = false
		case r == 0x1b:
			size = escapeSequenceLen(s, i)
		case c1StringIntroducer(r):
			if end, ok := stringSequenceEnd(s, i+size); ok {
				size = end - i
			}
		case r == 0x9b:
			if end, ok := csiEnd(s, i+size); ok {
				size = end - i
			}
		case r == '\n' || r == '\t':
			if !strings.HasSuffix(b.String(), " ") {
				b.WriteByte(' ')
			}
			flattened = true
		case r == ' ' && flattened:
			// already one space for this run
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			// a C0 control (CR included), DEL, or a C1 control
		default:
			b.WriteString(s[i : i+size])
			flattened = false
		}
		i += size
	}
	return strings.TrimSpace(b.String())
}

// escapeSequenceLen returns how many bytes, from the ESC at s[i], the escape
// sequence it begins occupies — 1 (the ESC alone) when it begins no complete
// sequence.
func escapeSequenceLen(s string, i int) int {
	j := i + 1
	if j >= len(s) {
		return 1
	}
	switch c := s[j]; {
	case c == '[':
		if end, ok := csiEnd(s, j+1); ok {
			return end - i
		}
	case c == ']' || c == 'P' || c == 'X' || c == '^' || c == '_':
		if end, ok := stringSequenceEnd(s, j+1); ok {
			return end - i
		}
	case c >= 0x20 && c <= 0x7e:
		// nF (ESC intermediates final) or a two-byte Fp/Fe/Fs sequence.
		for j < len(s) && s[j] >= 0x20 && s[j] <= 0x2f {
			j++
		}
		if j < len(s) && s[j] >= 0x30 && s[j] <= 0x7e {
			return j + 1 - i
		}
	}
	return 1
}

// csiEnd scans a control sequence's body from s[j] — parameter bytes
// 0x30–0x3F, intermediate bytes 0x20–0x2F, one final byte 0x40–0x7E — and
// returns the index just past the final byte, or false when the body is cut
// short or broken.
func csiEnd(s string, j int) (int, bool) {
	for j < len(s) && s[j] >= 0x30 && s[j] <= 0x3f {
		j++
	}
	for j < len(s) && s[j] >= 0x20 && s[j] <= 0x2f {
		j++
	}
	if j < len(s) && s[j] >= 0x40 && s[j] <= 0x7e {
		return j + 1, true
	}
	return 0, false
}

// stringSequenceEnd finds the terminator of an OSC/DCS/SOS/PM/APC body that
// starts at s[j] — BEL, ESC \ or the C1 ST (U+009C) — and returns the index
// just past it, or false when the sequence is never terminated.
func stringSequenceEnd(s string, j int) (int, bool) {
	for k := j; k < len(s); k++ {
		switch {
		case s[k] == 0x07:
			return k + 1, true
		case s[k] == 0x1b && k+1 < len(s) && s[k+1] == '\\':
			return k + 2, true
		case strings.HasPrefix(s[k:], "\u009c"):
			return k + len("\u009c"), true
		}
	}
	return 0, false
}

// c1StringIntroducer reports whether r is the 8-bit form of a string-sequence
// introducer: DCS, SOS, OSC, PM or APC.
func c1StringIntroducer(r rune) bool {
	return r == 0x90 || r == 0x98 || r == 0x9d || r == 0x9e || r == 0x9f
}
