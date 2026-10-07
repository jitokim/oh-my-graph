package runstate

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// writeAndReadBack writes s to a fresh state.json and returns the raw bytes
// on disk alongside what Load decodes from them.
func writeAndReadBack(t *testing.T, s Snapshot) ([]byte, Snapshot) {
	t.Helper()
	path := filepath.Join(t.TempDir(), SnapshotFileName)
	if err := Write(path, s); err != nil {
		t.Fatalf("Write: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return raw, loaded
}

// assertInputEscapedOnDisk is the shared body of the per-character tests
// below: an --input value carrying r is absent from state.json's raw bytes
// and comes back from Load byte-identical.
func assertInputEscapedOnDisk(t *testing.T, r rune) {
	t.Helper()
	value := "a" + string(r) + "b"
	s := sampleSnapshot()
	s.Inputs = map[string]string{"label": value}

	raw, loaded := writeAndReadBack(t, s)
	if bytes.ContainsRune(raw, r) {
		t.Fatalf("state.json carries %U raw:\n%s", r, raw)
	}
	if got := loaded.Inputs["label"]; got != value {
		t.Fatalf("Load returned input %q, want %q", got, value)
	}
}

// TestWrite_RightToLeftOverrideInInputIsEscaped: #346 — a bidi override
// (U+202E) in an --input value never reaches state.json raw, and Load returns
// the value unchanged.
func TestWrite_RightToLeftOverrideInInputIsEscaped(t *testing.T) {
	assertInputEscapedOnDisk(t, '\u202e')
}

// TestWrite_LineSeparatorInInputIsEscaped: #346 — a line separator (U+2028) in
// an --input value never reaches state.json raw, and Load returns the value
// unchanged.
func TestWrite_LineSeparatorInInputIsEscaped(t *testing.T) {
	assertInputEscapedOnDisk(t, '\u2028')
}

// TestWrite_ZeroWidthSpaceInInputIsEscaped: #346 — a zero-width space (U+200B)
// in an --input value never reaches state.json raw, and Load returns the value
// unchanged.
func TestWrite_ZeroWidthSpaceInInputIsEscaped(t *testing.T) {
	assertInputEscapedOnDisk(t, '\u200b')
}

// TestWrite_SupplementaryFormatCharacterInGraphSurvivesParse: #346 — a format
// character above U+FFFF (U+E0001 LANGUAGE TAG) in a node prompt is written as
// a UTF-16 surrogate-pair escape, and the stored Graph still re-parses through
// graph.Parse, as resume does, to the exact prompt it was written with.
func TestWrite_SupplementaryFormatCharacterInGraphSurvivesParse(t *testing.T) {
	const r = '\U000E0001'
	prompt := "dev" + string(r) + "work"
	g, err := json.Marshal(map[string]any{
		"name":  "g",
		"nodes": []map[string]string{{"id": "dev", "prompt": prompt}},
	})
	if err != nil {
		t.Fatalf("marshal graph: %v", err)
	}
	s := sampleSnapshot()
	s.Graph = g

	raw, loaded := writeAndReadBack(t, s)
	if bytes.ContainsRune(raw, r) {
		t.Fatalf("state.json carries %U raw:\n%s", r, raw)
	}
	if !bytes.Contains(raw, []byte("\\udb40\\udc01")) {
		t.Fatalf("state.json does not carry %U as its surrogate-pair escape:\n%s", r, raw)
	}
	parsed, err := graph.Parse(loaded.Graph)
	if err != nil {
		t.Fatalf("graph.Parse of the stored graph: %v", err)
	}
	if got := parsed.Nodes[0].Prompt; got != prompt {
		t.Fatalf("prompt after Write -> Load -> graph.Parse is %q, want %q", got, prompt)
	}
}

// TestLoad_EscapedBackslashBeforeUIsNotASurrogatePair: #346 — a prompt whose
// text is a backslash followed by "udb40\udc01" is written as an escaped
// backslash, and Load's surrogate-pair decode of Graph leaves it as that text
// rather than turning it into U+E0001.
func TestLoad_EscapedBackslashBeforeUIsNotASurrogatePair(t *testing.T) {
	prompt := `dev 󠀁 work`
	g, err := json.Marshal(map[string]any{
		"name":  "g",
		"nodes": []map[string]string{{"id": "dev", "prompt": prompt}},
	})
	if err != nil {
		t.Fatalf("marshal graph: %v", err)
	}
	s := sampleSnapshot()
	s.Graph = g

	_, loaded := writeAndReadBack(t, s)
	parsed, err := graph.Parse(loaded.Graph)
	if err != nil {
		t.Fatalf("graph.Parse of the stored graph: %v", err)
	}
	if got := parsed.Nodes[0].Prompt; got != prompt {
		t.Fatalf("prompt after Write -> Load -> graph.Parse is %q, want %q", got, prompt)
	}
}
