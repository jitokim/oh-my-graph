package handoff

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// TestPersistFailure_KeepsTheReplyWhole is the property the whole change
// exists for: after a node fails, its own words are still readable. The
// assertion is on the bytes — a test that only checked "a file exists" would
// pass against a file the engine wrote empty.
func TestPersistFailure_KeepsTheReplyWhole(t *testing.T) {
	dir := t.TempDir()
	h := New(dir, nil)
	reply := "the lock is pre-flock, so ProbeLock folds unmarked into LivenessUnknown"

	path, err := h.PersistFailure("verify", reply)
	if err != nil {
		t.Fatalf("PersistFailure: %v", err)
	}
	if path == "" {
		t.Fatal("PersistFailure reported no path for a reply it should have kept")
	}

	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed reply not persisted: %v", err)
	}
	if string(onDisk) != reply {
		t.Fatalf("persisted reply differs from the node's:\n got %q\nwant %q", onDisk, reply)
	}
	if want := FailedOutputPath(dir, "verify"); path != want {
		t.Fatalf("PersistFailure wrote %q, but FailedOutputPath points at %q", path, want)
	}
}

// TestPersistFailure_IsNotAnArtifact is the load-bearing separation:
// {{ artifacts.<id> }} means "this node produced this AND passed". A failed
// node's reply must not be reachable through it, must not sit at the flat
// artifact path a consumer globs for, and must not leave a session a
// handoff: session child could resume.
func TestPersistFailure_IsNotAnArtifact(t *testing.T) {
	dir := t.TempDir()
	h := New(dir, nil)

	if _, err := h.PersistFailure("dev", "here is what I found"); err != nil {
		t.Fatalf("PersistFailure: %v", err)
	}

	if _, err := h.Interpolate("{{ artifacts.dev }}"); err == nil {
		t.Fatal("{{ artifacts.dev }} resolved for a node that FAILED")
	} else {
		var interpErr *InterpolationError
		if !errors.As(err, &interpErr) || interpErr.Kind != "artifacts" {
			t.Fatalf("want an artifacts InterpolationError, got %v", err)
		}
	}
	if _, ok := h.ArtifactPath("dev"); ok {
		t.Fatal("a failed reply registered itself as an artifact path")
	}
	if _, err := os.Stat(filepath.Join(dir, "dev.out")); !os.IsNotExist(err) {
		t.Errorf("a failed reply landed at the flat artifact path (stat err = %v)", err)
	}

	child := graph.Node{ID: "next", Handoff: graph.HandoffSession, DependsOn: []string{"dev"}}
	if _, err := h.ResumeSessionFor(child); err == nil {
		t.Fatal("a failed node's session became resumable")
	}
}

// TestPersistFailure_SilenceWritesNothing: the file's existence is the claim
// "this node said something", so a node that said nothing must leave no file
// to misread. Whitespace is silence.
func TestPersistFailure_SilenceWritesNothing(t *testing.T) {
	for _, reply := range []string{"", "   \n\t "} {
		dir := t.TempDir()
		h := New(dir, nil)

		path, err := h.PersistFailure("quiet", reply)
		if err != nil {
			t.Fatalf("PersistFailure(%q): %v", reply, err)
		}
		if path != "" {
			t.Fatalf("PersistFailure(%q) reported path %q for a reply with no content", reply, path)
		}
		if _, err := os.Stat(FailedOutputPath(dir, "quiet")); !os.IsNotExist(err) {
			t.Errorf("PersistFailure(%q) left a file behind (stat err = %v)", reply, err)
		}
	}
}

// TestPersistFailure_BoundsAnUnboundedReply: a reply is model-produced with no
// length limit of its own and there is no second copy of it anywhere, so the
// bound is applied at the write. What survives is head AND tail — the opening
// frames the problem, the closing usually carries the conclusion — with the
// size of the cut stated in the file rather than left for a reader to guess.
func TestPersistFailure_BoundsAnUnboundedReply(t *testing.T) {
	dir := t.TempDir()
	h := New(dir, nil)
	head := "OPENING: the run pauses because "
	tail := " CONCLUSION: the lock predates flock."
	reply := head + strings.Repeat("x", 4*maxFailedReplyBytes) + tail

	path, err := h.PersistFailure("windy", reply)
	if err != nil {
		t.Fatalf("PersistFailure: %v", err)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed reply not persisted: %v", err)
	}

	if len(onDisk) > maxFailedReplyBytes {
		t.Errorf("persisted %d bytes, over the %d-byte cap", len(onDisk), maxFailedReplyBytes)
	}
	// Bounded, but not gutted: a cap that kept a token of the reply would
	// satisfy the size assertion and defeat the purpose.
	if len(onDisk) < maxFailedReplyBytes/2 {
		t.Errorf("persisted only %d bytes of a %d-byte reply; the cap is %d",
			len(onDisk), len(reply), maxFailedReplyBytes)
	}
	if !strings.HasPrefix(string(onDisk), head) {
		t.Error("the excerpt dropped the head of the reply")
	}
	if !strings.HasSuffix(string(onDisk), tail) {
		t.Error("the excerpt dropped the tail of the reply — where a conclusion lives")
	}
	if !strings.Contains(string(onDisk), "excerpted") {
		t.Error("the excerpt is silent about the cut it made")
	}
}

