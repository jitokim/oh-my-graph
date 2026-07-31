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

// InFlight reports whether the run's event stream says it is currently
// executing. Ground truth is the run-feed contract (docs/RUN-FEED.md): the
// stream is a series of legs, each opened by run_started and closed by
// run_finished, so the run is in flight exactly when its last leg is still
// open. A gate pause closes its leg (outcome "paused"), so a paused run is
// not in flight. A missing stream reads as no legs at all — a settled (or
// pre-runfeed) directory, judged by its snapshot alone.
//
// Lines are decoded into the Event shape the stream is written with; a line
// that does not decode is skipped, because the contract's only tolerated
// damage is one truncated final line. A line that DOES decode but is stamped
// with a schema newer than this binary's Schema is surfaced as an error
// rather than silently misread — RUN-FEED.md's compatibility rule for
// consumers, and the same loud refusal runstate.Load gives an incompatible
// snapshot. Known limitation, accepted for v1: a crashed or killed process
// leaves its last leg open, so by the stream alone such a run reads as in
// flight until it is resumed or its directory is cleaned up — there is no
// liveness probe here, deliberately, to keep every caller a pure reader of
// the two contract files.
func InFlight(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("open event stream %q: %w", path, err)
	}
	defer file.Close()

	open := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		if event.Schema > Schema {
			return false, fmt.Errorf("event stream %q: schema %d is newer than this binary understands (max %d)", path, event.Schema, Schema)
		}
		switch event.Type {
		case EventRunStarted:
			open = true
		case EventRunFinished:
			open = false
		}
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("read event stream %q: %w", path, err)
	}
	return open, nil
}

// Follow tails the event stream at path: every complete ('\n'-terminated)
// line already on disk is handed to handle immediately, oldest first, then
// the file is followed tail -f style — at end-of-stream the reader waits
// poll and re-reads, so an appended line reaches handle effectively as it
// lands. A complete line is always one whole JSON event (Emit writes line
// and newline in a single write), so the only damage Follow ever buffers
// around is a truncated final line, which is held back until its newline
// arrives (docs/RUN-FEED.md).
//
// handle receives the raw line without its trailing newline and owns all
// interpretation — decoding, schema checks, when to stop. Returning stop
// true ends the follow cleanly (nil); so does ctx being cancelled, which is
// how a consumer with no natural end (a disconnecting viewer, a Ctrl-C)
// stops. A handle error, an unopenable file (fs.ErrNotExist preserved for
// errors.Is), or a read failure ends it with that error.
func Follow(ctx context.Context, path string, poll time.Duration, handle func(line []byte) (stop bool, err error)) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open event stream %q: %w", path, err)
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	var pending []byte
	for {
		chunk, err := reader.ReadBytes('\n')
		pending = append(pending, chunk...)
		if err != nil {
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
