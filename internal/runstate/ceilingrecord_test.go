package runstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// rawPolicies decodes a marshalled snapshot's `tool_policies` loosely enough to
// ask whether a KEY is present — which is the question on both sides of ADR
// 0040: `setting_sources` records layer 1 by being ABSENT, and
// allowed_tools_is_a_ceiling records the consequence by always being there.
// A typed decode would collapse both questions into the same nil.
func rawPolicies(t *testing.T, encoded []byte) map[string]map[string]json.RawMessage {
	t.Helper()
	var snap struct {
		ToolPolicies map[string]map[string]json.RawMessage `json:"tool_policies"`
	}
	if err := json.Unmarshal(encoded, &snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if len(snap.ToolPolicies) == 0 {
		t.Fatalf("snapshot records no tool_policies at all:\n%s", encoded)
	}
	return snap.ToolPolicies
}

// TestMarshal_EveryPolicySaysWhetherItWasACeiling is ADR 0040's presence
// assertion: the derived key is written for BOTH postures, with the value that
// distinguishes them, and it is written by marshalling the type — not by a
// helper one writer remembers to call.
//
// Both arms matter and for different reasons. The `false` arm is the defect:
// a policy whose allowed_tools list did not bind used to say nothing at all.
// The `true` arm is what keeps the fix from reintroducing the same shape one
// level up — a key present only when false would be a second field that means
// something by its absence, which is the thing being fixed.
func TestMarshal_EveryPolicySaysWhetherItWasACeiling(t *testing.T) {
	snap := sampleSnapshot()
	snap.ToolPolicies["reply-drafts"] = NodeToolPolicy{
		AllowedTools: []string{"Read", "Grep", "Glob", "Write", "Edit", "Bash(git *)"},
		Tools:        []string{"Read", "Grep", "Glob", "Write", "Edit", "Bash"},
		// nil: layer 1 off, so the six-entry list above is a declaration and
		// not a limit — the state run 20260907-044244.824270000-1 was read
		// backwards.
		SettingSources: nil,
	}

	encoded, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	policies := rawPolicies(t, encoded)

	for id, want := range map[string]string{"dev": "true", "reply-drafts": "false"} {
		policy, present := policies[id]
		if !present {
			t.Fatalf("node %q lost its policy entirely: %s", id, encoded)
		}
		raw, present := policy[AllowedToolsIsACeilingKey]
		if !present {
			t.Errorf("node %q recorded no %s, so a reader is back to inferring the ceiling from an absence: %v",
				id, AllowedToolsIsACeilingKey, policy)
			continue
		}
		if string(raw) != want {
			t.Errorf("node %q recorded %s = %s, want %s", id, AllowedToolsIsACeilingKey, raw, want)
		}
	}

	// The mechanism is untouched: `setting_sources` still records layer 1 off
	// as an absent key (ADR 0032 §2.7), which is what the resume tests in
	// cmd/oh-my-graph assert on the raw bytes.
	if raw, present := policies["reply-drafts"]["setting_sources"]; present {
		t.Errorf("the unisolated policy grew setting_sources = %s; ADR 0040 records the consequence and leaves the mechanism alone", raw)
	}
	if raw := policies["dev"]["setting_sources"]; string(raw) != `""` {
		t.Errorf("the isolated policy recorded setting_sources = %s, want \"\"", raw)
	}
}

// TestMarshal_TheDerivedKeyIsNotStored guards the property that answers ADR
// 0032 §2.7's objection to a second snapshot field — that it "could only ever
// disagree with the argv". It cannot disagree because nothing reads it back:
// decoding a policy whose recorded ceiling contradicts its own
// `setting_sources` yields the pointer, and re-encoding recomputes the key.
func TestMarshal_TheDerivedKeyIsNotStored(t *testing.T) {
	// A liar: the key says the list bound, the mechanism says it did not.
	lie := []byte(`{"allowed_tools":["Read"],"allowed_tools_is_a_ceiling":true}`)

	var policy NodeToolPolicy
	if err := json.Unmarshal(lie, &policy); err != nil {
		t.Fatalf("decode policy: %v", err)
	}
	if policy.SettingSources != nil {
		t.Fatalf("SettingSources = %q, want nil: the derived key must not be able to invent layer 1", *policy.SettingSources)
	}

	encoded, err := json.Marshal(policy)
	if err != nil {
		t.Fatalf("re-encode policy: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("decode re-encoded policy: %v", err)
	}
	if string(got[AllowedToolsIsACeilingKey]) != "false" {
		t.Errorf("re-encoding carried the lie forward as %s; the key must be derived at every encode",
			got[AllowedToolsIsACeilingKey])
	}
}

// TestLoad_SnapshotWrittenBeforeTheCeilingRecord is the back-compat gate. The
// fixture is a schema-3 snapshot as they were written before ADR 0040 — no
// `allowed_tools_is_a_ceiling` anywhere, and no `setting_sources` key either,
// which is the unisolated state 251 of 365 recorded node policies were in when
// ADR 0040 §2 counted them (the command is quoted there; the corpus is one
// maintainer's machine, so the figure does not reproduce here).
//
// It must load, and it must still mean what it meant: a nil SettingSources, so
// resume rehydrates the same posture the first leg ran and does not silently
// re-isolate a run the operator opted out of.
func TestLoad_SnapshotWrittenBeforeTheCeilingRecord(t *testing.T) {
	fixture := filepath.Join("testdata", "pre-ceiling-record-state.json")
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	// The fixture is only evidence if it really lacks both keys; a later edit
	// that added either one would make this test pass by testing nothing.
	for _, key := range []string{AllowedToolsIsACeilingKey, "setting_sources"} {
		for id, policy := range rawPolicies(t, raw) {
			if _, present := policy[key]; present {
				t.Fatalf("fixture is not a pre-ADR-0040 snapshot: node %q already carries %q", id, key)
			}
		}
	}

	snap, err := Load(fixture)
	if err != nil {
		t.Fatalf("a snapshot written before this change must still load: %v", err)
	}
	if len(snap.ToolPolicies) != 2 {
		t.Fatalf("loaded %d tool policies, want 2: %v", len(snap.ToolPolicies), snap.ToolPolicies)
	}
	for id, policy := range snap.ToolPolicies {
		if policy.SettingSources != nil {
			t.Errorf("node %q loaded SettingSources = %q, want nil — the old absence means layer 1 was OFF, not on",
				id, *policy.SettingSources)
		}
		if len(policy.AllowedTools) == 0 {
			t.Errorf("node %q lost allowed_tools on load: %v", id, policy)
		}
	}

	// And re-writing it — which every resumed leg does on its first settled
	// node — stamps the answer without changing the posture.
	path := filepath.Join(t.TempDir(), "state.json")
	if err := Write(path, snap); err != nil {
		t.Fatalf("rewrite the loaded snapshot: %v", err)
	}
	rewritten, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the rewritten snapshot: %v", err)
	}
	for id, policy := range rawPolicies(t, rewritten) {
		if string(policy[AllowedToolsIsACeilingKey]) != "false" {
			t.Errorf("node %q rewrote %s = %s, want false", id, AllowedToolsIsACeilingKey, policy[AllowedToolsIsACeilingKey])
		}
		if _, present := policy["setting_sources"]; present {
			t.Errorf("node %q was silently re-isolated by the rewrite: %v", id, policy)
		}
	}
}
