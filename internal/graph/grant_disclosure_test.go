package graph

import (
	"reflect"
	"testing"
)

// The disclosure gap #196 closed: a fragment may declare a substitution point
// INSIDE its own allowed_tools, and a citing node binds it. That is not an
// override — the citing node declares only wiring — so `Overridden` stays empty
// and, for a multi-node use, `Spliced` says only which ids exist. Before this,
// the fragment file showed a slot, the citing graph showed a value, and the run
// log showed neither: the one grant that needed two files to read was the one no
// run could announce.
//
// Every case below is a PAIR. The positive shows the assembled grant recorded;
// the negative shows a verbatim grant recorded as nothing — without which these
// tests would pass on an implementation that announced every grant of every
// spliced node, which is the disclosure nobody reads.

// parameterizedGrantFragment invites the grant in, the shape the issue quotes.
const parameterizedGrantFragment = `fragment: tools
description: a gate whose grant is half the citing graph's
substitutions: [extra]
node:
  prompt: "check the work"
  allowed_tools: [Read, "{{ with.extra }}"]
  success_check: { exit_zero: true }
`

// verbatimGrantFragment is the same shape with the grant written out — the
// negative control. It still substitutes (into the prompt), so a test that
// passes here cannot be passing merely because nothing was bound.
const verbatimGrantFragment = `fragment: tools
description: a gate whose grant is its own
substitutions: [extra]
node:
  prompt: "check the work and {{ with.extra }}"
  allowed_tools: [Read, "Bash(go *)"]
  success_check: { exit_zero: true }
`

// grantCitingGraph cites either of the two above from a single node.
func grantCitingGraph(using string) string {
	return "name: t\nnodes:\n  - { id: dev, prompt: build }\n" + using
}

// TestSingleNodeGrantAssembledByBindingIsDisclosed — the positive half of the
// single-node pair: the resolution carries the RESOLVED list, not the slot.
func TestSingleNodeGrantAssembledByBindingIsDisclosed(t *testing.T) {
	path := writeGraphDir(t,
		grantCitingGraph(`  - { id: x, use: tools, depends_on: [dev], with: { extra: "Bash(go *)" } }`+"\n"),
		map[string]string{"tools": parameterizedGrantFragment})

	res, err := LoadFile(path)
	if err != nil {
		t.Fatalf("a parameterized grant must still load: %v", err)
	}
	want := []ResolvedGrant{{NodeID: "x", Tools: []string{"Read", "Bash(go *)"}}}
	if got := res.Resolutions[0].Grants; !reflect.DeepEqual(got, want) {
		t.Errorf("Grants = %+v, want %+v", got, want)
	}
	// And it is a grant, not an override: the citing node declared only wiring,
	// which is exactly why Overridden could never have carried this.
	if overridden := res.Resolutions[0].Overridden; len(overridden) != 0 {
		t.Errorf("a wiring-only citation overrides nothing, got %v", overridden)
	}
}

// TestSingleNodeVerbatimGrantIsNotDisclosed is the control. A grant written out
// in the fragment file is readable in ONE file; the principle names the two-file
// case, and a line per spliced node regardless is a line nobody reads.
func TestSingleNodeVerbatimGrantIsNotDisclosed(t *testing.T) {
	path := writeGraphDir(t,
		grantCitingGraph(`  - { id: x, use: tools, depends_on: [dev], with: { extra: "run make local" } }`+"\n"),
		map[string]string{"tools": verbatimGrantFragment})

	res, err := LoadFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := res.Resolutions[0].Grants; got != nil {
		t.Errorf("a grant substitution never touched must announce nothing, got %+v", got)
	}
	// The binding really did substitute — into the prompt — so this test failed
	// for the right reason and not because resolution did nothing at all.
	if x, _ := res.Graph.NodeByID("x"); x.Prompt != "check the work and run make local" {
		t.Errorf("prompt = %q, want the substituted text", x.Prompt)
	}
}