// TestPersistFailure_UnderCapIsUntouched guards the bound from the other side:
// an ordinary reply must arrive byte-identical, with no marker inserted into
// text the model did not write.
func TestPersistFailure_UnderCapIsUntouched(t *testing.T) {
	dir := t.TempDir()
	h := New(dir, nil)
	reply := strings.Repeat("a diagnosis worth keeping. ", 200)

	path, err := h.PersistFailure("dev", reply)
	if err != nil {
		t.Fatalf("PersistFailure: %v", err)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed reply not persisted: %v", err)
	}
	if string(onDisk) != reply {
		t.Fatalf("a %d-byte reply under the %d-byte cap was modified", len(reply), maxFailedReplyBytes)
	}
}

// TestPersistFailure_DoesNotCollideWithFeedback: both files are named after
// the node and both end in .out, and one node can produce both (a feedback
// declarer that exhausts its rounds). They must be two files, each holding
// what its own writer wrote.
func TestPersistFailure_DoesNotCollideWithFeedback(t *testing.T) {
	dir := t.TempDir()
	h := New(dir, nil)

	if err := h.SetFeedback("review", "round 1 findings"); err != nil {
		t.Fatalf("SetFeedback: %v", err)
	}
	failedPath, err := h.PersistFailure("review", "final round: still not fixed")
	if err != nil {
		t.Fatalf("PersistFailure: %v", err)
	}

	payload, err := os.ReadFile(filepath.Join(dir, "feedback", "review.out"))
	if err != nil {
		t.Fatalf("feedback payload lost: %v", err)
	}
	if string(payload) != "round 1 findings" {
		t.Fatalf("the failed reply overwrote the feedback payload: %q", payload)
	}
	failed, err := os.ReadFile(failedPath)
	if err != nil {
		t.Fatalf("failed reply lost: %v", err)
	}
	if string(failed) != "final round: still not fixed" {
		t.Fatalf("failed reply holds the wrong text: %q", failed)
	}
}

// TestPersistFailure_DottedNodeID: node ids may contain dots and separators.
// The reply of a node named "x.failed" must not be able to land where node
// "x"'s reply goes, and no id may escape the run directory.
func TestPersistFailure_DottedNodeID(t *testing.T) {
	dir := t.TempDir()
	h := New(dir, nil)

	plain, err := h.PersistFailure("x", "reply from x")
	if err != nil {
		t.Fatalf("PersistFailure(x): %v", err)
	}
	dotted, err := h.PersistFailure("x.failed", "reply from x.failed")
	if err != nil {
		t.Fatalf("PersistFailure(x.failed): %v", err)
	}
	if plain == dotted {
		t.Fatalf("two nodes share one reply file: %q", plain)
	}
	for id, path := range map[string]string{"x": plain, "x.failed": dotted} {
		onDisk, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reply for %q not persisted: %v", id, err)
		}
		if want := "reply from " + id; string(onDisk) != want {
			t.Fatalf("reply for %q holds %q, want %q", id, onDisk, want)
		}
	}

	escaping := FailedOutputPath(dir, "../escape")
	if !strings.HasPrefix(filepath.Clean(escaping), filepath.Clean(dir)) {
		t.Fatalf("a node id escaped the run directory: %q", escaping)
	}
}

// TestPersistFailure_LatestWins: a second write for the same node replaces the
// first rather than appending or leaving the earlier text half-overwritten.
func TestPersistFailure_LatestWins(t *testing.T) {
	dir := t.TempDir()
	h := New(dir, nil)

	if _, err := h.PersistFailure("dev", "first attempt"); err != nil {
		t.Fatalf("PersistFailure: %v", err)
	}
	path, err := h.PersistFailure("dev", "second attempt")
	if err != nil {
		t.Fatalf("PersistFailure: %v", err)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed reply not persisted: %v", err)
	}
	if string(onDisk) != "second attempt" {
		t.Fatalf("latest write did not win: %q", onDisk)
	}

	// The temp file the atomic write uses must not be left behind to be
	// mistaken for a second node's reply by anything listing the directory.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read failed dir: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("failed/ holds %v, want exactly the one reply file", names)
	}
}

