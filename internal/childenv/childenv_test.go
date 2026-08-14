package childenv

import "testing"

// TestScrub_RemovesBillingSwitchingVars is the policy itself: both variables
// that flip claude from subscription (OAuth) to metered API billing must be
// gone from any child environment built here.
func TestScrub_RemovesBillingSwitchingVars(t *testing.T) {
	got := Scrub([]string{
		"ANTHROPIC_API_KEY=sk-should-be-scrubbed",
		"ANTHROPIC_AUTH_TOKEN=tok-should-be-scrubbed",
		"OPENAI_API_KEY=sk-openai-should-be-scrubbed",
		"CODEX_API_KEY=sk-codex-should-be-scrubbed",
		"PATH=/usr/bin",
	})

	for _, kv := range got {
		if kv == "ANTHROPIC_API_KEY=sk-should-be-scrubbed" {
			t.Error("ANTHROPIC_API_KEY survived the scrub")
		}
		if kv == "ANTHROPIC_AUTH_TOKEN=tok-should-be-scrubbed" {
			t.Error("ANTHROPIC_AUTH_TOKEN survived the scrub")
		}
		if kv == "OPENAI_API_KEY=sk-openai-should-be-scrubbed" {
			t.Error("OPENAI_API_KEY survived the scrub")
		}
		if kv == "CODEX_API_KEY=sk-codex-should-be-scrubbed" {
			t.Error("CODEX_API_KEY survived the scrub")
		}
	}
	if !contains(got, "PATH=/usr/bin") {
		t.Errorf("scrub removed a benign variable; got %q", got)
	}
}

// TestScrub_MatchesOnKeyNotSubstring proves the scrub is surgical: it removes
// exactly the two keys, not anything that merely mentions them. A blunt
// substring match would strip a user's unrelated variables out of every child
// and break real runs. The case-folded survivors pin that matching the key
// without regard to case did not loosen into prefix matching.
func TestScrub_MatchesOnKeyNotSubstring(t *testing.T) {
	got := Scrub([]string{
		"ANTHROPIC_API_KEY=secret",
		"MY_NOTE=ANTHROPIC_API_KEY is scrubbed elsewhere",
		"ANTHROPIC_AUTH_TOKEN_BACKUP=keep-me",
		"NOT_ANTHROPIC_API_KEY=keep-me-too",
		"anthropic_api_key_backup=keep-me-three",
		"not_anthropic_auth_token=keep-me-four",
	})

	if contains(got, "ANTHROPIC_API_KEY=secret") {
		t.Error("whole-key ANTHROPIC_API_KEY was not scrubbed")
	}
	for _, want := range []string{
		"MY_NOTE=ANTHROPIC_API_KEY is scrubbed elsewhere",
		"ANTHROPIC_AUTH_TOKEN_BACKUP=keep-me",
		"NOT_ANTHROPIC_API_KEY=keep-me-too",
		"anthropic_api_key_backup=keep-me-three",
		"not_anthropic_auth_token=keep-me-four",
	} {
		if !contains(got, want) {
			t.Errorf("wrongly scrubbed %q; got %q", want, got)
		}
	}
}

// TestScrub_MatchesKeyCaseInsensitively is the Windows half of the policy,
// asserted on every platform. Windows resolves environment variable names
// case-insensitively, so a child there reads `anthropic_api_key` as the same
// billing switch as `ANTHROPIC_API_KEY`; a scrub that compared keys exactly
// would pass it through and put a run the user was told is inside their plan
// onto metered API billing. The rule is unconditional precisely so this test
// runs on Linux CI — revert Scrub to an exact-case comparison and every case
// variant below survives and fails here.
func TestScrub_MatchesKeyCaseInsensitively(t *testing.T) {
	variants := []string{
		"anthropic_api_key=sk-lowercase",
		"Anthropic_Api_Key=sk-mixed",
		"ANTHROPIC_API_key=sk-partly-lowered",
		"anthropic_auth_token=tok-lowercase",
		"Anthropic_Auth_Token=tok-mixed",
		"ANTHROPIC_AUTH_token=tok-partly-lowered",
		"openai_api_key=sk-openai-lowercase",
		"OpenAI_Api_Key=sk-openai-mixed",
		"codex_api_key=sk-codex-lowercase",
		"Codex_Api_Key=sk-codex-mixed",
	}

	got := Scrub(append(append([]string{}, variants...), "PATH=/usr/bin"))

	for _, variant := range variants {
		if contains(got, variant) {
			t.Errorf("case variant %q survived the scrub; on Windows the child "+
				"reads it as the API-billing switch", variant)
		}
	}
	if !contains(got, "PATH=/usr/bin") {
		t.Errorf("scrub removed a benign variable; got %q", got)
	}
}

// TestScrub_DoesNotMutateInput pins that the caller's slice is left alone — all
// four spawners pass os.Environ()'s result and one of them building its child
// env must never disturb the others' view of the parent environment.
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
