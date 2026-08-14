package runner

import "testing"

func TestParseRuntime(t *testing.T) {
	tests := []struct {
		input string
		want  Runtime
	}{
		{input: "", want: RuntimeClaude},
		{input: "claude", want: RuntimeClaude},
		{input: "codex", want: RuntimeCodex},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := ParseRuntime(test.input)
			if err != nil {
				t.Fatalf("ParseRuntime(%q): %v", test.input, err)
			}
			if got != test.want {
				t.Errorf("ParseRuntime(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestParseRuntimeRejectsUnknownRuntime(t *testing.T) {
	if _, err := ParseRuntime("gemini"); err == nil {
		t.Fatal("ParseRuntime(gemini) succeeded, want a closed-vocabulary error")
	}
}
