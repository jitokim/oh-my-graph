// reader.go is the consumer side of the stream contract this package writes:
// the readers oh-my-graph's own in-repo consumers (`runs list`, `watch`,
// `serve`) share so they can never disagree with each other — or with the
// writer — about what the bytes of events.jsonl mean. Like the writer, it
// imports only the standard library.

package runfeed

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"
)

// maxLineBytes is the longest events.jsonl line any reader in this package
// accepts. The writer emits lines orders of magnitude smaller (an Event is a
// handful of short fields), so a longer line means a corrupt or foreign file,
// and every reader refuses it with an error rather than silently truncating
// (InFlight) or buffering without bound (Follow). One shared constant so the
// readers can never disagree about which streams are readable.
const maxLineBytes = 1 << 20 // 1 MiB

// Walk reads a run's event stream once, oldest event first, and hands each
// decoded event to visit. It is the ONE-SHOT reader — the counterpart to
// Follow's endless tail — for a consumer that wants a settled answer about a
// run right now (is it in flight? what state is each node in? when did it
// start?) rather than a live subscription.
//
// It applies the contract's reading rules exactly once, so no caller has to
// restate them: a line that does not decode is skipped (the only tolerated
// damage is one truncated final line), a line stamped with a schema newer
// than this binary's Schema ends the walk with an error rather than being
// silently misread (RUN-FEED.md's compatibility rule for consumers, the same
// loud refusal runstate.Load gives an incompatible snapshot), and a line
// longer than maxLineBytes is an error too, same as Follow. A visit error
// ends the walk with that error. A stream that does not exist surfaces as an
// error wrapping fs.ErrNotExist, for the caller to translate — the missing
// stream means different things to different consumers.
func Walk(path string, visit func(Event) error) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open event stream %q: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	// Raise the Scanner's default 64 KiB token limit to the shared per-line
	// cap, so Walk and Follow agree on which streams are readable.
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		if event.Schema > Schema {
			return fmt.Errorf("event stream %q: schema %d is newer than this binary understands (max %d)", path, event.Schema, Schema)
		}
		if err := visit(event); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read event stream %q: %w", path, err)
	}
	return nil
}

// InFlight reports whether the run's event stream says it is currently
// executing. Ground truth is the run-feed contract (docs/RUN-FEED.md): the
// stream is a series of legs, each opened by run_started and closed by
// run_finished, so the run is in flight exactly when its last leg is still
// open. A gate pause closes its leg (outcome "paused"), so a paused run is
// not in flight. A missing stream reads as no legs at all — a settled (or
// pre-runfeed) directory, judged by its snapshot alone.
//
// The reading itself is Walk's, so the leg-walking rule lives in exactly one
// place and cannot drift from the other one-shot consumers of the same file.
//
// This is the STREAM's answer and only the stream's: a crashed or killed
// process never writes its run_finished, so by the stream alone its last leg
// reads open forever. There is still no liveness probe here, and that is still
// deliberate — but it is no longer a limitation the tool accepts. ADR 0015
// retired the acceptance by moving the missing half OUT rather than in: the run
// lock answers whether that leg's writer is alive (runstate.ProbeLock), and
// internal/runstatus composes the two facts once for every in-repo surface —
// open leg AND held lock is in flight, open leg AND affirmatively free lock is
// abandoned, every doubt is in flight. So no caller reads this function's
// answer as the whole truth about a run, and this package stays what it is: a
// pure stdlib reader of the two contract files that spawns nothing, probes
// nothing, and knows nothing about locks or processes.
func InFlight(path string) (bool, error) {
	leg, err := LastLeg(path)
	if err != nil {
		return false, err
	}
	return leg.Open, nil
}

// Leg is everything the stream says about a run's LAST leg, read in one walk.
// It exists because since ADR 0023 the composition layer needs three facts off
// this file rather than one — whether the last leg is open, which phase it
// opened in, and how the last one that closed said it ended — and reading the
// stream three times to learn them would put the dashboard's hot path (one
// walk per card per tick) back where ADR 0015 took it out of.
type Leg struct {
	// Open is runfeed.InFlight's own answer: the last leg has not been closed
	// by a run_finished. It says nothing about the process that opened it —
	// that half is the run lock's, composed in internal/runstatus.
	Open bool
	// Started says a run_started has been seen AT ALL, open or since closed. It
	// is the fact that separates a stream which has said nothing from one whose
	// leg is merely over, which is not a status question but the question of
	// whether there is a status to ask for (runstatus.Spoken, ADR 0023 §2.1.1).
	// A lone run_finished does not set it: a close with no open before it is
	// damage, not a leg.
	Started bool
	// Phase is the LAST run_started's Phase: PhasePlanning while the planner
	// call runs, empty for a scheduler leg. The transition between them is
	// affirmative in BOTH directions — "the latest run_started says planning"
	// and "the latest run_started says nothing" — never "no node has started
	// yet", which would paint the first instants of every hand-written run as
	// planning (ADR 0023 §2.3).
	Phase string
	// LastOutcome is the last run_finished's Outcome — "passed", "failed" or
	// "paused". It is meaningful only when Open is false; while a leg is open
	// it is whatever the previous leg said, which no caller reads because an
	// open leg answers first (ADR 0023 §2.1).
	LastOutcome string
}

