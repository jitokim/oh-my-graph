// Fragment resolution — ADR 0013, generalized by ADR 0027. A fragment is a
// definition file under the entry graph's own fragments/ sibling directory,
// spliced into a using node's `use:` at LOAD time: substitute the declared
// `{{ with.<name> }}` points, overlay the using node's own keys, and hand the
// resolved document to the exact same decode → Validate pipeline every graph
// already goes through.
//
// A fragment declares EITHER `node:` — one node's behavior, the ADR 0013 form,
// unchanged in every respect — or `nodes:` plus `exit:`, a whole subgraph: the
// loop people were already writing out longhand. The invariant is one sentence
// covering both: a fragment may never name an id it does not itself declare.
// A single-node fragment declares no ids, so `depends_on`/`feedback` stay load
// errors for it; a multi-node fragment declares its own, so edges among THOSE
// are legal and nothing else is. The spliced ids are namespaced
// `<using-id>/<internal-id>`, which no author and no planner may write, so a
// spliced node can never collide with one that was.
// Everything here operates on the raw *yaml.Node document, before any decode,
// so "explicitly overridden" is judged by KEY PRESENCE in the raw mapping —
// `budget_usd: 0` written in the using node is an override, an absent key is
// not — and the resolved document is decoded from the spliced tree directly,
// never re-marshaled to bytes (a serialize/reparse round-trip is a place for
// anchors, tags and styles to shift, and it buys nothing).
//
// Resolution is a pure function of the ENTRY FILE's path: `use: <name>` in
// /repo/graphs/foo.yaml resolves to /repo/graphs/fragments/<name>.yaml and
// nowhere else — one location, no search path, no cwd dependence. The engine
// downstream of the loader has no fragment concept at all.
package graph

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// FragmentError is a load error in fragment resolution: the using node named
// a fragment that cannot be resolved as declared, or the fragment file itself
// is broken. Every one of these fail-fasts LoadFile and is collected by
// LintFile, the same Validate-is-Issues()[0] contract the structural checks
// live under.
type FragmentError struct {
	NodeID   string // the using node's id; "" when the node declares none
	Fragment string // the use: name; "" when the error is not tied to one
	Source   string // the fragment file's path; "" before lookup resolved one
	Reason   string
}

func (e *FragmentError) Error() string {
	msg := "invalid graph"
	if e.NodeID != "" {
		msg += fmt.Sprintf(": node %q", e.NodeID)
	}
	if e.Fragment != "" {
		msg += fmt.Sprintf(": fragment %q", e.Fragment)
	}
	return msg + ": " + e.Reason
}

// FragmentResolution records one resolved `use:` for the run-time disclosure
// line: which node spliced which fragment file, and every top-level key the
// using node overrode — so a hollowed-out success_check or a widened
// allowed_tools is announced at every run, not only visible to whoever reads
// the file.
type FragmentResolution struct {
	NodeID      string
	Fragment    string
	Description string   // the fragment file's description:, printed with the disclosure
	Source      string   // the fragment file's path
	Overridden  []string // top-level keys declared by BOTH files, using node's value winning; fragment-file key order
	// Spliced is the ids a MULTI-NODE resolution minted, in fragment order —
	// empty for the single-node form, whose one spliced id is NodeID itself.
	// A multi-node use overrides nothing (the using node may declare only
	// wiring), so this is what its disclosure line has to say instead: the
	// reader of a run log learns that one `use:` became five nodes, and which
	// five, without opening the fragment file.
	Spliced []string
}

// FragmentAdvisory is an advisory finding about a fragment file — drift smell,
// not an error: harmless at run time, worth a warning line from lint. Advice
// only; it never affects whether a graph is valid or what any command exits
// with (the same standing as handoff's lint warnings).
type FragmentAdvisory struct {
	Fragment string
	Source   string
	Detail   string
}

func (a FragmentAdvisory) String() string {
	return fmt.Sprintf("fragment %q (%s): %s", a.Fragment, a.Source, a.Detail)
}

// LoadResult is what LoadFile hands the CLI: the validated graph, the entry
// file's raw bytes (the datum `run` needs for GraphSHA256 — the hash is of
// the ENTRY file only, the snapshot is the authority on resolved content),
// one FragmentResolution per resolved `use:` — which is both the disclosure
// line's input and the snapshot's re-encode signal (whenever any node
// resolved a fragment, the snapshot must store the re-encoded resolved graph,
// never the raw entry bytes, or a JSON-authored fragment graph would snapshot
// unresolved and fail its own resume) — and the advisory findings about the
// fragment FILES this load spliced.
//
// Advisories travel with a successful load, not only with LintFile, because
// they belong to the same disclosure the run already prints: `run` announces
// which fragments it spliced, so it must be able to announce their drift
// smell too, or the identical file warns under `lint` and stays silent under
// the command that spends money. They remain advice — nothing here affects
// whether the load succeeded.
type LoadResult struct {
	Graph       *Graph
	Source      []byte
	Resolutions []FragmentResolution
	Advisories  []FragmentAdvisory
}

// LoadFile is the path-aware load stage (ADR 0013) that `run` is wired onto:
// read the entry file, resolve every `use:` against the file's own fragments/
// sibling, decode the spliced document, and return it only if it passes
// Validate — fail-fast, first problem wins, mirroring Parse. A fragment-free
// file loads byte-for-byte identically to Parse(os.ReadFile(path)).
func LoadFile(path string) (*LoadResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read graph file %q: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse graph YAML: %w", err)
	}
	outcome := resolveFragments(&doc, path)
	if len(outcome.errs) > 0 {
		return nil, outcome.errs[0]
	}
	g, err := decodeResolved(&doc)
	if err != nil {
		return nil, err
	}
	if err := g.Validate(); err != nil {
		return nil, err
	}
	return &LoadResult{Graph: g, Source: data, Resolutions: outcome.resolutions, Advisories: outcome.advisories}, nil
}

// LintFile is LoadFile's collect-all counterpart, standing to it exactly as
// Graph.Issues stands to Graph.Validate one layer down: every
// fragment issue plus every structural issue of the resolved graph, first
// element identical to what LoadFile would have failed with — so `lint` and
// `run --dry-run` render a whole list and the two views can never disagree
// about which graph files are valid. Advisory findings travel separately from
// issues, because advice must never affect an exit code. The error return is
// I/O only (the file could not be read); an unreadable file has no list to
// collect.
//
// Callers that also need the graph itself must use LintLoadFile rather than
// following this with LoadFile — see the read-once note there.
func LintFile(path string) (issues []error, advisories []FragmentAdvisory, err error) {
	issues, advisories, _, err = LintLoadFile(path)
	return issues, advisories, err
}

// LintLoadFile is LintFile and LoadFile in one pass, from ONE read of path:
// the collected issue list, the fragment advisories, and — when that list is
// empty — the same *LoadResult LoadFile would have returned for the same
// bytes. `lint` and `run --dry-run` both need the whole list AND the resolved
// graph (they sweep it for advisories), and reading the path twice to get
// both is not equivalent to reading it once: on a path that can only be read
// once — a FIFO, a process substitution like `lint <(...)`, /dev/stdin — the
// second read comes back EMPTY, which decodes to an empty graph that passes
// every check. The command then printed `valid` with the advisory sweep run
// over the wrong (empty) graph, so the advice was silently dropped while the
// exit code and the issue list, both computed from the first read, stayed
// correct. Load once, sweep the graph that was linted.
//
// loaded is nil exactly when issues is non-empty: an invalid graph has no
// LoadResult, which is what LoadFile says by failing.
func LintLoadFile(path string) (issues []error, advisories []FragmentAdvisory, loaded *LoadResult, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read graph file %q: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		// A YAML syntax error is the whole report on its own: nothing decoded,
		// so there is no graph for Graph.Issues to have an opinion about.
		return []error{fmt.Errorf("parse graph YAML: %w", err)}, nil, nil, nil
	}
	outcome := resolveFragments(&doc, path)
	issues = outcome.errs
	g, err := decodeResolved(&doc)
	if err != nil {
		return append(issues, err), outcome.advisories, nil, nil
	}
	issues = append(issues, g.Issues()...)
	if len(issues) > 0 {
		return issues, outcome.advisories, nil, nil
	}
	return nil, outcome.advisories, &LoadResult{
		Graph:       g,
		Source:      data,
		Resolutions: outcome.resolutions,
		Advisories:  outcome.advisories,
	}, nil
}

