//go:build unix

package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestLintGraph_ReadsThePathOnlyOnce pins graph.LintLoadFile's read-once
// contract at the boundary that motivated it. `lint` used to read the path
// TWICE — once to collect issues, once more to obtain the graph the advisory
// sweeps run over — and two reads are equivalent to one only for a seekable
// file. Served through a pipe (`lint <(...)`, /dev/stdin, a named FIFO) the
// second read came back EMPTY, decoded to an empty graph that has no
// advisories to give, and the command printed `valid` at exit 0 with the
// advice silently gone. Issues survived, because they came from the first
// read; only the advice — the whole point of the feedback-reach sweep — was
// lost.
//
// A FIFO is the observation: exactly one writer opens it, so a second read
// cannot be served. A regression therefore BLOCKS rather than returning
// wrong, which is why lintGraph runs under a deadline here — the hang is the
// failure signal, and the assertion on the advisory catches any other way of
// losing it.
func TestLintGraph_ReadsThePathOnlyOnce(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "graph.yaml")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("this filesystem cannot host a FIFO, so the read-once property is unobservable here: %v", err)
	}
	// The writer reports its own failures: an open, write or close that fails
	// would surface as a deadline or a YAML error, and reading those as the
	// read-once verdict would be a lie about what the test observed.
	written := make(chan error, 1)
	go func() {
		w, err := os.OpenFile(fifo, os.O_WRONLY, 0)
		if err != nil {
			written <- err
			return
		}
		_, err = io.WriteString(w, fanInFeedbackYAML)
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		written <- err
	}()

	var out, warnings strings.Builder
	done := make(chan error, 1)
	go func() { done <- lintGraph(&out, &warnings, fifo) }()

	select {
	case err := <-done:
		select {
		case werr := <-written:
			if werr != nil {
				t.Fatalf("the FIFO writer failed, so this run says nothing about read-once: %v", werr)
			}
		case <-time.After(time.Second):
			t.Fatal("the FIFO writer never finished, so this run says nothing about read-once")
		}
		if err != nil {
			t.Fatalf("a valid graph served over a FIFO must lint clean: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("lintGraph never returned: it read the path more than once, and the second read is waiting on a writer that will never come")
	}

	if !strings.Contains(out.String(), "valid") {
		t.Errorf("graph should lint as valid:\n%s", out.String())
	}
	if !strings.Contains(warnings.String(), `node "review": feedback:`) {
		t.Errorf("the feedback-reach advisory was dropped — the sweep ran over a graph that was not the one linted:\n%s", warnings.String())
	}
}
