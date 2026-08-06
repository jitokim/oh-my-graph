// Package handoff moves data between nodes. It does three things the Scheduler
// delegates to it, so the Scheduler never touches the filesystem or string
// templates itself:
//
//   - interpolate {{ inputs.<name> }}, {{ artifacts.<id> }} and
//     {{ feedback.<id> }} into a node's prompt and cwd before it runs;
//   - persist each node's .result to ~/.oh-my-graph/runs/<run-id>/<node-id>.out
//     so dependents can read it (the artifact-default handoff), and a feedback
//     declarer's failing payload to feedback/<node-id>.out so a feedback
//     re-run can read it (ADR 0010 — an INTERNAL file, not a consumer
//     contract);
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
	Kind      string // the placeholder kind at fault: "inputs", "artifacts" or "feedback"
	Reference string // the name/id that could not be resolved
	Reason    string
}

func (e *InterpolationError) Error() string {
	return fmt.Sprintf("cannot resolve {{ %s.%s }}: %s", e.Kind, e.Reference, e.Reason)
}

// placeholderPattern matches {{ inputs.name }} / {{ artifacts.id }} /
// {{ feedback.id }} with an optional `| inline` filter. Group 1 = kind,
// group 2 = reference, group 3 = filter (empty or "inline"). Whitespace
// around each token is tolerated. The filter is only meaningful on
// artifacts; a feedback placeholder always inlines and resolveLocked rejects
// a filter on it loudly (graph.Validate already refuses it at load for any
// graph that came through Parse).
var placeholderPattern = regexp.MustCompile(
	`\{\{\s*(inputs|artifacts|feedback)\.([A-Za-z0-9._-]+)\s*(?:\|\s*(inline)\s*)?\}\}`,
)

// ContainsPlaceholder reports whether s holds any sequence Interpolate would
// treat as a live placeholder. It exists for code that must guarantee text is
// template-inert — the coordinator's skill-inlining neutralizer tests against
// it — so that guarantee is judged by the engine's own pattern and can never
// drift from what Interpolate and LintPlaceholders actually match.
func ContainsPlaceholder(s string) bool {
	return placeholderPattern.MatchString(s)
}

// Handoff owns the run directory and the accumulating state of completed nodes.
type Handoff struct {
	runDir string
	inputs map[string]string

	mu            sync.Mutex
	artifactPaths map[string]string // node id -> persisted .out path
	sessions      map[string]string // node id -> claude session id
	feedback      map[string]string // declarer id -> latest feedback payload (ADR 0010)
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
		feedback:      make(map[string]string),
	}
}

