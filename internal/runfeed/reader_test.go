package runfeed

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testPoll keeps the tail's end-of-stream sleep short so follow tests finish
// in milliseconds rather than at a production interval.
const testPoll = 2 * time.Millisecond

// startFollow runs fn (Follow or FollowWait) against path in a goroutine and
// returns a channel of delivered lines plus a channel carrying fn's return
// value. cancel tears the follow down; the caller must drain done.
func startFollow(t *testing.T, fn func(ctx context.Context, path string, poll time.Duration, handle func([]byte) (bool, error)) error, path string) (lines chan string, done chan error, cancel context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	lines = make(chan string, 64)
	done = make(chan error, 1)
	go func() {
		done <- fn(ctx, path, testPoll, func(line []byte) (bool, error) {
			lines <- string(line)
			return false, nil
		})
	}()
	return lines, done, cancel
}

// expectLine asserts the next delivered line equals want, within a deadline.
func expectLine(t *testing.T, lines chan string, want string) {
	t.Helper()
	select {
	case got := <-lines:
		if got != want {
			t.Fatalf("delivered line = %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for line %q", want)
	}
}

// expectDone asserts the follow returns, and hands back its error.
func expectDone(t *testing.T, done chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the follow to return")
		return nil
	}
}

// TestFollow_HoldsBackATruncatedFinalLine pins the contract's one tolerated
// damage: a final line without its newline (a crash mid-write, or a writer
// mid-append) is held back — never delivered as a partial event — until its
// newline arrives, at which point it is delivered whole.
func TestFollow_HoldsBackATruncatedFinalLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	const whole = `{"schema":2,"event":"run_started"}`
	const sentinel = `{"schema":2,"event":"node_passed","node_id":"sentinel"}`
	const partHead = `{"schema":2,"ev`
	const partTail = `ent":"node_started"}`
	if err := os.WriteFile(path, []byte(whole+"\n"+sentinel+"\n"+partHead), 0o644); err != nil {
		t.Fatalf("write fixture stream: %v", err)
	}

	lines, done, cancel := startFollow(t, Follow, path)
	defer func() { cancel(); expectDone(t, done) }()

	expectLine(t, lines, whole)

	// Positive hold-back proof: the sentinel is the complete line scanned
	// immediately before the truncated tail, so its arrival — while the
	// truncated line still has not arrived — shows the tail reached the
	// damage and held it back. The old sleep-then-assert-nothing shape only
	// proved the reader was slower than the sleep, which under CI load it
	// often was not.
	expectLine(t, lines, sentinel)
	select {
	case got := <-lines:
		t.Fatalf("truncated line was delivered as %q, want it held back", got)
	default:
	}

	// Its newline arrives: the completed line is delivered whole.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("reopen fixture stream: %v", err)
	}
	if _, err := f.Write([]byte(partTail + "\n")); err != nil {
		t.Fatalf("complete the truncated line: %v", err)
	}
	f.Close()
	expectLine(t, lines, partHead+partTail)
}

