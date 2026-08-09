package handoff

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

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
	// The number, not just the code's agreement with itself: every other
	// assertion below derives its input and its ceiling from the constant, so a
	// hundredfold cap would satisfy all of them while quietly letting one
	// runaway node fill a user's disk. 256 KiB is what the CHANGELOG and ADR
	// 0020 §3 publish.
	if maxFailedReplyBytes != 256*1024 {
		t.Fatalf("maxFailedReplyBytes = %d, want 256 KiB — the cap on what one node's reply may cost a "+
			"run directory is published; moving it means moving ADR 0020 §3 and the CHANGELOG with it",
			maxFailedReplyBytes)
	}
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

// TestPersistFailure_AnnouncesExactlyWhatItDropped: the marker states a figure,
// so the figure has to be the truth. Measuring it against the CAP rather than
// against the cut reports the marker's own length as though it were reply text
// that survived — an under-report in the one file whose stated purpose is never
// to present a cut reply as whole.
func TestPersistFailure_AnnouncesExactlyWhatItDropped(t *testing.T) {
	dir := t.TempDir()
	h := New(dir, nil)
	reply := strings.Repeat("x", 4*maxFailedReplyBytes)

	path, err := h.PersistFailure("windy", reply)
	if err != nil {
		t.Fatalf("PersistFailure: %v", err)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed reply not persisted: %v", err)
	}

	m := regexp.MustCompile(`excerpted (\d+) bytes here`).FindStringSubmatch(string(onDisk))
	if m == nil {
		t.Fatalf("the file names no figure for what it dropped: %q", string(onDisk[:min(len(onDisk), 400)]))
	}
	announced, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("unreadable figure %q: %v", m[1], err)
	}
	marker := fmt.Sprintf(failedExcerptMarker, announced, maxFailedReplyBytes)
	if !strings.Contains(string(onDisk), marker) {
		t.Fatalf("the marker is not the one this file writes: %q", m[0])
	}
	if dropped := len(reply) - (len(onDisk) - len(marker)); announced != dropped {
		t.Errorf("the file says it dropped %d bytes of the reply; it dropped %d", announced, dropped)
	}
}

// TestPersistFailure_CutLandsOnRuneBoundaries: a reply is text, and a cut at an
// arbitrary byte offset lands inside a multi-byte rune. Half a rune is invalid
// UTF-8 — mojibake at exactly the seam a reader looks at first, in a file whose
// whole job is to be read. The three offsets shift the rune grid against the
// fixed cut so one of them must land mid-rune.
func TestPersistFailure_CutLandsOnRuneBoundaries(t *testing.T) {
	for shift := 0; shift < 3; shift++ {
		reply := strings.Repeat("x", shift) + strings.Repeat("긴 답장을 남긴 노드 ", 20000)
		if len(reply) <= maxFailedReplyBytes {
			t.Fatalf("this test needs a reply over the cap; got %d bytes", len(reply))
		}
		dir := t.TempDir()
		h := New(dir, nil)

		path, err := h.PersistFailure("windy", reply)
		if err != nil {
			t.Fatalf("PersistFailure: %v", err)
		}
		onDisk, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed reply not persisted: %v", err)
		}
		if !utf8.Valid(onDisk) {
			t.Errorf("shift %d: the cut left invalid UTF-8 on disk", shift)
		}
		if len(onDisk) > maxFailedReplyBytes {
			t.Errorf("shift %d: persisted %d bytes, over the %d-byte cap", shift, len(onDisk), maxFailedReplyBytes)
		}
	}
}

// TestDropFailure removes the claim once it stops being true: a node that
// failed in an earlier leg and PASSED in this one must not leave the losing
// reply beside the winning artifact. A node that never failed is the common
// case and must not be an error.
func TestDropFailure(t *testing.T) {
	dir := t.TempDir()
	h := New(dir, nil)
	if _, err := h.PersistFailure("dev", "LEG-ONE-WRONG-ANSWER"); err != nil {
		t.Fatalf("PersistFailure: %v", err)
	}
	if err := h.DropFailure("dev"); err != nil {
		t.Fatalf("DropFailure: %v", err)
	}
	if _, err := os.Stat(FailedOutputPath(dir, "dev")); !os.IsNotExist(err) {
		t.Errorf("the stale failed reply survived (stat err = %v)", err)
	}
	if err := h.DropFailure("never-failed"); err != nil {
		t.Errorf("DropFailure on a node that never failed = %v, want nil", err)
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

// --- handing the reply back to the leg that repeats the attempt (ADR 0020) ---

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
	seeded, err := next.SeedPriorReply("dev")
	if err != nil {
		t.Fatalf("SeedPriorReply: %v", err)
	}
	if !seeded {
		t.Error("SeedPriorReply reported nothing seeded for a reply it did seed; that bool is how a " +
			"caller tells a retry carrying its previous attempt from one silently without it")
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
	if _, err := h.SeedPriorReply("dev"); err != nil {
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
// about nothing — running the retry without a quote is the pre-ADR-0020
// behaviour, which is correct rather than degraded.
//
// It is nonetheless REPORTABLE: seeded comes back false, so a caller that knew
// a reply was owed can say so instead of handing the node a silently degraded
// retry that looks exactly like a whole one.
func TestSeedPriorReply_MissingFileIsACleanNoOp(t *testing.T) {
	h := New(t.TempDir(), nil)
	seeded, err := h.SeedPriorReply("never-ran")
	if err != nil {
		t.Fatalf("SeedPriorReply on a missing file = %v, want nil", err)
	}
	if seeded {
		t.Error("SeedPriorReply on a missing file reported a reply seeded; a nil error alone cannot " +
			"tell the caller which of the two it got")
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
	seeded, err := New(dir, nil).SeedPriorReply("dev")
	if err == nil {
		t.Fatal("SeedPriorReply on an unreadable path = nil, want the failure reported")
	}
	if seeded {
		t.Error("SeedPriorReply reported a reply seeded alongside a read failure")
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
	if _, err := h.SeedPriorReply("dev"); err != nil {
		t.Fatalf("SeedPriorReply: %v", err)
	}
	if _, err := h.Interpolate("{{ artifacts.dev }}"); err == nil {
		t.Error("{{ artifacts.dev }} resolved for a failed node — an artifact means the node PASSED")
	}
	if path, ok := h.ArtifactPath("dev"); ok {
		t.Errorf("ArtifactPath(dev) = %q; a kept failure reply is not an artifact path", path)
	}
}