// decodeResolved decodes a (possibly spliced) YAML document into a normalized
// Graph — decode's back half applied to a *yaml.Node instead of bytes, per
// the no-round-trip rule in the package comment above.
func decodeResolved(doc *yaml.Node) (*Graph, error) {
	var raw rawGraph
	if doc.Kind != 0 {
		if err := doc.Decode(&raw); err != nil {
			return nil, fmt.Errorf("parse graph YAML: %w", err)
		}
	}
	return fromRaw(raw), nil
}

// withTokenPattern is the substitution-token grammar: exactly
// handoff's placeholderPattern — same whitespace rules, same body shape —
// with `with` as the leading word and no filter (a substitution point is
// bound, not filtered). The runtime's pattern must never learn `with`; this
// one exists so the token is gone before the runtime looks.
var withTokenPattern = regexp.MustCompile(`\{\{\s*with\.([A-Za-z0-9._-]+)\s*\}\}`)

// looseTokenPattern finds every {{ ... }} token in a fragment body, well-formed
// or not. withTokenPattern alone can only see tokens that ALREADY obey the
// grammar, so on its own it never notices `{{ with.checks | inline }}` or
// `{{ with. }}` or `{{ With.checks }}`: each one claims the with namespace,
// none of them substitutes, and all three survive resolution into the spliced
// prompt and reach the model verbatim — the exact silent-verbatim failure the
// load-time/run-time token split exists to abolish. Scanning loosely and then
// judging is the same shape handoff's placeholder lint uses; it is duplicated
// here rather than shared because handoff imports graph, not the reverse.
var looseTokenPattern = regexp.MustCompile(`\{\{[^{}]*\}\}`)

// tokenLeadingWord extracts the first identifier of a {{ ... }} token's body —
// the word that decides whether the token claims the with namespace at all.
var tokenLeadingWord = regexp.MustCompile(`^[A-Za-z0-9_]+`)

// idTokenBody is the body of a token that names a NODE: one of the two id
// namespaces, a dot, and the id. Applied to a token body already split at its
// filter, so the id runs to the end.
var idTokenBody = regexp.MustCompile(`^(artifacts|feedback)\.([^\s|]+)$`)

// idToken is one {{ artifacts.<id> }} / {{ feedback.<id> }} occurrence found in
// a scalar, kept as its exact source text plus its parts, so a rewrite can put
// back what it did not change.
type idToken struct {
	token     string // the token exactly as written, for a literal replace
	kind      string // "artifacts" or "feedback"
	ref       string // the id it names
	filter    string // the text after the '|', trimmed; "" when there is none
	hasFilter bool
}

// idTokensIn finds every well-formed node-naming token in one scalar. The scan
// is loose and then judged, the same shape withTokenName uses: a token that
// claims a namespace but breaks the grammar is NOT returned here, because a
// namespace rewrite must never repair a token — a malformed one is the lint
// sweeps' finding and the runtime's verbatim passthrough, and silently fixing
// it here would hide an authoring bug behind a splice.
func idTokensIn(value string) []idToken {
	var found []idToken
	for _, token := range looseTokenPattern.FindAllString(value, -1) {
		body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(token, "{{"), "}}"))
		head, filter, hasFilter := strings.Cut(body, "|")
		groups := idTokenBody.FindStringSubmatch(strings.TrimSpace(head))
		if groups == nil {
			continue
		}
		found = append(found, idToken{
			token: token, kind: groups[1], ref: groups[2],
			filter: strings.TrimSpace(filter), hasFilter: hasFilter,
		})
	}
	return found
}

// renderedAs is this token respelled at a new id, filter and all. The
// whitespace is normalized to the canonical `{{ kind.ref | filter }}`, which is
// what every graph in the repo writes anyway.
func (t idToken) renderedAs(ref string) string {
	if !t.hasFilter {
		return fmt.Sprintf("{{ %s.%s }}", t.kind, ref)
	}
	return fmt.Sprintf("{{ %s.%s | %s }}", t.kind, ref, t.filter)
}

// rewriteIDTokens respells every node-naming token in a subtree's scalars for
// which rename returns a new id. Tokens rename declines are left byte-identical
// — this is a targeted rewrite of ids, never a reformat of prompts.
func rewriteIDTokens(node *yaml.Node, rename func(kind, ref string) (string, bool)) {
	walkScalarNodes(node, func(scalar *yaml.Node) {
		for _, token := range idTokensIn(scalar.Value) {
			to, ok := rename(token.kind, token.ref)
			if !ok {
				continue
			}
			scalar.Value = strings.ReplaceAll(scalar.Value, token.token, token.renderedAs(to))
		}
	})
}

// namespaceSeparator joins a using node's id to a fragment-internal one. It is
// the one separator no author and no planner can write (the file loader and
// the coordinator each refuse it, and nodeIDSegment excludes it), which is what
// makes a spliced id incapable of colliding with an authored one.
const namespaceSeparator = "/"

// splicedID is the id a fragment-internal node takes in the using graph.
func splicedID(usingID, internalID string) string {
	return usingID + namespaceSeparator + internalID
}

// fragmentNamePattern is the grammar of a `use:` value: a BARE name, never a
// path. The lookup rule ADR 0013 spends a section defending is "one location,
// no search path" — but filepath.Join cleans lexically, so an unconstrained
// name reaches straight out of the fragments/ sibling it is documented to
// resolve inside (`use: ../../evil` → <repo>/evil.yaml, `use: a/b` → a nested
// search path). The refused file supplies a real prompt, allowed_tools and
// success_check.verify.command, so "which files can a graph pull behavior
// from" is exactly the boundary a reviewer reads fragments/ to check. A
// leading alphanumeric rules out `..` and dotfiles; the class rules out every
// separator, on either platform.
var fragmentNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// fragmentWiringFields are the node fields a fragment file must not declare
// freely, partitioned by ADR 0027 rather than shrunk. The invariant is one
// sentence — a fragment may never name an id it does not itself declare — and
// these two halves are what it comes to in practice:
//
//   - fragmentLocationFields are refused in BOTH forms. A worktree name is
//     lane choreography and a cwd is invocation-specific: neither is wiring
//     among declared ids, so declaring one is not made legal by declaring ids.
//     They stay on the using node, which is also the node that PROPAGATES them
//     to every spliced node.
//   - fragmentIDBearingFields are refused exactly when the fragment declares no
//     ids to justify them. A single-node fragment declares none, so all three
//     stay the load errors ADR 0013 made them; a multi-node fragment declares
//     its own, so an id, an internal depends_on and an internal feedback arc
//     are its own business — and naming anything else is the invariant's error.
var (
	fragmentLocationFields  = []string{"cwd", "worktree"}
	fragmentIDBearingFields = []string{"id", "depends_on", "feedback"}
)

// multiNodeUsingKeys are the keys a node citing a MULTI-NODE fragment may
// declare: the wiring, and nothing else. ADR 0013's merge rules are per-key
// over ONE node, and there is no coherent way to overlay a using node's
// success_check onto five spliced ones — so rather than pick an arbitrary
// reading, a behavior key on a multi-node `use:` is a load error naming the
// key. A loop that needs a different gate needs a substitution point or a
// different fragment.
var multiNodeUsingKeys = map[string]bool{
	"id": true, "use": true, "with": true, "depends_on": true, "cwd": true, "worktree": true,
}

// fragmentFile is one parsed, structurally-checked fragment definition, in
// either of its two forms.
type fragmentFile struct {
	name          string
	description   string
	source        string
	substitutions []string
	// referenced is the set of substitution points the body actually uses —
	// a fragment-level fact (the body is the same for every user), computed
	// once at load so the drift advisory fires once per fragment, not once
	// per use.
	referenced map[string]bool
	// node is the single-node form's node: mapping — never mutated; users
	// splice a deep copy. nil exactly when this is a multi-node fragment.
	node *yaml.Node
	// nodes are the multi-node form's declarations in file order, ids the id
	// each one declares (same order), declares that same set as a lookup, and
	// exit the id a downstream `depends_on: [<using-id>]` resolves to. All nil
	// or empty for the single-node form.
	nodes    []*yaml.Node
	ids      []string
	declares map[string]bool
	exit     string
}

// isMulti reports which form this fragment took. The two are exclusive at load
// (a file declaring both `node:` and `nodes:` is refused), so one test decides.
func (f *fragmentFile) isMulti() bool { return len(f.nodes) > 0 }

