// Package handoff moves data between nodes. It does three things the Scheduler
// delegates to it, so the Scheduler never touches the filesystem or string
// templates itself:
//
//   - interpolate {{ inputs.<name> }} and {{ artifacts.<id> }} into a node's
//     prompt and cwd before it runs;
//   - persist each node's .result to ~/.oh-my-graph/runs/<run-id>/<node-id>.out
//     so dependents can read it (the artifact-default handoff);
//   - resolve which claude session a session-handoff node resumes.
//
// It is safe for concurrent use: parallel nodes interpolate and persist at the
// same time, guarded by one mutex over the shared maps.
package handoff

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// InterpolationError names the reference a template asked for that could not be
// resolved — a missing input or an artifact whose producer has not run. A named
// type (not a bare fmt error) so the Scheduler can tell a template problem from
// a run failure and report the exact reference at fault.
type InterpolationError struct {
	Kind      string // "inputs" or "artifacts"
	Reference string // the name/id that could not be resolved
	Reason    string
}

func (e *InterpolationError) Error() string {
	return fmt.Sprintf("cannot resolve {{ %s.%s }}: %s", e.Kind, e.Reference, e.Reason)
}

// placeholderPattern matches {{ inputs.name }} / {{ artifacts.id }} with an
// optional `| inline` filter. Group 1 = kind, group 2 = reference, group 3 =
// filter (empty or "inline"). Whitespace around each token is tolerated.
var placeholderPattern = regexp.MustCompile(
	`\{\{\s*(inputs|artifacts)\.([A-Za-z0-9_-]+)\s*(?:\|\s*(inline)\s*)?\}\}`,
)

// Handoff owns the run directory and the accumulating state of completed nodes.
type Handoff struct {
	runDir string
	inputs map[string]string

	mu            sync.Mutex
	artifactPaths map[string]string // node id -> persisted .out path
	sessions      map[string]string // node id -> claude session id
}

// New builds a Handoff bound to a run directory and the invocation's inputs. The
// inputs map is copied so a caller may keep mutating its own.
func New(runDir string, inputs map[string]string) *Handoff {
	copied := make(map[string]string, len(inputs))
	for k, v := range inputs {
		copied[k] = v
	}
	return &Handoff{
		runDir:        runDir,
		inputs:        copied,
		artifactPaths: make(map[string]string),
		sessions:      make(map[string]string),
	}
}

// Interpolate substitutes every {{ inputs.x }} and {{ artifacts.id }} in tmpl.
//
//   - inputs resolve to the bound input value.
//   - artifacts resolve to the persisted .out FILE PATH by default (robust,
//     lets the node read it however it likes); the `| inline` filter instead
//     inlines the file's content.
//
// An unknown input, or an artifact whose producer has not persisted yet, is an
// *InterpolationError — never a silent empty substitution.
func (h *Handoff) Interpolate(tmpl string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	var firstErr error
	out := placeholderPattern.ReplaceAllStringFunc(tmpl, func(match string) string {
		groups := placeholderPattern.FindStringSubmatch(match)
		kind, ref, filter := groups[1], groups[2], groups[3]

		value, err := h.resolveLocked(kind, ref, filter)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		return value
	})
	if firstErr != nil {
		return "", firstErr
	}
	return out, nil
}

// resolveLocked resolves one placeholder. Caller must hold h.mu.
func (h *Handoff) resolveLocked(kind, ref, filter string) (string, error) {
	if kind == "inputs" {
		value, ok := h.inputs[ref]
		if !ok {
			return "", &InterpolationError{Kind: kind, Reference: ref, Reason: "no such input was provided"}
		}
		return value, nil
	}

	// kind == "artifacts"
	path, ok := h.artifactPaths[ref]
	if !ok {
		return "", &InterpolationError{
			Kind:      kind,
			Reference: ref,
			Reason:    "artifact not available (its producing node has not completed)",
		}
	}
	if filter != "inline" {
		return path, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", &InterpolationError{
			Kind:      kind,
			Reference: ref,
			Reason:    fmt.Sprintf("cannot inline artifact file: %v", err),
		}
	}
	return string(content), nil
}