// Interpolate substitutes every {{ inputs.x }}, {{ artifacts.id }} and
// {{ feedback.id }} in tmpl.
//
//   - inputs resolve to the bound input value.
//   - artifacts resolve to the persisted .out FILE PATH by default (robust,
//     lets the node read it however it likes); the `| inline` filter instead
//     inlines the file's content.
//   - feedback ALWAYS inlines the declarer's latest feedback payload, and
//     resolves to the EMPTY string while no round has fired (ADR 0010) —
//     the one namespace where "not there yet" is an expected state rather
//     than a wiring bug, which is why graph.Validate confines the token to
//     the declarer's loop body at load.
//
// An unknown input, or an artifact whose producer has not persisted yet, is an
// *InterpolationError — never a silent empty substitution. The feedback
// namespace's empty default is deliberately NOT that: it is a documented
// value, confined to the one place it can mean something.
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

	if kind == "feedback" {
		if filter != "" {
			return "", &InterpolationError{
				Kind:      kind,
				Reference: ref,
				Reason:    "a feedback placeholder takes no filter — {{ feedback.<id> }} always inlines the payload",
			}
		}
		// Empty while no round has fired: the documented first-pass default,
		// never an error (see Interpolate).
		return h.feedback[ref], nil
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
//
// Owner-only (0o700 / 0o600), the same stance saveGeneratedSpec takes for a
// saved plan and for the same reason: an artifact is a model's full reply, and
// with `| inline` it is the text that then goes into a downstream node's
// prompt. On a shared machine that is nobody else's business. This narrows the
// at-rest exposure only — a running node's prompt is still visible in argv to
// any co-tenant (SECURITY.md, "What is exposed while a node runs").
func (h *Handoff) PersistOutput(nodeID, result, sessionID string) error {
	if err := os.MkdirAll(h.runDir, 0o700); err != nil {
		return fmt.Errorf("create run dir %q: %w", h.runDir, err)
	}
	path := h.artifactPath(nodeID)
	if err := os.WriteFile(path, []byte(result), 0o600); err != nil {
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

// SetFeedback records a feedback declarer's payload — its result text when
// the failing execution produced one, else its failure detail (the Scheduler
// decides which) — so the loop body's re-run resolves {{ feedback.<id> }} to
// it. The payload is also persisted to <run-dir>/feedback/<node-id>.out,
// overwritten per round (latest wins): an INTERNAL implementation file, not
// a documented consumer contract (ADR 0010) — it exists so a run stopped
// mid-loop can re-seed the payload on resume (SeedFeedback). The .out
// artifact contract is untouched: artifacts live flat in the run directory,
// payloads under feedback/, so a payload never means "a passed node's
// result" — and a node literally named "x.feedback" cannot collide with
// node "x"'s payload file (node ids allow dots).
//
// The mutex is held across the file write AND the map update: two rounds
// that overlapped for the same declarer could otherwise leave the file
// holding one payload and the map the other, and a resume (which reads the
// file) would then disagree with the run it resumed.
//
// The file itself is written with the same temp+rename discipline as
// runstate.Write: the payload lands in a temp file in the feedback
// directory, is synced and closed, then renamed over the final path. A
// rename within one directory is atomic on POSIX, so a resume that races a
// crash reads either the previous round's complete payload or the new one,
// never a torn file — and the map is updated only after the rename, so the
// in-memory payload never runs ahead of what a resume would see. The payload
// ends up owner-only like a PersistOutput artifact without a mode argument
// here: os.CreateTemp creates at 0o600 and the rename carries that mode over.
func (h *Handoff) SetFeedback(nodeID, payload string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	path := h.feedbackPath(nodeID)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create feedback dir for node %q: %w", nodeID, err)
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp feedback payload for node %q: %w", nodeID, err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup of the temp file on any failure before the rename;
	// after a successful rename there is nothing left to remove.
	defer os.Remove(tmpName)

	if _, err := tmp.Write([]byte(payload)); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp feedback payload for node %q: %w", nodeID, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp feedback payload for node %q: %w", nodeID, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp feedback payload for node %q: %w", nodeID, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("persist feedback payload for node %q: %w", nodeID, err)
	}

	h.feedback[nodeID] = payload
	return nil
}

// SeedFeedback rehydrates one declarer's feedback payload for a resumed run
// from the feedback/<id>.out file SetFeedback persisted — the payload's analogue
// of Seed. A missing file is a clean no-op, not an error: it means no round
// had fired (or the payload predates a crash that lost it), and the
// namespace's documented empty default is exactly the right degraded
// behaviour. Any other read failure is returned so the resume path can warn
// rather than silently run a round without its feedback.
func (h *Handoff) SeedFeedback(nodeID string) error {
	payload, err := os.ReadFile(h.feedbackPath(nodeID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("re-read feedback payload for node %q: %w", nodeID, err)
	}
	h.mu.Lock()
	h.feedback[nodeID] = string(payload)
	h.mu.Unlock()
	return nil
}

// feedbackPath is the on-disk location of a declarer's persisted feedback
// payload, sanitized exactly as artifactPath is and for the same reason. It
// lives under its own feedback/ directory rather than sharing the run
// directory with artifacts: node ids allow dots, so a suffix scheme like
// <id>.feedback.out would let a node literally named "x.feedback" produce an
// artifact at node "x"'s payload path.
func (h *Handoff) feedbackPath(nodeID string) string {
	return filepath.Join(h.runDir, "feedback", sanitizeNodeID(nodeID)+".out")
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

// artifactPath is the on-disk location of a node's persisted result.
func (h *Handoff) artifactPath(nodeID string) string {
	return filepath.Join(h.runDir, sanitizeNodeID(nodeID)+".out")
}

// sanitizeNodeID makes a node id safe as a single path element. Node ids are
// validated (no path separators expected), but both '/' and this OS's own
// separator are replaced so a stray separator can never escape the run
// directory on any platform — Windows resolves '/' as a separator too, so
// sanitizing os.PathSeparator alone would leave a '/'-carrying id escaping
// there.
func sanitizeNodeID(nodeID string) string {
	safe := strings.ReplaceAll(nodeID, "/", "_")
	return strings.ReplaceAll(safe, string(os.PathSeparator), "_")
}