// loadedFragment is a cache slot: a fragment file is read and judged once per
// resolution pass, so a fragment used by several nodes reports its own
// defects once (each using node still fails to resolve, but the file's
// errors are not repeated per user).
type loadedFragment struct {
	frag       *fragmentFile
	errs       []*FragmentError
	advisories []FragmentAdvisory
}

// fragmentOutcome is one resolution pass's full result.
type fragmentOutcome struct {
	resolutions []FragmentResolution
	advisories  []FragmentAdvisory
	errs        []error
	// loops maps each multi-node using id to the internal id its fragment
	// declared as `exit:`. It is what makes a loop addressable from outside as
	// one thing: a downstream `depends_on: [qa-a]` and a downstream
	// {{ artifacts.qa-a }} both resolve to `qa-a/<exit>` in the second pass.
	loops map[string]string
	// bound records every {{ artifacts.<id> }} a using node BOUND into a
	// fragment, so the second pass can prove each names a node that exists.
	// Bound text is never namespace-rewritten (it belongs to the using graph,
	// not to the fragment), so a using author who binds a fragment's internal
	// id gets a token that names nothing — which would otherwise survive load
	// and fail after spend.
	bound []boundReference
}

// boundReference is one artifact id a using node bound into a fragment.
type boundReference struct {
	nodeID string
	key    string
	ref    string
}

// resolveFragments walks the entry document's nodes and splices every `use:`
// in place, collecting every error rather than stopping at the first — the
// collect-all form LintFile needs; LoadFile fail-fasts by taking errs[0],
// which is the first error in document order, so the two views agree on
// which problem comes first. A node that fails to resolve has its use:/with:
// keys stripped so the structural pass that follows reports each defect once
// (the Validate backstop exists for documents that never came through here,
// not to echo these errors).
func resolveFragments(doc *yaml.Node, entryPath string) fragmentOutcome {
	out := fragmentOutcome{loops: make(map[string]string)}
	cache := make(map[string]*loadedFragment)
	nodes := findNodesSequence(doc)
	if nodes == nil {
		return out
	}
	// Before anything is spliced: no id a HUMAN wrote may carry the namespace
	// separator. Judged on the authored document, because that is the only
	// moment the two are distinguishable — after the splice, a '/' id is
	// exactly what a correct resolution produces.
	refuseAuthoredNamespaces(nodes, &out)

	spliced := make([]*yaml.Node, 0, len(nodes.Content))
	for _, nodeMap := range nodes.Content {
		if nodeMap.Kind != yaml.MappingNode {
			spliced = append(spliced, nodeMap) // decode will report the malformed node itself
			continue
		}
		if loop := resolveNode(nodeMap, entryPath, cache, &out); loop != nil {
			spliced = append(spliced, loop...)
			continue
		}
		spliced = append(spliced, nodeMap)
	}
	nodes.Content = spliced

	// The second pass is over the RESOLVED sequence, so it sees the host
	// graph's own references and the ones a binding carried into a fragment
	// alike — a loop cited by another loop's entry node included.
	resolveLoopReferences(nodes, &out)
	checkBoundReferences(nodes, &out)
	return out
}

// refuseAuthoredNamespaces is the loader's half of ADR 0027's encapsulation
// guarantee: `<using-id>/<internal-id>` is minted by the splicer alone, so a
// '/' in any id a FILE spells is a load error — an entry graph's `nodes:`
// (checked here) or a fragment file's own `nodes:` (checked by
// judgeMultiNodeIDs). The coordinator refuses the same shape in a planner
// reply, and between the two, reaching into a loop's internals is refused
// wherever it can be typed.
//
// It covers `depends_on` and `feedback.rerun` as well as `id`, because those
// are where reaching IN is actually spelled: `depends_on: [qa-a/impl]` names a
// node that really exists after a splice, so nothing downstream would object.
func refuseAuthoredNamespaces(nodes *yaml.Node, out *fragmentOutcome) {
	refuse := func(id, where, spelling string) {
		out.errs = append(out.errs, &GraphValidationError{
			NodeID: id,
			Reason: fmt.Sprintf("%s carries a '/' (%q) — that namespace is minted only by a multi-node fragment splice (ADR 0027), never written: name the loop's using id to depend on the loop, and nothing to reach inside it", where, spelling),
		})
	}
	for _, nodeMap := range nodes.Content {
		if nodeMap.Kind != yaml.MappingNode {
			continue
		}
		keys := mappingValues(nodeMap)
		id := strings.TrimSpace(scalarValue(keys["id"]))
		if strings.Contains(id, namespaceSeparator) {
			refuse(id, "node id", id)
		}
		if dependsOn := keys["depends_on"]; dependsOn != nil && dependsOn.Kind == yaml.SequenceNode {
			for _, parent := range dependsOn.Content {
				if value := strings.TrimSpace(scalarValue(parent)); strings.Contains(value, namespaceSeparator) {
					refuse(id, "depends_on", value)
				}
			}
		}
		if feedback := keys["feedback"]; feedback != nil && feedback.Kind == yaml.MappingNode {
			if rerun := strings.TrimSpace(scalarValue(mappingValues(feedback)["rerun"])); strings.Contains(rerun, namespaceSeparator) {
				refuse(id, "feedback.rerun", rerun)
			}
		}
	}
}

// resolveLoopReferences makes a spliced loop addressable from outside as ONE
// thing, and refuses the one spelling that cannot mean what it says:
//
//   - `depends_on: [qa-a]` resolves to `qa-a/<exit>`. From outside, the loop's
//     value is its exit's.
//   - `{{ artifacts.qa-a }}` resolves to `{{ artifacts.qa-a/<exit> }}`,
//     symmetrically, filter and all. Without the symmetry the token would name
//     a node that no longer exists, and `run` does not run the lint sweeps —
//     so the graph would load, the upstream nodes would be paid for, and the
//     citing node would die on an InterpolationError.
//   - `feedback: { rerun: qa-a }` is a load error. The symmetry is a trap
//     here: rewritten to the exit, an author who asked to re-run their loop
//     would silently get one node re-run instead. `depends_on: [qa-a]` means
//     "after the loop" and the exit expresses that exactly; `rerun: qa-a`
//     means "again, from the top" and the exit expresses the opposite.
//
// {{ feedback.qa-a }} gets no rewrite and needs none: a feedback token is legal
// only inside the body of the arc that declares it, so from outside the loop it
// was already a load error and stays one.
func resolveLoopReferences(nodes *yaml.Node, out *fragmentOutcome) {
	if len(out.loops) == 0 {
		return
	}
	for _, nodeMap := range nodes.Content {
		if nodeMap.Kind != yaml.MappingNode {
			continue
		}
		keys := mappingValues(nodeMap)
		if dependsOn := keys["depends_on"]; dependsOn != nil && dependsOn.Kind == yaml.SequenceNode {
			for _, parent := range dependsOn.Content {
				if exit, isLoop := out.loops[strings.TrimSpace(scalarValue(parent))]; isLoop {
					parent.Value = splicedID(parent.Value, exit)
				}
			}
		}
		if feedback := keys["feedback"]; feedback != nil && feedback.Kind == yaml.MappingNode {
			rerun := strings.TrimSpace(scalarValue(mappingValues(feedback)["rerun"]))
			if _, isLoop := out.loops[rerun]; isLoop {
				out.errs = append(out.errs, &GraphValidationError{
					NodeID: strings.TrimSpace(scalarValue(keys["id"])),
					Reason: fmt.Sprintf("feedback.rerun names %q, which cites a multi-node fragment — a loop is not a rerun target: a feedback arc re-runs ONE ancestor node and the body up to this one, so aiming it at a loop would silently re-run only that loop's exit. Name the node inside it you mean — which this graph may not spell — or declare the arc in the fragment itself", rerun),
				})
			}
		}
		rewriteIDTokens(nodeMap, func(kind, ref string) (string, bool) {
			exit, isLoop := out.loops[ref]
			if kind != "artifacts" || !isLoop {
				return "", false
			}
			return splicedID(ref, exit), true
		})
	}
}