// --- handing the reply back to the leg that repeats the attempt (ADR 0016) ---

// TestSeedPriorReply_RoundTripsWhatPersistFailureWrote is the cross-process
// half of the retry quote: a second process, holding only the run directory,
// recovers the reply the first one persisted.
func TestSeedPriorReply_RoundTripsWhatPersistFailureWrote(t *testing.T) {
	dir := t.TempDir()
	if _, err := New(dir, nil).PersistFailure("dev", "what the failed attempt said"); err != nil {
		t.Fatalf("PersistFailure: %v", err)
	}

	// A fresh Handoff over the same directory — this is what `resume` builds.
	next := New(dir, nil)
	if err := next.SeedPriorReply("dev"); err != nil {
		t.Fatalf("SeedPriorReply: %v", err)
	}
	reply, ok := next.TakePriorReply("dev")
	if !ok || reply != "what the failed attempt said" {
		t.Fatalf("TakePriorReply = %q, %v; want the persisted reply", reply, ok)
	}
}

// TestTakePriorReply_HandsItOverExactlyOnce: one seeded reply belongs to one
// execution. A node that runs again in the same leg — a feedback round, a
// retry — is repeating a DIFFERENT attempt, and must not be handed a reply from
// before that.
func TestTakePriorReply_HandsItOverExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	h := New(dir, nil)
	if _, err := h.PersistFailure("dev", "prior"); err != nil {
		t.Fatalf("PersistFailure: %v", err)
	}
	if err := h.SeedPriorReply("dev"); err != nil {
		t.Fatalf("SeedPriorReply: %v", err)
	}

	if reply, ok := h.TakePriorReply("dev"); !ok || reply != "prior" {
		t.Fatalf("first TakePriorReply = %q, %v; want the seeded reply", reply, ok)
	}
	if reply, ok := h.TakePriorReply("dev"); ok {
		t.Fatalf("second TakePriorReply returned %q; the reply must be handed over once", reply)
	}
}

// TestSeedPriorReply_MissingFileIsACleanNoOp: a node that left no reply (it
// never ran, it failed to spawn, it said nothing) seeds nothing and errors
// about nothing — running the retry without a quote is the pre-ADR-0016
// behaviour, which is correct rather than degraded.
func TestSeedPriorReply_MissingFileIsACleanNoOp(t *testing.T) {
	h := New(t.TempDir(), nil)
	if err := h.SeedPriorReply("never-ran"); err != nil {
		t.Fatalf("SeedPriorReply on a missing file = %v, want nil", err)
	}
	if reply, ok := h.TakePriorReply("never-ran"); ok {
		t.Fatalf("TakePriorReply = %q, want nothing seeded", reply)
	}
}

// TestSeedPriorReply_UnreadableFileIsReported: any failure that is NOT "no such
// file" comes back, so the caller warns instead of silently retrying without a
// quote it was told existed.
func TestSeedPriorReply_UnreadableFileIsReported(t *testing.T) {
	dir := t.TempDir()
	// A directory where the reply file should be: readable as an entry,
	// unreadable as a file, and not an IsNotExist.
	if err := os.MkdirAll(FailedOutputPath(dir, "dev"), 0o755); err != nil {
		t.Fatalf("stage an unreadable reply: %v", err)
	}
	err := New(dir, nil).SeedPriorReply("dev")
	if err == nil {
		t.Fatal("SeedPriorReply on an unreadable path = nil, want the failure reported")
	}
	if !strings.Contains(err.Error(), "dev") {
		t.Errorf("error %q does not name the node it is about", err)
	}
}

// TestPersistFailure_RegistersNothing_PriorReplyIsNotAnArtifact re-states the
// boundary now that a second reader exists: keeping a reply for the retry that
// repeats it still does not make it an artifact or a resumable session.
func TestPersistFailure_RegistersNothing_PriorReplyIsNotAnArtifact(t *testing.T) {
	dir := t.TempDir()
	h := New(dir, nil)
	if _, err := h.PersistFailure("dev", "a reply that failed"); err != nil {
		t.Fatalf("PersistFailure: %v", err)
	}
	if err := h.SeedPriorReply("dev"); err != nil {
		t.Fatalf("SeedPriorReply: %v", err)
	}
	if _, err := h.Interpolate("{{ artifacts.dev }}"); err == nil {
		t.Error("{{ artifacts.dev }} resolved for a failed node — an artifact means the node PASSED")
	}
	if path, ok := h.ArtifactPath("dev"); ok {
		t.Errorf("ArtifactPath(dev) = %q; a kept failure reply is not an artifact path", path)
	}
}