// TestGrantBoundAsAWholeListIsDisclosed pins why the judgment is a before/after
// comparison rather than a scan of the fragment source for `{{`: here the token
// IS the whole scalar, so substitution replaces the field's type as well as its
// text, and the resolved grant is a list that never appeared in either file's
// allowed_tools as a list.
func TestGrantBoundAsAWholeListIsDisclosed(t *testing.T) {
	frag := `fragment: tools
description: a gate whose whole grant is bound
substitutions: [grant]
node:
  prompt: "check the work"
  allowed_tools: "{{ with.grant }}"
  success_check: { exit_zero: true }
`
	path := writeGraphDir(t,
		grantCitingGraph(`  - { id: x, use: tools, depends_on: [dev], with: { grant: [Read, "Bash(go *)"] } }`+"\n"),
		map[string]string{"tools": frag})

	res, err := LoadFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := []ResolvedGrant{{NodeID: "x", Tools: []string{"Read", "Bash(go *)"}}}
	if got := res.Resolutions[0].Grants; !reflect.DeepEqual(got, want) {
		t.Errorf("Grants = %+v, want %+v", got, want)
	}
}

// TestOverriddenGrantIsAnnouncedAsAnOverrideNotAsAGrant — the two clauses must
// not double up. When the citing node overrides allowed_tools, the substituted
// one is discarded by the overlay, so announcing it would name a grant no node
// runs with; the override list already says the key came from the citing file.
func TestOverriddenGrantIsAnnouncedAsAnOverrideNotAsAGrant(t *testing.T) {
	path := writeGraphDir(t,
		grantCitingGraph(`  - { id: x, use: tools, depends_on: [dev], allowed_tools: [Read], with: { extra: "Bash(go *)" } }`+"\n"),
		map[string]string{"tools": parameterizedGrantFragment})

	res, err := LoadFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := res.Resolutions[0].Grants; got != nil {
		t.Errorf("an overridden grant is the override clause's business, got %+v", got)
	}
	if want := []string{"allowed_tools"}; !reflect.DeepEqual(res.Resolutions[0].Overridden, want) {
		t.Errorf("Overridden = %v, want %v", res.Resolutions[0].Overridden, want)
	}
	if x, _ := res.Graph.NodeByID("x"); !reflect.DeepEqual(x.AllowedTools, []string{"Read"}) {
		t.Errorf("the citing node's grant must win, got %v", x.AllowedTools)
	}
}

// multiGrantFragment parameterizes ONE of its two nodes' grants — the pair held
// inside a single fragment, so the multi-node case cannot pass by announcing
// every spliced node.
const multiGrantFragment = `fragment: lanes
description: build then review, one parameterized grant
substitutions: [extra]
exit: review
nodes:
  - id: build
    prompt: "do the work"
    allowed_tools: [Read, "{{ with.extra }}"]
  - id: review
    depends_on: [build]
    prompt: "review {{ artifacts.build }}"
    allowed_tools: [Read]
`

// TestMultiNodeGrantDisclosureNamesOnlyTheAssembledOne is the multi-node pair in
// one case: `x/build`'s grant came from two files and is announced under its
// MINTED id, `x/review`'s is its own and is announced nowhere.
func TestMultiNodeGrantDisclosureNamesOnlyTheAssembledOne(t *testing.T) {
	path := writeGraphDir(t,
		grantCitingGraph(`  - { id: x, use: lanes, depends_on: [dev], with: { extra: "Bash(go *)" } }`+"\n"),
		map[string]string{"lanes": multiGrantFragment})

	res, err := LoadFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := []ResolvedGrant{{NodeID: "x/build", Tools: []string{"Read", "Bash(go *)"}}}
	if got := res.Resolutions[0].Grants; !reflect.DeepEqual(got, want) {
		t.Errorf("Grants = %+v, want %+v — only the assembled grant, under the spliced id", got, want)
	}
	// The ids clause is untouched: both spliced nodes are still named there.
	if want := []string{"x/build", "x/review"}; !reflect.DeepEqual(res.Resolutions[0].Spliced, want) {
		t.Errorf("Spliced = %v, want %v", res.Resolutions[0].Spliced, want)
	}
}

// TestGrantDisclosureIsSilentForAFragmentWithNoGrant — a fragment declaring no
// allowed_tools at all compares equal to itself, so an absent key can never be
// mistaken for an assembled one.
func TestGrantDisclosureIsSilentForAFragmentWithNoGrant(t *testing.T) {
	frag := `fragment: tools
description: a gate with no grant of its own
substitutions: [extra]
node:
  prompt: "check the work and {{ with.extra }}"
  success_check: { exit_zero: true }
`
	path := writeGraphDir(t,
		grantCitingGraph(`  - { id: x, use: tools, depends_on: [dev], with: { extra: "run make local" } }`+"\n"),
		map[string]string{"tools": frag})

	res, err := LoadFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := res.Resolutions[0].Grants; got != nil {
		t.Errorf("no grant to assemble, got %+v", got)
	}
}