// checkBoundReferences proves every artifact id a using node bound into a
// fragment names a node the resolved graph actually has. It is the residue of
// the rewrite ORDER: the namespace rewrite applies to the fragment file's own
// text and applies BEFORE substitution, so a value bound at the using site is
// inserted afterwards and is never rewritten. A using author who writes
// `with: { evidence: "{{ artifacts.impl | inline }}" }` meaning the fragment's
// internal `impl` therefore gets a token naming nothing in their own graph —
// which would survive load and fail after spend, for the same reason the
// {{ artifacts.<loop> }} rewrite exists.
//
// A bound token whose id DOES exist keeps today's semantics exactly, advisory
// ancestry lint included.
func checkBoundReferences(nodes *yaml.Node, out *fragmentOutcome) {
	if len(out.bound) == 0 {
		return
	}
	exists := make(map[string]bool, len(nodes.Content))
	for _, nodeMap := range nodes.Content {
		if nodeMap.Kind == yaml.MappingNode {
			exists[strings.TrimSpace(scalarValue(mappingValues(nodeMap)["id"]))] = true
		}
	}
	for _, ref := range out.bound {
		id := ref.ref
		if exit, isLoop := out.loops[id]; isLoop {
			id = splicedID(id, exit)
		}
		if exists[id] {
			continue
		}
		out.errs = append(out.errs, &GraphValidationError{
			NodeID: ref.nodeID,
			Reason: fmt.Sprintf("with: binds %q to a value referencing {{ artifacts.%s }}, which is not a node in this graph — a bound value belongs to the CITING graph and is never rewritten into the fragment's namespace, so a fragment's own internal id cannot be named here", ref.key, ref.ref),
		})
	}
}

// resolveNode resolves one node mapping. A single-node `use:` is spliced IN
// PLACE and returns nil, exactly as it did before ADR 0027; a multi-node one
// returns the node mappings that REPLACE this one in the graph's sequence.
// A plain node is a no-op (except the dead-`with:` refusal), and a node that
// fails to resolve has its fragment keys stripped so the structural pass that
// follows reports each defect once.
func resolveNode(nodeMap *yaml.Node, entryPath string, cache map[string]*loadedFragment, out *fragmentOutcome) []*yaml.Node {
	keys := mappingValues(nodeMap)
	id := scalarValue(keys["id"])
	useNode, withNode := keys["use"], keys["with"]

	if useNode == nil {
		if withNode != nil {
			out.errs = append(out.errs, &FragmentError{NodeID: id,
				Reason: "with: without use: — a binding with no fragment to bind is a dead key, which is a wiring bug, not a style choice"})
			removeKeys(nodeMap, "with")
		}
		return nil
	}

	// fail records the node's resolution errors and strips the fragment keys
	// so the structural pass cannot cascade a second report onto them.
	fail := func(errs ...*FragmentError) {
		for _, e := range errs {
			out.errs = append(out.errs, e)
		}
		removeKeys(nodeMap, "use", "with")
	}

	name := strings.TrimSpace(scalarValue(useNode))
	if useNode.Kind != yaml.ScalarNode || name == "" {
		fail(&FragmentError{NodeID: id, Reason: "use: must be a single non-empty fragment name"})
		return nil
	}
	if !fragmentNamePattern.MatchString(name) {
		fail(&FragmentError{NodeID: id, Fragment: name,
			Reason: "use: must be a bare fragment name (letters, digits, then any of . _ -), not a path — a use: resolves against the graph file's own fragments/ sibling and nowhere else, so a separator, a leading dot or a .. has no location to mean"})
		return nil
	}
	if keys["prompt"] != nil {
		fail(&FragmentError{NodeID: id, Fragment: name,
			Reason: "prompt: alongside use: — a wholesale prompt override recreates the copy-variation drift fragments exist to kill, while still claiming the fragment's name; customize through the fragment's declared substitution points, or write an inline node honestly"})
		return nil
	}

	lf := loadFragmentCached(name, entryPath, cache, out)
	if lf.frag == nil {
		// The file's own errors were reported once, on first load; this
		// node still cannot resolve, so its keys are stripped either way.
		fail(chargeTo(id, lf.errs)...)
		return nil
	}

	bindings, bindErrs := bindingsFor(withNode, lf.frag, id)
	if len(bindErrs) > 0 {
		fail(bindErrs...)
		return nil
	}
	recordBoundReferences(id, bindings, out)

	if lf.frag.isMulti() {
		loop, errs := spliceLoop(nodeMap, id, lf.frag, bindings)
		if len(errs) > 0 {
			fail(errs...)
			return nil
		}
		out.loops[id] = lf.frag.exit
		out.resolutions = append(out.resolutions, FragmentResolution{
			NodeID: id, Fragment: name, Description: lf.frag.description,
			Source: lf.frag.source, Spliced: splicedIDs(id, lf.frag),
		})
		return loop
	}

	body := deepCopyNode(lf.frag.node)
	if errs := substituteWithTokens(body, bindings, id, name); len(errs) > 0 {
		fail(errs...)
		return nil
	}

	overridden := overlayUsingNode(nodeMap, body)
	out.resolutions = append(out.resolutions, FragmentResolution{
		NodeID: id, Fragment: name, Description: lf.frag.description,
		Source: lf.frag.source, Overridden: overridden,
	})
	return nil
}

// splicedIDs is the ids one multi-node resolution mints, in fragment order —
// the disclosure line's answer to "this use: became how many nodes, and which".
func splicedIDs(usingID string, frag *fragmentFile) []string {
	ids := make([]string, 0, len(frag.ids))
	for _, internal := range frag.ids {
		ids = append(ids, splicedID(usingID, internal))
	}
	return ids
}

// recordBoundReferences notes every artifact id this using node bound, for the
// existence check the resolved graph can answer and this moment cannot.
func recordBoundReferences(nodeID string, bindings map[string]*yaml.Node, out *fragmentOutcome) {
	for key, value := range bindings {
		walkScalars(value, func(text string) {
			for _, token := range idTokensIn(text) {
				if token.kind == "artifacts" {
					out.bound = append(out.bound, boundReference{nodeID: nodeID, key: key, ref: token.ref})
				}
			}
		})
	}
}