// PersistOutput records a completed node: it writes the node's result to
// <run-dir>/<node-id>.out and remembers both the artifact path (for dependents'
// {{ artifacts.<id> }}) and the session id (for a session-child's --resume and
// for the ledger). Called once per successful node.
func (h *Handoff) PersistOutput(nodeID, result, sessionID string) error {
	if err := os.MkdirAll(h.runDir, 0o755); err != nil {
		return fmt.Errorf("create run dir %q: %w", h.runDir, err)
	}
	path := h.artifactPath(nodeID)
	if err := os.WriteFile(path, []byte(result), 0o644); err != nil {
		return fmt.Errorf("persist output for node %q: %w", nodeID, err)
	}

	h.mu.Lock()
	h.artifactPaths[nodeID] = path
	h.sessions[nodeID] = sessionID
	h.mu.Unlock()
	return nil
}

// Seed rehydrates one already-completed node's handoff state for a resumed run,
// without re-running the node and without writing anything to disk. On resume the
// earlier leg's .out artifact file is still on disk exactly where PersistOutput
// left it, so Seed only re-populates the same in-memory maps PersistOutput would
// have: the artifact path (so a dependent's {{ artifacts.<id> }} resolves to the
// existing file) and the session id (so a handoff: session child can --resume the
// parent it never watched run in this process).
//
// Seed is deliberately ignorant of where its arguments came from — the resume
// path, which owns the run snapshot, feeds it the recorded path and session id,
// exactly as the Scheduler feeds PersistOutput a fresh node's result. Handoff
// never learns what a snapshot is; the dependency runs snapshot → Handoff, never
// the reverse (see DESIGN.md, "Handoff ... Seed"). It does no I/O, so it cannot
// fail and returns nothing; a caller that seeds a path to a missing file only
// finds out if a dependent later interpolates it with the `| inline` filter,
// which is the same InterpolationError a normal run would raise.
func (h *Handoff) Seed(nodeID, artifactPath, sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.artifactPaths[nodeID] = artifactPath
	h.sessions[nodeID] = sessionID
}

// ResumeSessionFor returns the claude session id a session-handoff node must
// resume — the session of its single parent. It returns "" and nil for any node
// that is not a session-handoff node (an artifact node resumes nothing). It is
// an error to reach here with a session node whose parent has no recorded
// session; validation guarantees exactly one parent, so this only fires on a
// genuine scheduling bug.
func (h *Handoff) ResumeSessionFor(node graph.Node) (string, error) {
	if node.Handoff != graph.HandoffSession {
		return "", nil
	}
	if len(node.DependsOn) != 1 {
		return "", fmt.Errorf(
			"node %q has handoff: session but %d parents; validation should have rejected this",
			node.ID, len(node.DependsOn),
		)
	}
	parent := node.DependsOn[0]

	h.mu.Lock()
	session, ok := h.sessions[parent]
	h.mu.Unlock()
	if !ok || session == "" {
		return "", fmt.Errorf(
			"node %q cannot resume: parent %q has no recorded session id",
			node.ID, parent,
		)
	}
	return session, nil
}

// ArtifactPath returns the on-disk path recorded for a completed node's
// output — the same path {{ artifacts.<id> }} resolves to — and whether one
// exists. Exposed (read-only; the map itself stays private) for the run-state
// recorder, which persists it into the snapshot so a later resume's
// Handoff.Seed can rehydrate it without recomputing anything from the node id
// (see runstate.NodeRecord.ArtifactPath).
func (h *Handoff) ArtifactPath(nodeID string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	path, ok := h.artifactPaths[nodeID]
	return path, ok
}

// artifactPath is the on-disk location of a node's persisted result. Node ids
// are validated (no path separators expected), but both '/' and this OS's own
// separator are replaced so a stray separator can never escape the run
// directory on any platform — Windows resolves '/' as a separator too, so
// sanitizing os.PathSeparator alone would leave a '/'-carrying id escaping
// there.
func (h *Handoff) artifactPath(nodeID string) string {
	safe := strings.ReplaceAll(nodeID, "/", "_")
	safe = strings.ReplaceAll(safe, string(os.PathSeparator), "_")
	return filepath.Join(h.runDir, safe+".out")
}
