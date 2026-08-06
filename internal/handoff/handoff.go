// Package handoff moves data between nodes. It does three things the Scheduler
// delegates to it, so the Scheduler never touches the filesystem or string
// templates itself:
//
//   - interpolate {{ inputs.<name> }}, {{ artifacts.<id> }} and
//     {{ feedback.<id> }} into a node's prompt and cwd before it runs;
//   - persist each node's .result to ~/.oh-my-graph/runs/<run-id>/<node-id>.out
//     so dependents can read it (the artifact-default handoff), a feedback
//     declarer's failing payload to feedback/<node-id>.out so a feedback
//     re-run can read it (ADR 0010 — an INTERNAL file, not a consumer
//     contract), and a FAILED node's own reply to failed/<node-id>.out so the
//     work a failed node already paid for survives it — and, from that same
//     file, hand the reply back to a later leg's retry of the node that wrote
//     it (SeedPriorReply/TakePriorReply, ADR 0016);
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

	"github.com/jitokim/oh-my-graph/internal/fence"
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
	priorReplies  map[string]string // node id -> a PREVIOUS LEG's failed reply, to hand back once (ADR 0016)
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
		priorReplies:  make(map[string]string),
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
	if err := writeUnderRunDir(h.feedbackPath(nodeID), payload, "feedback payload", nodeID); err != nil {
		return err
	}
	h.feedback[nodeID] = payload
	return nil
}

// writeUnderRunDir writes payload to a file inside the run directory with the
// temp+rename discipline runstate.Write uses: the payload lands in a temp file
// in the SAME directory, is synced and closed, then renamed over the final
// path. A rename within one directory is atomic on POSIX, so a reader that
// races a crash sees either the previous complete file or the new one, never a
// torn one. what names the kind of file in error messages ("feedback payload",
// "failed reply"), so one discipline serves both writers and neither can drift
// from the other.
func writeUnderRunDir(path, payload, what, nodeID string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s dir for node %q: %w", what, nodeID, err)
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp %s for node %q: %w", what, nodeID, err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup of the temp file on any failure before the rename;
	// after a successful rename there is nothing left to remove.
	defer os.Remove(tmpName)

	if _, err := tmp.Write([]byte(payload)); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp %s for node %q: %w", what, nodeID, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp %s for node %q: %w", what, nodeID, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp %s for node %q: %w", what, nodeID, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("persist %s for node %q: %w", what, nodeID, err)
	}
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

// failedDir holds one file per FAILED node, holding that node's own reply. It
// is its own subdirectory for exactly the reasons feedback/ is: a flat
// <id>.failed.out would collide with an artifact of a node literally named
// "x.failed" (node ids allow dots), and a consumer globbing the run directory
// flat for artifacts — the pattern docs/RUN-FEED.md teaches — must not pick up
// a reply that failed as though it were a result that passed.
const failedDir = "failed"

// maxFailedReplyBytes bounds ONE failed reply on disk. The reply is
// model-produced and has no length limit of its own, and a node fails at most
// once per leg, so the run directory's worst case is (nodes × this). The bound
// is applied at the WRITE (excerptFailedReply, called by PersistFailure), not
// at a read: the alternative is a run directory that a single runaway node can
// grow without limit, and unlike a ledger Detail — which is bounded where it
// is RENDERED because the full cause is still on disk — there is no other copy
// of this text to fall back to. The cut keeps head and tail and says out loud
// how much it dropped, so a reader is never told a truncated reply is whole.
const maxFailedReplyBytes = 256 * 1024

// PersistFailure writes a FAILED node's own reply to
// <run-dir>/failed/<node-id>.out and returns the path it wrote, so a caller can
// point a human at it. A reply that is empty or all whitespace is NOT written
// and comes back as ("", nil): the existence of the file means "this node said
// something", so an empty file would be a claim the engine cannot make.
//
// This is deliberately not PersistOutput, and it is not a near-miss of it. It
// registers NOTHING: not artifactPaths, so {{ artifacts.<id> }} keeps failing
// for a failed node exactly as it did before (an artifact means "this node
// produced this AND passed"), and not sessions, so a handoff: session child
// cannot resume the conversation of a parent that failed. The only trace it
// leaves is the file — the reply reaches a human, never another node's prompt.
//
// It holds no lock because it shares no state; the atomic rename below makes
// the file itself safe regardless, and a node reaches its terminal failure
// once per leg.
//
// The reply is bounded on the way in (see maxFailedReplyBytes).
func (h *Handoff) PersistFailure(nodeID, reply string) (string, error) {
	if strings.TrimSpace(reply) == "" {
		return "", nil
	}
	path := FailedOutputPath(h.runDir, nodeID)
	if err := writeUnderRunDir(path, excerptFailedReply(reply), "failed reply", nodeID); err != nil {
		return "", err
	}
	return path, nil
}