// LastLeg reads the run's event stream once and reports what it says about the
// last leg. Like InFlight, a missing stream is not an error: it reads as no
// legs at all — a settled (or pre-runfeed) directory. A stream this binary
// refuses to read IS an error, and the caller decides what that means.
func LastLeg(path string) (Leg, error) {
	var leg Leg
	err := Walk(path, func(event Event) error {
		switch event.Type {
		case EventRunStarted:
			leg.Open = true
			leg.Started = true
			leg.Phase = event.Phase
		case EventRunFinished:
			leg.Open = false
			leg.LastOutcome = event.Outcome
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Leg{}, nil
		}
		return Leg{}, err
	}
	return leg, nil
}

// Follow tails the event stream at path: every complete ('\n'-terminated)
// line already on disk is handed to handle immediately, oldest first, then
// the file is followed tail -f style — at end-of-stream the reader waits
// poll and re-reads, so an appended line reaches handle effectively as it
// lands. A complete line is always one whole JSON event (Emit writes line
// and newline in a single write), so the only damage Follow ever buffers
// around is a truncated final line, which is held back until its newline
// arrives (docs/RUN-FEED.md). A line longer than maxLineBytes — which the
// writer never emits — ends the follow with an error rather than buffering
// without bound, the same refusal InFlight gives an over-long line.
//
// handle receives the raw line without its trailing newline and owns all
// interpretation — decoding, schema checks, when to stop. Returning stop
// true ends the follow cleanly (nil); so does ctx being cancelled, which is
// how a consumer with no natural end (a disconnecting viewer, a Ctrl-C)
// stops. A handle error, an unopenable file (fs.ErrNotExist preserved for
// errors.Is — the missing-stream signal `watch` turns into its unknown-run
// error), or a read failure ends it with that error. A consumer that must
// instead outwait a stream that does not exist yet uses FollowWait.
func Follow(ctx context.Context, path string, poll time.Duration, handle func(line []byte) (stop bool, err error)) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open event stream %q: %w", path, err)
	}
	defer file.Close()
	return follow(ctx, file, path, poll, handle)
}

// FollowWait is Follow for a stream that may not exist yet: a fresh run's
// directory can briefly exist before events.jsonl does (and a pre-runfeed
// directory has no stream at all), so FollowWait polls until the file can be
// opened — at the same cadence the follow itself polls at end-of-stream —
// and then tails it exactly as Follow does. ctx being cancelled while
// waiting ends it cleanly (nil), same as during the follow. Callers that
// need a missing stream to be an error (`watch`, where it means a mistyped
// run id) use Follow instead.
func FollowWait(ctx context.Context, path string, poll time.Duration, handle func(line []byte) (stop bool, err error)) error {
	for {
		file, err := os.Open(path)
		if err == nil {
			defer file.Close()
			return follow(ctx, file, path, poll, handle)
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("open event stream %q: %w", path, err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(poll):
		}
	}
}

// follow is the shared tail loop behind Follow and FollowWait, reading an
// already-open file. It reads in bounded chunks (ReadSlice) rather than one
// unbounded ReadBytes so the maxLineBytes cap is enforced while a line is
// still accumulating, not after it has been buffered whole.
func follow(ctx context.Context, file *os.File, path string, poll time.Duration, handle func(line []byte) (stop bool, err error)) error {
	reader := bufio.NewReader(file)
	var pending []byte
	for {
		chunk, err := reader.ReadSlice('\n')
		pending = append(pending, chunk...)

		content := len(pending)
		if content > 0 && pending[content-1] == '\n' {
			content--
		}
		if content > maxLineBytes {
			return fmt.Errorf("read event stream %q: line exceeds %d bytes (corrupt or foreign file)", path, maxLineBytes)
		}

		if err != nil {
			if errors.Is(err, bufio.ErrBufferFull) {
				// The line spans the reader's internal buffer; keep
				// accumulating (bounded by the cap above).
				continue
			}
			if !errors.Is(err, io.EOF) {
				return fmt.Errorf("read event stream %q: %w", path, err)
			}
			// Caught up with the writer (possibly mid-line): wait for more.
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(poll):
			}
			continue
		}

		line := pending[:len(pending)-1]
		pending = nil

		stop, err := handle(line)
		if err != nil {
			return err
		}
		if stop {
			return nil
		}
	}
}
