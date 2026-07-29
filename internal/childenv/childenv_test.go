package childenv

import "testing"

// TestScrub_RemovesBillingSwitchingVars is the policy itself: both variables
// that flip claude from subscription (OAuth) to metered API billing must be
// gone from any child environment built here.
func TestScrub_RemovesBillingSwitchingVars(t *testing.T) {
	got := Scrub([]string{
		"ANTHROPIC_API_KEY=sk-should-be-scrubbed",
		"ANTHROPIC_AUTH_TOKEN=tok-should-be-scrubbed",
		"PATH=/usr/bin",
	})

	for _, kv := range got {
		if kv == "ANTHROPIC_API_KEY=sk-should-be-scrubbed" {
			t.Error("ANTHROPIC_API_KEY survived the scrub")
		}
		if kv == "ANTHROPIC_AUTH_TOKEN=tok-should-be-scrubbed" {
			t.Error("ANTHROPIC_AUTH_TOKEN survived the scrub")
		}
	}
	if !contains(got, "PATH=/usr/bin") {
		t.Errorf("scrub removed a benign variable; got %q", got)
	}
}

// TestScrub_MatchesOnKeyNotSubstring proves the scrub is surgical: it removes
// exactly the two keys, not anything that merely mentions them. A blunt
// substring match would strip a user's unrelated variables out of every child
// and break real runs.
func TestScrub_MatchesOnKeyNotSubstring(t *testing.T) {
	got := Scrub([]string{
		"ANTHROPIC_API_KEY=secret",
		"MY_NOTE=ANTHROPIC_API_KEY is scrubbed elsewhere",
		"ANTHROPIC_AUTH_TOKEN_BACKUP=keep-me",
		"NOT_ANTHROPIC_API_KEY=keep-me-too",
	})

	if contains(got, "ANTHROPIC_API_KEY=secret") {
		t.Error("exact-key ANTHROPIC_API_KEY was not scrubbed")
	}
	for _, want := range []string{
		"MY_NOTE=ANTHROPIC_API_KEY is scrubbed elsewhere",
		"ANTHROPIC_AUTH_TOKEN_BACKUP=keep-me",
		"NOT_ANTHROPIC_API_KEY=keep-me-too",
	} {
		if !contains(got, want) {
			t.Errorf("wrongly scrubbed %q; got %q", want, got)
		}
	}
}

// TestScrub_DoesNotMutateInput pins that the caller's slice is left alone —
// both spawners pass os.Environ()'s result and one of them building its child
// env must never disturb the other's view of the parent environment.
func TestScrub_DoesNotMutateInput(t *testing.T) {
	parent := []string{"ANTHROPIC_API_KEY=secret", "PATH=/usr/bin"}
	Scrub(parent)

	if len(parent) != 2 || parent[0] != "ANTHROPIC_API_KEY=secret" {
		t.Errorf("Scrub mutated its input: %q", parent)
	}
}

// TestScrub_EmptyParentYieldsEmptyEnv covers the degenerate input: an empty
// parent environment must produce an empty (never nil-surprising) child env.
func TestScrub_EmptyParentYieldsEmptyEnv(t *testing.T) {
	if got := Scrub(nil); len(got) != 0 {
		t.Errorf("Scrub(nil) = %q, want empty", got)
	}
}

func contains(env []string, want string) bool {
	for _, kv := range env {
		if kv == want {
			return true
		}
	}
	return false
}