// DropFailure removes the failed/<node-id>.out an earlier leg wrote, for a node
// that has since PASSED. A missing file is success, not an error: the common
// case is a node that never failed at all.
//
// It is the counterpart of PersistFailure and exists for the same reason the
// write does — the file is a claim about the node, and a claim that has stopped
// being true is worse than no claim. Nothing else about the node changes here:
// its artifact was already persisted by PersistOutput, which is where a passing
// node's result lives.
func (h *Handoff) DropFailure(nodeID string) error {
	if err := os.Remove(FailedOutputPath(h.runDir, nodeID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale failed reply for node %q: %w", nodeID, err)
	}
	return nil
}

// FailedOutputPath reports where PersistFailure writes nodeID's reply under
// runDir, touching no filesystem. It is exported because the file exists to be
// found: a read-back surface locates it by the same computation that wrote it
// rather than by re-spelling the layout somewhere else.
func FailedOutputPath(runDir, nodeID string) string {
	return filepath.Join(runDir, failedDir, sanitizeNodeID(nodeID)+".out")
}

// failedExcerptMarker is the line excerptFailedReply leaves in place of what it
// dropped. It is engine-written text inside model-written text and is NOT a
// trust boundary: anything that later feeds this file to a model must fence it
// with its own per-call nonce, the way the assessor fences artifacts.
const failedExcerptMarker = "\n\n… oh-my-graph excerpted %d bytes here: this reply exceeded the %d-byte cap on failed/<node-id>.out …\n\n"

// excerptFailedReply bounds reply to roughly maxFailedReplyBytes, keeping head
// and tail — a reply's opening frames the problem and its closing usually
// carries the conclusion, which is the half a head-only cut would throw away.
// The cut is fence.HeadAndTail's, shared rather than hand-rolled a second time;
// the marker is this file's own, because it names the figures.
//
// The dropped count is measured from what was actually kept, never from the cap
// that motivated the cut. Deriving it from the cap would report the marker's own
// length (and the rune-boundary trim) as though it were reply text that survived
// — an under-report in the one file whose stated purpose is never to present a
// cut reply as whole.
//
// That makes the marker's own length depend on the figure it has yet to carry,
// so the space for it is RESERVED at the widest that figure can be: the whole
// reply's length, which cannot have fewer digits than the part of it that was
// dropped. One cut, an exact figure, and a file still inside the cap.
func excerptFailedReply(reply string) string {
	if len(reply) <= maxFailedReplyBytes {
		return reply
	}
	reserve := len(fmt.Sprintf(failedExcerptMarker, len(reply), maxFailedReplyBytes))
	head, tail := fence.HeadAndTail(reply, maxFailedReplyBytes-reserve)
	marker := fmt.Sprintf(failedExcerptMarker, len(reply)-len(head)-len(tail), maxFailedReplyBytes)
	return head + marker + tail
}

// SeedPriorReply rehydrates one node's failed reply from the failed/<id>.out
// file PersistFailure wrote, so a `resume --retry-failed` leg can quote the
// previous leg's attempt back to the node it is re-running (ADR 0016). It is
// the cross-process half of that quote: an in-leg retry still has the reply in
// memory, but a resume is a different process and the file is the only thing
// that survived it — which is why the reply is written at all.
//
// A missing file is a clean no-op, not an error, exactly as SeedFeedback's is:
// it means that node left no reply to quote (it never ran, it spawned badly, it
// said nothing), and running the retry with no quote is the pre-ADR-0016
// behaviour, which is correct rather than degraded. Any other read failure is
// returned so the resume path can warn.
//
// seeded is what tells those two nils apart. Without it a caller reading only
// the error cannot distinguish a retry that carries its previous attempt from
// one that silently does not — the same outcome the caller sees when everything
// worked — and the caller is the one that knows whether the file was EXPECTED:
// a judged failure was written by the leg before it, so its absence is worth a
// word, while an unjudged one never had a reply to leave.
//
// Nothing here decides WHETHER the reply should be quoted: the caller does,
// from runstate.NodeRecord.Judged, which is the same isJudgmentFailure verdict
// the in-leg retry is gated on, recorded by the leg that rendered it. Handoff
// reads the file it wrote and nothing more.
func (h *Handoff) SeedPriorReply(nodeID string) (seeded bool, err error) {
	reply, err := os.ReadFile(FailedOutputPath(h.runDir, nodeID))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("re-read failed reply for node %q: %w", nodeID, err)
	}
	h.mu.Lock()
	h.priorReplies[nodeID] = string(reply)
	h.mu.Unlock()
	return true, nil
}

// TakePriorReply hands over a seeded prior reply and FORGETS it, so one seeded
// reply is quoted into exactly one execution of the node.
//
// Taking rather than reading is what keeps it honest across the two ways a node
// can run more than once in a leg. A retry supersedes it immediately (the next
// attempt quotes the attempt this leg just watched fail, not a previous leg's),
// and a feedback re-run of the same node — a whole new round, against new
// artifacts — would otherwise keep being handed a reply from before the loop
// re-armed, which is not the attempt it is repeating.
func (h *Handoff) TakePriorReply(nodeID string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	reply, ok := h.priorReplies[nodeID]
	if ok {
		delete(h.priorReplies, nodeID)
	}
	return reply, ok
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
