// Fragment resolution — ADR 0013. A fragment is a single-node definition file
// under the entry graph's own fragments/ sibling directory, spliced into a
// using node's `use:` at LOAD time: substitute the declared `{{ with.<name> }}`
// points, overlay the using node's own keys, and hand the resolved document to
// the exact same decode → Validate pipeline every graph already goes through.
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
// and one FragmentResolution per resolved `use:` — which is both the
// disclosure line's input and the snapshot's re-encode signal (whenever any
// node resolved a fragment, the snapshot must store the re-encoded resolved
// graph, never the raw entry bytes, or a JSON-authored fragment graph would
// snapshot unresolved and fail its own resume).
type LoadResult struct {
	Graph       *Graph
	Source      []byte
	Resolutions []FragmentResolution
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
	return &LoadResult{Graph: g, Source: data, Resolutions: outcome.resolutions}, nil
}

// LintFile is LoadFile's collect-all counterpart, mirroring Lint: every
// fragment issue plus every structural issue of the resolved graph, first
// element identical to what LoadFile would have failed with — so `lint` and
// `run --dry-run` render a whole list and the two views can never disagree
// about which graph files are valid. Advisory findings travel separately from
// issues, because advice must never affect an exit code. The error return is
// I/O only (the file could not be read); an unreadable file has no list to
// collect.
func LintFile(path string) (issues []error, advisories []FragmentAdvisory, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read graph file %q: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		// A YAML syntax error is the whole report on its own, as in Lint.
		return []error{fmt.Errorf("parse graph YAML: %w", err)}, nil, nil
	}
	outcome := resolveFragments(&doc, path)
	issues = outcome.errs
	g, err := decodeResolved(&doc)
	if err != nil {
		return append(issues, err), outcome.advisories, nil
	}
	return append(issues, g.Issues()...), outcome.advisories, nil
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

// fragmentWiringFields are the node fields a fragment file must NOT declare:
// graph-local wiring (depends_on and feedback name ids that only exist in the
// using graph; a worktree name is lane choreography; cwd is
// invocation-specific; an id is the graph's naming, not the fragment's). A
// fragment is a node's behavior, portable precisely because it says nothing
// about where it sits.
var fragmentWiringFields = []string{"id", "depends_on", "cwd", "worktree", "feedback"}

// fragmentFile is one parsed, structurally-checked fragment definition.
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
	node       *yaml.Node // the node: mapping, never mutated; users splice a deep copy
}

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
	var out fragmentOutcome
	cache := make(map[string]*loadedFragment)
	nodes := findNodesSequence(doc)
	if nodes == nil {
		return out
	}
	for _, nodeMap := range nodes.Content {
		if nodeMap.Kind != yaml.MappingNode {
			continue // decode will report the malformed node itself
		}
		resolveNode(nodeMap, entryPath, cache, &out)
	}
	return out
}

// resolveNode resolves one node mapping in place. No-op for a plain node
// (except the dead-`with:` refusal); for a `use:` node it either splices the
// fragment or strips the fragment keys and records why it could not.
func resolveNode(nodeMap *yaml.Node, entryPath string, cache map[string]*loadedFragment, out *fragmentOutcome) {
	keys := mappingValues(nodeMap)
	id := scalarValue(keys["id"])
	useNode, withNode := keys["use"], keys["with"]

	if useNode == nil {
		if withNode != nil {
			out.errs = append(out.errs, &FragmentError{NodeID: id,
				Reason: "with: without use: — a binding with no fragment to bind is a dead key, which is a wiring bug, not a style choice"})
			removeKeys(nodeMap, "with")
		}
		return
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
		return
	}
	if !fragmentNamePattern.MatchString(name) {
		fail(&FragmentError{NodeID: id, Fragment: name,
			Reason: "use: must be a bare fragment name (letters, digits, then any of . _ -), not a path — a use: resolves against the graph file's own fragments/ sibling and nowhere else, so a separator, a leading dot or a .. has no location to mean"})
		return
	}
	if keys["prompt"] != nil {
		fail(&FragmentError{NodeID: id, Fragment: name,
			Reason: "prompt: alongside use: — a wholesale prompt override recreates the copy-variation drift fragments exist to kill, while still claiming the fragment's name; customize through the fragment's declared substitution points, or write an inline node honestly"})
		return
	}

	lf := loadFragmentCached(name, entryPath, cache, out)
	if lf.frag == nil {
		// The file's own errors were reported once, on first load; this
		// node still cannot resolve, so its keys are stripped either way.
		fail(chargeTo(id, lf.errs)...)
		return
	}

	bindings, bindErrs := bindingsFor(withNode, lf.frag, id)
	if len(bindErrs) > 0 {
		fail(bindErrs...)
		return
	}

	body := deepCopyNode(lf.frag.node)
	if errs := substituteWithTokens(body, bindings, id, name); len(errs) > 0 {
		fail(errs...)
		return
	}

	overridden := overlayUsingNode(nodeMap, body)
	out.resolutions = append(out.resolutions, FragmentResolution{
		NodeID: id, Fragment: name, Description: lf.frag.description,
		Source: lf.frag.source, Overridden: overridden,
	})
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
		return fileErr(fmt.Sprintf("no fragment file at the fragment location %q — a use: resolves against the graph file's own fragments/ sibling, and nowhere else", source))
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
	body := rootKeys["node"]
	if body == nil || body.Kind != yaml.MappingNode {
		return fileErr(fmt.Sprintf("fragment file %q must declare a node: mapping — the single node this fragment splices in", source))
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

	bodyKeys := mappingValues(body)
	for _, wiring := range fragmentWiringFields {
		if bodyKeys[wiring] != nil {
			badFile(fmt.Sprintf("fragment declares %q — a fragment carries behavior, never wiring (id, depends_on, cwd, worktree and feedback are graph-local; they belong on the using node)", wiring))
		}
	}
	if bodyKeys["use"] != nil || bodyKeys["with"] != nil {
		badFile("a fragment's node may not itself carry use:/with: — fragments do not reference fragments in v1 (single-pass resolution, no cycle detection needed)")
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

	// Judge the body's tokens once: an undeclared point is an authoring bug
	// in the FRAGMENT, found the first time any graph resolves it; a declared
	// point the body never uses is drift smell, worth an advisory.
	declared := make(map[string]bool, len(substitutions))
	for _, s := range substitutions {
		declared[s] = true
	}
	referenced := make(map[string]bool)
	walkScalars(body, func(value string) {
		for _, match := range withTokenPattern.FindAllStringSubmatch(value, -1) {
			referenced[match[1]] = true
			if !declared[match[1]] {
				badFile(fmt.Sprintf("the fragment body references {{ with.%s }}, which substitutions: does not declare — an undeclared point would silently never substitute", match[1]))
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
	return &loadedFragment{
		frag:       &fragmentFile{name: name, description: description, source: source, substitutions: substitutions, referenced: referenced, node: body},
		advisories: advisories,
	}
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