// spliceLoop turns one multi-node `use:` into the nodes it stands for.
//
// The ORDER of the two rewrites is fixed and load-bearing: the namespace
// rewrite applies to the tokens written in the FRAGMENT FILE's own body, and it
// applies BEFORE substitution, so a value bound at the using site is inserted
// afterwards and is never rewritten. Substitute-then-rewrite would let a bound
// token be silently re-pointed whenever the using graph's id happened to match
// one the fragment declares — `self-dev` binds `{{ artifacts.e2e | inline }}`
// into a fragment today, and `e2e` is not a far-fetched internal name for a QA
// loop. That is the worst available outcome: a working reference quietly aimed
// at someone else's node. Rewrite-then-substitute keeps the two namespaces
// apart, which is what an author of either file expects to be reading.
func spliceLoop(using *yaml.Node, usingID string, frag *fragmentFile, bindings map[string]*yaml.Node) ([]*yaml.Node, []*FragmentError) {
	var errs []*FragmentError
	bad := func(reason string) {
		errs = append(errs, &FragmentError{NodeID: usingID, Fragment: frag.name, Source: frag.source, Reason: reason})
	}

	if strings.TrimSpace(usingID) == "" {
		bad("a node citing a multi-node fragment must declare an id — every spliced node's id is <this id>/<the fragment's own>, so without one there is no namespace to mint them in")
		return nil, errs
	}
	// Deterministic in document order, so a node declaring several behavior
	// keys reports them the way its author reads them.
	for i := 0; i+1 < len(using.Content); i += 2 {
		if key := using.Content[i].Value; !multiNodeUsingKeys[key] {
			bad(fmt.Sprintf("declares %q alongside a multi-node use: — this fragment splices %d nodes, and there is no coherent way to overlay one node's key onto all of them. A multi-node use: declares wiring only (id, depends_on, cwd, worktree); a loop that needs different behavior needs a substitution point or a different fragment", key, len(frag.nodes)))
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}

	keys := mappingValues(using)
	dependsOn, cwd, worktree := keys["depends_on"], keys["cwd"], keys["worktree"]

	spliced := make([]*yaml.Node, 0, len(frag.nodes))
	for _, internal := range frag.nodes {
		body := deepCopyNode(internal)
		namespaceNode(body, usingID, frag)
		if subErrs := substituteWithTokens(body, bindings, usingID, frag.name); len(subErrs) > 0 {
			errs = append(errs, subErrs...)
			continue
		}
		// An ENTRY node — one with no internal parent — inherits the using
		// node's depends_on verbatim. A fragment may have several; all of them
		// inherit it, which is what makes the loop start where the using node
		// says it starts.
		if dependsOn != nil && mappingValues(internal)["depends_on"] == nil {
			setKey(body, "depends_on", deepCopyNode(dependsOn))
		}
		// cwd/worktree stay on the using node and PROPAGATE to every spliced
		// node — exactly what backlog-batch writes by hand today, once per
		// lane node. cwd propagates as a template string and interpolates per
		// node at run time, as always.
		if cwd != nil {
			setKey(body, "cwd", deepCopyNode(cwd))
		}
		if worktree != nil {
			setKey(body, "worktree", deepCopyNode(worktree))
		}
		spliced = append(spliced, body)
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return spliced, nil
}

// namespaceNode rewrites one fragment-internal node into the using node's
// namespace: its own id, the ids it depends on, the ancestor its feedback arc
// re-runs, and every artifact/feedback token in its text. Every id it can name
// is one the fragment declares (loadFragmentFile refused the rest), so this is
// a total rename, not a best-effort one.
func namespaceNode(body *yaml.Node, usingID string, frag *fragmentFile) {
	rewriteIDTokens(body, func(_, ref string) (string, bool) {
		if !frag.declares[ref] {
			return "", false
		}
		return splicedID(usingID, ref), true
	})

	keys := mappingValues(body)
	if id := keys["id"]; id != nil {
		id.Value = splicedID(usingID, strings.TrimSpace(id.Value))
	}
	if dependsOn := keys["depends_on"]; dependsOn != nil && dependsOn.Kind == yaml.SequenceNode {
		for _, parent := range dependsOn.Content {
			if frag.declares[strings.TrimSpace(parent.Value)] {
				parent.Value = splicedID(usingID, strings.TrimSpace(parent.Value))
			}
		}
	}
	if feedback := keys["feedback"]; feedback != nil && feedback.Kind == yaml.MappingNode {
		if rerun := mappingValues(feedback)["rerun"]; rerun != nil && frag.declares[strings.TrimSpace(rerun.Value)] {
			rerun.Value = splicedID(usingID, strings.TrimSpace(rerun.Value))
		}
	}
}

// setKey writes value at key in a mapping, replacing an existing entry in place
// (so key order is stable) or appending a new one.
func setKey(mapping *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

// chargeTo returns the fragment file's cached errors re-attributed to the
// FIRST using node when they were reported without one, and an empty slice
// for later users (the errors were already reported; only the strip is
// needed). Cached errors already carry a NodeID when the first user set one.
func chargeTo(nodeID string, errs []*FragmentError) []*FragmentError {
	charged := make([]*FragmentError, 0, len(errs))
	for _, e := range errs {
		if e.NodeID == "" {
			e.NodeID = nodeID
			charged = append(charged, e)
		}
	}
	return charged
}

// loadFragmentCached parses and judges a fragment file once per resolution
// pass, emitting its file-level errors and advisories exactly once.
func loadFragmentCached(name, entryPath string, cache map[string]*loadedFragment, out *fragmentOutcome) *loadedFragment {
	if lf, ok := cache[name]; ok {
		return lf
	}
	lf := loadFragmentFile(name, filepath.Join(filepath.Dir(entryPath), "fragments", name+".yaml"))
	cache[name] = lf
	out.advisories = append(out.advisories, lf.advisories...)
	return lf
}

// loadFragmentFile reads and structurally judges one fragment file. On any
// error frag is nil and errs says why (without a NodeID — the caller charges
// the first using node); on success the substitution declarations, the body's
// referenced set and the drift advisories are already computed, because they
// are facts about the FILE, identical for every user.
func loadFragmentFile(name, source string) *loadedFragment {
	fileErr := func(reason string) *loadedFragment {
		return &loadedFragment{errs: []*FragmentError{{Fragment: name, Source: source, Reason: reason}}}
	}

	data, err := os.ReadFile(source)
	if os.IsNotExist(err) {
		return fileErr(fmt.Sprintf("no fragment file at the fragment location %q — a use: resolves against the graph file's own fragments/ sibling, and nowhere else, so a graph stored where no fragments/ directory sits beside it can cite nothing: put the fragment there, or author this graph next to a fragments/ that already has it (`oh-my-graph init <dir>` unpacks the shipped shapes into <dir>/graphs/fragments/, for graphs written in <dir>/graphs/)", source))
	}
	if err != nil {
		return fileErr(fmt.Sprintf("read fragment file %q: %v", source, err))
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fileErr(fmt.Sprintf("fragment file %q does not parse: %v", source, err))
	}
	root := documentMapping(&doc)
	if root == nil {
		return fileErr(fmt.Sprintf("fragment file %q must be a YAML mapping (fragment/description/substitutions/node)", source))
	}
	rootKeys := mappingValues(root)
	single, multi := rootKeys["node"], rootKeys["nodes"]
	switch {
	case single != nil && multi != nil:
		return fileErr(fmt.Sprintf("fragment file %q declares both node: and nodes: — a fragment is either one node's behavior or a subgraph, and a file claiming both leaves no reading of which ids it declares", source))
	case single == nil && multi == nil:
		return fileErr(fmt.Sprintf("fragment file %q must declare a node: mapping (the single node this fragment splices in) or a nodes: sequence with an exit: (the subgraph it splices in)", source))
	case single != nil && single.Kind != yaml.MappingNode:
		return fileErr(fmt.Sprintf("fragment file %q must declare a node: mapping — the single node this fragment splices in", source))
	case multi != nil && (multi.Kind != yaml.SequenceNode || len(multi.Content) == 0):
		return fileErr(fmt.Sprintf("fragment file %q must declare nodes: as a non-empty sequence of node mappings", source))
	}
	// body is what the substitution machinery walks: one node mapping, or the
	// whole sequence of them. Both are trees of scalars, and every check below
	// that is about TEXT rather than about wiring reads it without caring which.
	body := single
	if body == nil {
		body = multi
	}

	var errs []*FragmentError
	badFile := func(reason string) {
		errs = append(errs, &FragmentError{Fragment: name, Source: source, Reason: reason})
	}

	// fragment: and description: are checked, not decorative. The FILENAME is
	// what a `use:` resolves, so a `fragment:` disagreeing with it is a typo
	// nobody would ever see — the same silent-mismatch class as a `with:` key
	// the fragment does not declare, which is already a load error. And the
	// description is what every run and every lint prints when this shape is
	// spliced in, so an empty one costs the reader of a run log the answer to
	// "what is this node".
	switch declared := strings.TrimSpace(scalarValue(rootKeys["fragment"])); {
	case declared == "":
		badFile("fragment file must declare fragment: <name>, matching its filename — a fragment that does not say its own name cannot be checked against the name that resolved it")
	case declared != name:
		badFile(fmt.Sprintf("fragment file declares fragment: %q but is stored as %q.yaml — the filename is the name a use: resolves, so a disagreement is a typo no reader would catch; rename the file or fix the key", declared, name))
	}
	description := strings.TrimSpace(scalarValue(rootKeys["description"]))
	if description == "" {
		badFile("fragment file must declare a non-empty description: — it is printed with the disclosure line every time this fragment is spliced, so a run log says what the node is without anyone opening this file")
	}

	substitutions, subErr := substitutionNames(rootKeys["substitutions"])
	if subErr != "" {
		badFile(subErr)
	}

	var ids []string
	declares := make(map[string]bool)
	exit := strings.TrimSpace(scalarValue(rootKeys["exit"]))
	if single != nil {
		bodyKeys := mappingValues(single)
		for _, wiring := range append(append([]string{}, fragmentIDBearingFields...), fragmentLocationFields...) {
			if bodyKeys[wiring] != nil {
				badFile(fmt.Sprintf("fragment declares %q — a single-node fragment carries behavior and declares no ids, so it may name none: id, depends_on and feedback are the using graph's wiring, and cwd/worktree are the using node's location", wiring))
			}
		}
		if rootKeys["exit"] != nil {
			badFile("fragment declares exit: alongside node: — an exit names which of the fragment's OWN nodes a downstream depends_on resolves to, and a single-node fragment has exactly one node, which is the using node itself")
		}
		refuseNestedUse(single, "", badFile)
	} else {
		ids, declares = judgeMultiNodeIDs(multi, badFile)
		judgeMultiNodeWiring(multi, declares, badFile)
		exit = judgeMultiNodeExit(multi, rootKeys["exit"], ids, declares, badFile)
	}
	// A fragment body must be walkable in full, because BOTH halves of the
	// substitution contract are walks: the undeclared-token check below, and
	// the substitution itself. A YAML alias hides its scalars behind a pointer
	// into another part of the file, so `prompt: &p "{{ with.x }}"` / `other:
	// *p` would be neither checked against substitutions: nor substituted —
	// the token would ship verbatim into a paid prompt, which is precisely the
	// silent-verbatim failure the load-time/run-time token split exists to
	// abolish. Refusing is the honest fix rather than descending into
	// node.Alias: the descent would have to inline the target to substitute
	// into it (an alias points at the fragment file's tree, which the cache
	// shares across every using node), and inlining nested aliases is an
	// exponential-expansion bomb on a file the loader reads before any
	// validation. Anchors remain sanctioned everywhere else in a graph file —
	// this is a rule about the one block that gets spliced.
	if containsAlias(body) {
		badFile("the fragment's node: uses a YAML alias (a *reference or a `<<:` merge key) — a spliced body must be walkable in full, or a {{ with.x }} hiding behind the alias would be neither declaration-checked nor substituted and would reach the model verbatim; write the shared value out, or declare it as a substitution point")
	}

	// The invariant applied to the DATA edges, not only the topology ones: a
	// multi-node fragment's {{ artifacts.<id> }} / {{ feedback.<id> }} tokens
	// are rewritten into the namespace against its OWN declared ids, so a token
	// naming anything else names a node in a graph the fragment cannot see. It
	// would survive resolution unrewritten and fail at RUN time, after the
	// upstream nodes were paid for — `run` does not run the advisory lint
	// sweeps. A single-node fragment declares no ids and is left alone here:
	// its tokens name the using graph's nodes, which is the arrangement ADR
	// 0013 shipped and every existing fragment relies on.
	if multi != nil {
		walkScalars(body, func(value string) {
			for _, token := range idTokensIn(value) {
				if declares[token.ref] {
					continue
				}
				badFile(fmt.Sprintf("the fragment body contains %s, and this fragment declares no node %q — a multi-node fragment's artifact and feedback tokens are rewritten into its own namespace, so one naming an id it does not declare could only point at a node in whichever graph happened to cite it", token.token, token.ref))
			}
		})
	}

	// Judge the body's tokens once: a token that claims the with namespace but
	// breaks its grammar can never substitute at all; an undeclared point is an
	// authoring bug in the FRAGMENT, found the first time any graph resolves it;
	// a declared point the body never uses is drift smell, worth an advisory.
	// The scan is loose (every {{ ... }}) so the first class is visible: a
	// strict scan sees only the tokens that are already fine.
	declared := make(map[string]bool, len(substitutions))
	for _, s := range substitutions {
		declared[s] = true
	}
	referenced := make(map[string]bool)
	walkScalars(body, func(value string) {
		for _, token := range looseTokenPattern.FindAllString(value, -1) {
			point, claimsWith := withTokenName(token)
			if !claimsWith {
				continue // another namespace, or deliberate literal text — not this loader's business
			}
			if point == "" {
				badFile(fmt.Sprintf("the fragment body contains %s, which claims the with namespace but is not a substitution token — the grammar is exactly {{ with.<name> }}, lowercase and unfiltered (a substitution point is bound, not filtered), so this one would never substitute and would reach the model verbatim", token))
				continue
			}
			referenced[point] = true
			if !declared[point] {
				badFile(fmt.Sprintf("the fragment body references {{ with.%s }}, which substitutions: does not declare — an undeclared point would silently never substitute", point))
			}
		}
	})
	if len(errs) > 0 {
		return &loadedFragment{errs: errs}
	}

	var advisories []FragmentAdvisory
	for _, s := range substitutions {
		if !referenced[s] {
			advisories = append(advisories, FragmentAdvisory{Fragment: name, Source: source,
				Detail: fmt.Sprintf("substitution point %q is declared but never referenced in the fragment body — harmless at run time, but drift smell (the body moved and the declaration didn't)", s)})
		}
	}
	frag := &fragmentFile{
		name: name, description: description, source: source,
		substitutions: substitutions, referenced: referenced,
		node: single, ids: ids, declares: declares, exit: exit,
	}
	if multi != nil {
		frag.nodes = multi.Content
	}
	return &loadedFragment{frag: frag, advisories: advisories}
}

// judgeMultiNodeIDs reads the ids a multi-node fragment declares — the set
// every edge in it is then held to. An id here is a SEGMENT: the splicer joins
// it to the using node's id with a '/', so an internal id carrying one of its
// own would mint a two-slash id no validator admits.
func judgeMultiNodeIDs(nodes *yaml.Node, badFile func(string)) ([]string, map[string]bool) {
	ids := make([]string, 0, len(nodes.Content))
	declares := make(map[string]bool, len(nodes.Content))
	for i, internal := range nodes.Content {
		if internal.Kind != yaml.MappingNode {
			badFile(fmt.Sprintf("nodes:[%d] is not a mapping — every entry declares one node", i))
			ids = append(ids, "")
			continue
		}
		id := strings.TrimSpace(scalarValue(mappingValues(internal)["id"]))
		ids = append(ids, id)
		switch {
		case id == "":
			badFile(fmt.Sprintf("nodes:[%d] declares no id — a multi-node fragment names its own nodes, and those names are what its edges and its exit: refer to", i))
		case !nodeIDSegmentPattern.MatchString(id):
			badFile(fmt.Sprintf("nodes:[%d] declares id %q, which is not one path element: alphanumerics, '.', '_' or '-', starting with an alphanumeric — a spliced id is <using-id>/<this>, so this half may not carry a separator of its own", i, id))
		case declares[id]:
			badFile(fmt.Sprintf("nodes:[%d] declares duplicate id %q", i, id))
		default:
			declares[id] = true
		}
	}
	return ids, declares
}

// judgeMultiNodeWiring holds every internal node to the invariant: it may name
// ids this fragment declares, and no others. `cwd`/`worktree` stay refused for
// their own reason — they are the using node's location, propagated by value to
// every spliced node — and a nested `use:` stays refused as ADR 0013 made it.
func judgeMultiNodeWiring(nodes *yaml.Node, declares map[string]bool, badFile func(string)) {
	for i, internal := range nodes.Content {
		if internal.Kind != yaml.MappingNode {
			continue // already reported by judgeMultiNodeIDs
		}
		keys := mappingValues(internal)
		label := fmt.Sprintf("nodes:[%d]", i)
		if id := strings.TrimSpace(scalarValue(keys["id"])); id != "" {
			label = fmt.Sprintf("node %q", id)
		}
		for _, located := range fragmentLocationFields {
			if keys[located] != nil {
				badFile(fmt.Sprintf("%s declares %q — a fragment says what its nodes DO, never where they run: cwd and worktree stay on the using node, which propagates them to every spliced node", label, located))
			}
		}
		refuseNestedUse(internal, label, badFile)

		if dependsOn := keys["depends_on"]; dependsOn != nil {
			if dependsOn.Kind != yaml.SequenceNode {
				badFile(fmt.Sprintf("%s declares depends_on that is not a sequence of node ids", label))
			} else {
				for _, parent := range dependsOn.Content {
					judgeInternalReference(parent, declares, label, "depends_on", badFile)
				}
			}
		}
		if feedback := keys["feedback"]; feedback != nil {
			if feedback.Kind != yaml.MappingNode {
				badFile(fmt.Sprintf("%s declares feedback that is not a mapping (rerun/max)", label))
				continue
			}
			judgeInternalReference(mappingValues(feedback)["rerun"], declares, label, "feedback.rerun", badFile)
		}
	}
}

// judgeInternalReference is the invariant at one reference site: the id must be
// one this fragment declares. It is the multi-node form of ADR 0013's refusal —
// there, a fragment could name no id because it declared none; here it may name
// exactly the ones it declared. Everything else, including a '/' reaching into
// some other loop, lands on the same message.
func judgeInternalReference(ref *yaml.Node, declares map[string]bool, label, field string, badFile func(string)) {
	id := strings.TrimSpace(scalarValue(ref))
	if id == "" {
		badFile(fmt.Sprintf("%s: %s names nothing — it must name one of this fragment's own nodes", label, field))
		return
	}
	if !declares[id] {
		badFile(fmt.Sprintf("%s: %s names %q, which this fragment does not declare — a fragment may only wire the nodes it declares itself, so an id from the citing graph has nothing to refer to here", label, field, id))
	}
}

// judgeMultiNodeExit enforces the two rules on `exit:` and returns it.
//
// It is REQUIRED, and deliberately not inferred from the unique sink. Inference
// is right only while there is exactly one sink; the day someone adds a second
// terminal node — a notification, a cleanup, a second reviewer — it either
// picks one or gives up, and when it picks wrong it is wrong SILENTLY. Nothing
// fails, the run proceeds, and the author finds out afterwards from output that
// does not match the shape in their head. One required key costs a line per
// fragment file and buys a load error instead of a lost afternoon.
//
// It also may not lie STRICTLY INSIDE one of the fragment's own feedback
// bodies, and that rule exists so a loop fragment's validity cannot depend on
// the graph citing it. A downstream `depends_on: [qa-a]` resolves to the exit;
// if the exit were a body node other than its arc's declarer, that downstream
// edge would be a side exit (ADR 0010) — a load error the fragment's author
// cannot prevent and cannot see. Checked fragment-locally, charged to the
// fragment file.
func judgeMultiNodeExit(nodes *yaml.Node, declared *yaml.Node, ids []string, declares map[string]bool, badFile func(string)) string {
	exit := strings.TrimSpace(scalarValue(declared))
	if exit == "" {
		badFile("a multi-node fragment must declare exit: <id> — the node a downstream depends_on resolves to. It is not inferred from the sink: inference is right only while there is exactly one, and when it is wrong it is wrong silently, wiring a graph nobody asked for and saying nothing")
		return ""
	}
	if !declares[exit] {
		badFile(fmt.Sprintf("exit: names %q, which this fragment does not declare", exit))
		return ""
	}
	for _, body := range fragmentFeedbackBodies(nodes, ids, declares) {
		if body.members[exit] && exit != body.declarer {
			badFile(fmt.Sprintf("exit: names %q, which lies inside the feedback body %q declares (rerun: %q) — a downstream depends_on would then reach INTO the loop, which is a side exit ADR 0010 refuses, in a graph whose author never wrote it. Exit at the declarer, or downstream of it", exit, body.declarer, body.rerun))
		}
	}
	return exit
}

// fragmentBody is one internal feedback arc's loop body, computed from the
// fragment's own nodes alone.
type fragmentBody struct {
	declarer string
	rerun    string
	members  map[string]bool
}

// fragmentFeedbackBodies computes each internal arc's body the way
// Graph.FeedbackBody does — every node on a depends_on path from the target up
// to and including the declarer — but over the raw fragment file, before any
// splice. The duplication is the price of judging a fragment on its own: the
// graph-level computation needs a *Graph, which does not exist until the
// fragment has been spliced into one, and the whole point of this check is to
// refuse the fragment BEFORE any graph inherits its problem.
func fragmentFeedbackBodies(nodes *yaml.Node, ids []string, declares map[string]bool) []fragmentBody {
	parents := make(map[string][]string, len(ids))
	arcs := make(map[string]string)
	for i, internal := range nodes.Content {
		if internal.Kind != yaml.MappingNode || i >= len(ids) || ids[i] == "" {
			continue
		}
		keys := mappingValues(internal)
		if dependsOn := keys["depends_on"]; dependsOn != nil && dependsOn.Kind == yaml.SequenceNode {
			for _, parent := range dependsOn.Content {
				if id := strings.TrimSpace(scalarValue(parent)); declares[id] {
					parents[ids[i]] = append(parents[ids[i]], id)
				}
			}
		}
		if feedback := keys["feedback"]; feedback != nil && feedback.Kind == yaml.MappingNode {
			if rerun := strings.TrimSpace(scalarValue(mappingValues(feedback)["rerun"])); declares[rerun] {
				arcs[ids[i]] = rerun
			}
		}
	}

	// ancestors is the transitive parent set, visited-set walked so a cyclic
	// (already-refused) fragment still terminates here.
	ancestors := func(id string) map[string]bool {
		seen := make(map[string]bool)
		var walk func(string)
		walk = func(current string) {
			for _, parent := range parents[current] {
				if !seen[parent] {
					seen[parent] = true
					walk(parent)
				}
			}
		}
		walk(id)
		return seen
	}

	bodies := make([]fragmentBody, 0, len(arcs))
	for declarer, rerun := range arcs {
		ofDeclarer := ancestors(declarer)
		members := map[string]bool{declarer: true, rerun: true}
		for _, id := range ids {
			if id != "" && ofDeclarer[id] && ancestors(id)[rerun] {
				members[id] = true
			}
		}
		bodies = append(bodies, fragmentBody{declarer: declarer, rerun: rerun, members: members})
	}
	return bodies
}

// refuseNestedUse rejects a `use:`/`with:` inside a fragment's own node — the
// nesting ADR 0013 deferred and ADR 0027 keeps deferred. Multi-node fragments
// make the temptation sharper and the cost higher: closure needs cycle
// detection over fragment RESOLUTION, on files read before any validation runs,
// plus a policy for namespacing an already-namespaced id.
func refuseNestedUse(node *yaml.Node, label string, badFile func(string)) {
	keys := mappingValues(node)
	if keys["use"] == nil && keys["with"] == nil {
		return
	}
	where := "a fragment's node"
	if label != "" {
		where = "a fragment's " + label
	}
	badFile(where + " may not itself carry use:/with: — fragments do not reference fragments (single-pass resolution, no cycle detection needed)")
}

// withTokenName classifies one {{ ... }} token found in a fragment body.
// claimsWith reports whether the token claims the substitution namespace — its
// leading word is `with`, compared case-INSENSITIVELY, because
// `{{ With.checks }}` is a typo that ships verbatim rather than deliberate
// literal text. point is the substitution point the token names, and is empty
// exactly when the token claims the namespace without obeying the grammar.
//
// A body that genuinely wants the literal text `{{ with ... }}` in a prompt is
// the price: it must write it some other way. That trade is deliberate — a
// fragment body is a template, and a token there is far likelier to be a
// broken substitution than prose.
func withTokenName(token string) (point string, claimsWith bool) {
	body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(token, "{{"), "}}"))
	if !strings.EqualFold(tokenLeadingWord.FindString(body), "with") {
		return "", false
	}
	// The grammar judgment must come from withTokenPattern itself — the exact
	// regex substituteWithTokens replaces with — anchored to the whole token,
	// so what this loader calls well-formed and what actually substitutes can
	// never drift apart.
	if m := withTokenPattern.FindStringSubmatchIndex(token); m != nil && m[0] == 0 && m[1] == len(token) {
		return token[m[2]:m[3]], true
	}
	return "", true
}

// substitutionNames decodes the substitutions: sequence. A missing key means
// no substitution points; anything but a sequence of scalars is a load error
// described by the returned reason.
func substitutionNames(node *yaml.Node) ([]string, string) {
	if node == nil {
		return nil, ""
	}
	if node.Kind != yaml.SequenceNode {
		return nil, "substitutions: must be a sequence of point names"
	}
	names := make([]string, 0, len(node.Content))
	for _, item := range node.Content {
		if item.Kind != yaml.ScalarNode || strings.TrimSpace(item.Value) == "" {
			return nil, "substitutions: entries must be non-empty names (defaults are deferred from v1 — see ADR 0013, Alternatives)"
		}
		names = append(names, item.Value)
	}
	return names, ""
}

// bindingsFor checks the using node's with: mapping against the fragment's
// declared substitution points, both ways: a key the fragment does not
// declare is the same silent-mismatch class as a typoed retry.on cause, and
// an unbound declared point means the fragment's prompt ships with a hole in
// it (there are no defaults in v1). All violations are collected, so a
// mis-wired node reports its whole binding problem at once.
func bindingsFor(withNode *yaml.Node, frag *fragmentFile, nodeID string) (map[string]*yaml.Node, []*FragmentError) {
	var errs []*FragmentError
	bad := func(reason string) {
		errs = append(errs, &FragmentError{NodeID: nodeID, Fragment: frag.name, Source: frag.source, Reason: reason})
	}

	bindings := make(map[string]*yaml.Node)
	var boundOrder []string // document order, so a multi-defect report is deterministic
	if withNode != nil {
		if withNode.Kind != yaml.MappingNode {
			bad("with: must be a mapping of substitution-point names to bound values")
			return nil, errs
		}
		for i := 0; i+1 < len(withNode.Content); i += 2 {
			key := withNode.Content[i].Value
			// An alias is resolved to its target here, once, so both
			// substitution shapes see the bound VALUE: a `*ref` bound to an
			// embedded token is judged by what it refers to rather than
			// rejected as "a alias".
			bindings[key] = resolveAlias(withNode.Content[i+1])
			boundOrder = append(boundOrder, key)
		}
	}

	declared := make(map[string]bool, len(frag.substitutions))
	for _, s := range frag.substitutions {
		declared[s] = true
	}
	for _, key := range boundOrder {
		if !declared[key] {
			bad(fmt.Sprintf("with: binds %q, which the fragment does not declare in substitutions: [%s]", key, strings.Join(frag.substitutions, ", ")))
		}
	}
	for _, s := range frag.substitutions {
		if _, ok := bindings[s]; !ok {
			bad(fmt.Sprintf("substitution point %q is declared by the fragment but left unbound — there are no defaults in v1, so unbound means the fragment ships with a hole in it", s))
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return bindings, nil
}

// substituteWithTokens walks every scalar in the fragment body copy and
// resolves its {{ with.<name> }} tokens against the bindings. Typed when the
// token stands alone (the bound YAML node replaces the scalar wholesale — a
// bound list stays a list, a mapping a mapping), textual when embedded inside
// a longer string, in which case the bound value must itself be a scalar —
// binding a list into an embedded token is a load error, not a Go-side
// coercion. Every token name is already known-declared (the fragment file
// check) and known-bound (bindingsFor), so only the shape can still fail.
func substituteWithTokens(body *yaml.Node, bindings map[string]*yaml.Node, nodeID, fragment string) []*FragmentError {
	var errs []*FragmentError
	walkScalarNodes(body, func(scalar *yaml.Node) {
		matches := withTokenPattern.FindAllStringSubmatchIndex(scalar.Value, -1)
		if len(matches) == 0 {
			return
		}
		// The token is the ENTIRE scalar value: typed replacement.
		if len(matches) == 1 && matches[0][0] == 0 && matches[0][1] == len(scalar.Value) {
			name := scalar.Value[matches[0][2]:matches[0][3]]
			*scalar = *deepCopyNode(bindings[name])
			return
		}
		// Embedded: string substitution, scalars only.
		resolved := withTokenPattern.ReplaceAllStringFunc(scalar.Value, func(token string) string {
			name := withTokenPattern.FindStringSubmatch(token)[1]
			bound := bindings[name]
			if bound.Kind != yaml.ScalarNode {
				errs = append(errs, &FragmentError{NodeID: nodeID, Fragment: fragment,
					Reason: fmt.Sprintf("substitution point %q is embedded in a longer string, so its bound value must be a scalar — got a %s", name, kindName(bound.Kind))})
				return token
			}
			return bound.Value
		})
		if len(errs) > 0 {
			return
		}
		scalar.Value = resolved
		scalar.Tag = "!!str"
	})
	return errs
}

// overlayUsingNode merges the using node's own keys over the substituted
// fragment body, IN the using node's mapping (which the entry document then
// decodes in place). Override granularity is the whole top-level key —
// subtree replacement, never deep merge: if you override a block, you own the
// block, and the reader of the using file can tell what the node does
// without mentally zipping two files' mappings. Returns the keys declared by
// both files (the disclosure list), in the fragment file's key order.
func overlayUsingNode(using *yaml.Node, body *yaml.Node) []string {
	merged := make([]*yaml.Node, 0, len(using.Content)+len(body.Content))
	usingKeys := make(map[string]bool)
	for i := 0; i+1 < len(using.Content); i += 2 {
		key := using.Content[i].Value
		if key == "use" || key == "with" {
			continue
		}
		usingKeys[key] = true
		merged = append(merged, using.Content[i], using.Content[i+1])
	}

	overridden := make([]string, 0)
	for i := 0; i+1 < len(body.Content); i += 2 {
		key := body.Content[i].Value
		if usingKeys[key] {
			overridden = append(overridden, key)
			continue
		}
		merged = append(merged, body.Content[i], body.Content[i+1])
	}
	using.Content = merged
	using.Tag = "!!map"
	return overridden
}

// findNodesSequence locates the top-level nodes: sequence, or nil when the
// document has none (empty file, non-mapping root, missing key — all left
// for decode to judge exactly as it does today).
func findNodesSequence(doc *yaml.Node) *yaml.Node {
	root := documentMapping(doc)
	if root == nil {
		return nil
	}
	nodes := mappingValues(root)["nodes"]
	if nodes == nil || nodes.Kind != yaml.SequenceNode {
		return nil
	}
	return nodes
}

// documentMapping unwraps a document node to its root mapping, or nil.
func documentMapping(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return nil
	}
	return doc
}

// mappingValues indexes a mapping node's values by key. Non-scalar keys are
// skipped (decode rejects them later with its own message).
func mappingValues(mapping *yaml.Node) map[string]*yaml.Node {
	values := make(map[string]*yaml.Node, len(mapping.Content)/2)
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Kind == yaml.ScalarNode {
			values[mapping.Content[i].Value] = mapping.Content[i+1]
		}
	}
	return values
}

// scalarValue returns a node's scalar value, or "" for nil/non-scalar.
func scalarValue(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}
	return node.Value
}