// TestFollow_CtxCancelAtEOFReturnsNil pins that cancellation is the clean,
// error-free way for a consumer with no natural end (a disconnecting viewer,
// a Ctrl-C) to stop a follow that is parked at end-of-stream.
func TestFollow_CtxCancelAtEOFReturnsNil(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	const line = `{"schema":2,"event":"run_started"}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatalf("write fixture stream: %v", err)
	}

	lines, done, cancel := startFollow(t, Follow, path)
	expectLine(t, lines, line) // parked at EOF now
	cancel()
	if err := expectDone(t, done); err != nil {
		t.Fatalf("a cancelled follow must return nil, got %v", err)
	}
}

// TestFollow_MissingStreamIsErrNotExist pins Follow's missing-file contract:
// fs.ErrNotExist is preserved for errors.Is, because `watch` turns exactly
// that into its unknown-run error — the reason watch uses Follow and not
// FollowWait.
func TestFollow_MissingStreamIsErrNotExist(t *testing.T) {
	err := Follow(context.Background(), filepath.Join(t.TempDir(), FileName), testPoll, func([]byte) (bool, error) {
		t.Fatal("handle must never be called for a missing stream")
		return true, nil
	})
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want one satisfying errors.Is(err, fs.ErrNotExist)", err)
	}
}

// TestReaders_ShareOneLineCap pins that InFlight and Follow enforce the same
// maximum line length: a line over the shared cap is refused by both (never
// one reader succeeding where the other fails), and a long-but-legal line —
// over bufio.Scanner's 64 KiB default, which InFlight once inherited — is
// read by both.
func TestReaders_ShareOneLineCap(t *testing.T) {
	writeStream := func(t *testing.T, detail string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), FileName)
		lines := fmt.Sprintf(`{"schema":2,"event":"run_started"}`+"\n"+
			`{"schema":2,"event":"node_passed","node_id":"a","detail":%q}`+"\n", detail)
		if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
			t.Fatalf("write fixture stream: %v", err)
		}
		return path
	}

	t.Run("a line over the cap is refused by both readers", func(t *testing.T) {
		path := writeStream(t, strings.Repeat("x", maxLineBytes+1024))

		if _, err := InFlight(path); err == nil {
			t.Error("InFlight accepted an over-cap line, want an error")
		}
		err := Follow(context.Background(), path, testPoll, func([]byte) (bool, error) { return false, nil })
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Errorf("Follow err = %v, want a line-exceeds-cap error", err)
		}
	})

	t.Run("a long line under the cap is read by both readers", func(t *testing.T) {
		path := writeStream(t, strings.Repeat("x", 100*1024)) // > the 64 KiB Scanner default

		open, err := InFlight(path)
		if err != nil {
			t.Fatalf("InFlight returned error: %v", err)
		}
		if !open {
			t.Error("InFlight = false, want true — the long line must not eat the open leg")
		}

		var got []string
		err = Follow(context.Background(), path, testPoll, func(line []byte) (bool, error) {
			got = append(got, string(line))
			return len(got) == 2, nil
		})
		if err != nil {
			t.Fatalf("Follow returned error: %v", err)
		}
		if len(got) != 2 || len(got[1]) < 100*1024 {
			t.Errorf("Follow delivered %d lines (last %d bytes), want the long line intact", len(got), len(got[len(got)-1]))
		}
	})
}

// TestFollowWait_DeliversAStreamCreatedAfterItStarted pins the wait-for-create
// behavior serve's SSE endpoint relies on: a follow opened before events.jsonl
// exists parks, then delivers the stream when it appears.
func TestFollowWait_DeliversAStreamCreatedAfterItStarted(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)

	// Positive missing-signal, per CONTRIBUTING's test-double rule (no
	// wall-clock arm): before delegating to FollowWait, the follow goroutine
	// itself proves the stream does not exist — Follow's documented
	// fs.ErrNotExist — and only that observation, not a sleep, releases the
	// writer below to create the stream.
	observedMissing := make(chan error, 1)
	followWaitAfterProvenMissing := func(ctx context.Context, p string, poll time.Duration, handle func([]byte) (bool, error)) error {
		err := Follow(ctx, p, poll, handle)
		observedMissing <- err
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return FollowWait(ctx, p, poll, handle)
	}

	lines, done, cancel := startFollow(t, followWaitAfterProvenMissing, path)
	defer func() { cancel(); expectDone(t, done) }()

	select {
	case err := <-observedMissing:
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("pre-wait probe err = %v, want fs.ErrNotExist for a not-yet-created stream", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the follower to observe the stream missing")
	}

	w, err := NewStreamWriter(path, "run-1")
	if err != nil {
		t.Fatalf("NewStreamWriter: %v", err)
	}
	defer w.Close()
	if err := w.Emit(Event{Type: EventRunStarted}); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	select {
	case got := <-lines:
		if !strings.Contains(got, string(EventRunStarted)) {
			t.Errorf("delivered line = %q, want the run_started event", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the created stream's first line")
	}
}

// TestFollowWait_CtxCancelWhileWaitingReturnsNil pins that a viewer who
// disconnects while the stream still does not exist ends the wait cleanly.
func TestFollowWait_CtxCancelWhileWaitingReturnsNil(t *testing.T) {
	lines, done, cancel := startFollow(t, FollowWait, filepath.Join(t.TempDir(), FileName))
	cancel()
	if err := expectDone(t, done); err != nil {
		t.Fatalf("a cancelled wait must return nil, got %v", err)
	}
	select {
	case got := <-lines:
		t.Fatalf("a never-created stream delivered %q", got)
	default:
	}
}