// removeKeys strips the named keys from a mapping node in place.
func removeKeys(mapping *yaml.Node, keys ...string) {
	drop := make(map[string]bool, len(keys))
	for _, k := range keys {
		drop[k] = true
	}
	kept := make([]*yaml.Node, 0, len(mapping.Content))
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if drop[mapping.Content[i].Value] {
			continue
		}
		kept = append(kept, mapping.Content[i], mapping.Content[i+1])
	}
	mapping.Content = kept
}

// walkScalars visits every scalar VALUE in a subtree (mapping keys are not
// substitution surfaces). The read-only sibling of walkScalarNodes.
func walkScalars(node *yaml.Node, visit func(value string)) {
	walkScalarNodes(node, func(scalar *yaml.Node) { visit(scalar.Value) })
}

// walkScalarNodes visits every scalar value node in a subtree, recursively —
// no per-field whitelist: prompt is the common case, not a special one.
func walkScalarNodes(node *yaml.Node, visit func(scalar *yaml.Node)) {
	switch node.Kind {
	case yaml.ScalarNode:
		visit(node)
	case yaml.SequenceNode:
		for _, item := range node.Content {
			walkScalarNodes(item, visit)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			walkScalarNodes(node.Content[i+1], visit)
		}
	}
}

// deepCopyNode clones a yaml.Node subtree so one fragment body can be spliced
// into several using nodes without sharing mutable state. Anchors are dropped
// from the copy: a fragment body may not contain aliases (loadFragmentFile
// refuses them), so an anchor on the copy names nothing, and carrying it into
// the entry document would put a second definition of that anchor name in a
// tree that already has its own.
func deepCopyNode(node *yaml.Node) *yaml.Node {
	copied := *node
	copied.Anchor = ""
	if len(node.Content) > 0 {
		copied.Content = make([]*yaml.Node, len(node.Content))
		for i, child := range node.Content {
			copied.Content[i] = deepCopyNode(child)
		}
	}
	return &copied
}

// containsAlias reports whether a subtree carries a YAML alias node anywhere —
// keys included, since `<<:` merge keys and aliased keys are aliases too.
func containsAlias(node *yaml.Node) bool {
	if node.Kind == yaml.AliasNode {
		return true
	}
	for _, child := range node.Content {
		if containsAlias(child) {
			return true
		}
	}
	return false
}

// resolveAlias follows a YAML alias to the node it names, so a value bound in
// the using node's with: is judged and spliced as the thing it refers to
// rather than as an alias. One hop is the whole walk: an alias node cannot
// itself carry an anchor, so aliases never chain.
func resolveAlias(node *yaml.Node) *yaml.Node {
	if node != nil && node.Kind == yaml.AliasNode && node.Alias != nil {
		return node.Alias
	}
	return node
}

// kindName names a yaml.Kind for error messages.
func kindName(kind yaml.Kind) string {
	switch kind {
	case yaml.ScalarNode:
		return "scalar"
	case yaml.SequenceNode:
		return "list"
	case yaml.MappingNode:
		return "mapping"
	case yaml.AliasNode:
		return "alias"
	default:
		return "document"
	}
}
