package runner

import (
	"bytes"
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// A node's stderr is collected to read at most maxStderrInError bytes of it, so
// collecting it whole is unbounded growth for a fixed need: the writer keeps a
// constant amount no matter how much a chatty CLI writes.
func TestTailBufferRetainsOnlyTheTailUnderFloodOfWrites(t *testing.T) {
	b := newTailBuffer(64)
	for i := 0; i < 10_000; i++ {
		if _, err := b.Write([]byte("noise line that a chatty CLI keeps writing\n")); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	b.Write([]byte("fatal: the last line is the one that matters"))

	if got := len(b.Bytes()); got > 64 {
		t.Errorf("retained %d bytes, want at most 64", got)
	}
	if got := len(b.buf); got > 128 {
		t.Errorf("held %d bytes internally after 420 KiB of writes, want at most 2*limit", got)
	}
	if !bytes.HasSuffix(b.Bytes(), []byte("the one that matters")) {
		t.Errorf("tail = %q, want the last bytes written", b.Bytes())
	}
}

// The window is EXACTLY the last limit bytes — asserted by equality, on every
// write, across the bulk trim. An upper bound plus a suffix check is not enough:
// a buffer that kept a quarter of the window would satisfy both while silently
// throwing away three quarters of the context a human debugging a crash was
// promised. This is the only test that pins how much survives rather than how
// little.
func TestTailBufferKeepsExactlyTheLastLimitBytesAcrossATrim(t *testing.T) {
	const limit = 16
	b := newTailBuffer(limit)

	var written []byte
	for i := 0; i < 30; i++ {
		// Five bytes at a time: every write stays UNDER the limit, so this
		// drives the accumulate-then-trim path an oversized single write never
		// reaches — and it crosses 2*limit repeatedly.
		chunk := []byte(fmt.Sprintf("%04d/", i))
		if _, err := b.Write(chunk); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		written = append(written, chunk...)

		want := written
		if len(want) > limit {
			want = want[len(want)-limit:]
		}
		if got := b.Bytes(); !bytes.Equal(got, want) {
			t.Fatalf("after %d write(s) (%d bytes total): Bytes = %q, want exactly the last %d bytes %q",
				i+1, len(written), got, limit, want)
		}
		if len(b.buf) > 2*limit {
			t.Fatalf("after %d write(s): held %d bytes internally, want at most 2*limit", i+1, 2*limit)
		}
	}
}

// A single write larger than the whole window must be truncated on its own,
// not appended and left to a later trim that may never come.
func TestTailBufferTruncatesOneOversizedWrite(t *testing.T) {
	b := newTailBuffer(16)
	b.Write([]byte(strings.Repeat("x", 4096) + "TAIL"))

	if got := string(b.Bytes()); got != "xxxxxxxxxxxxTAIL" {
		t.Errorf("tail = %q, want the last 16 bytes of the write", got)
	}
	if got := len(b.buf); got != 16 {
		t.Errorf("held %d bytes internally, want exactly the limit", got)
	}
}

// The oversized path REPLACES what was already retained; it does not append to
// it. Starting from empty cannot tell the two apart — both leave the same
// Bytes() — so only the internal length after a write onto existing content
// pins it. An appending variant grows without bound under a CLI that emits one
// huge line per second, which is the exact failure this buffer exists to stop.
func TestTailBufferOversizedWriteReplacesExistingContent(t *testing.T) {
	const limit = 16
	b := newTailBuffer(limit)
	b.Write([]byte("earlier"))

	b.Write([]byte(strings.Repeat("x", 4096) + "TAIL"))

	if got := string(b.Bytes()); got != "xxxxxxxxxxxxTAIL" {
		t.Errorf("tail = %q, want the last 16 bytes of the oversized write", got)
	}
	if got := len(b.buf); got != limit {
		t.Errorf("held %d bytes internally, want exactly the limit — the oversized write must replace, not append", got)
	}
	if bytes.Contains(b.buf, []byte("earlier")) {
		t.Errorf("the superseded content is still held: %q", b.buf)
	}
}

// The branch boundary: a write of exactly limit bytes is the oversized path
// (n >= limit), so it replaces. Bytes() reads identically either way, which is
// why the internal length is what tells a `>=` from a `>`.
func TestTailBufferWriteOfExactlyTheLimitReplaces(t *testing.T) {
	const limit = 8
	b := newTailBuffer(limit)
	b.Write([]byte("earlier"))

	n, err := b.Write([]byte("12345678"))
	if err != nil || n != limit {
		t.Fatalf("Write = (%d, %v), want (%d, nil)", n, err, limit)
	}
	if got := string(b.Bytes()); got != "12345678" {
		t.Errorf("tail = %q, want the whole exactly-limit write", got)
	}
	if got := len(b.buf); got != limit {
		t.Errorf("held %d bytes internally, want exactly the limit — a write of exactly limit bytes takes the replacing path", got)
	}
}

// Under the limit nothing is dropped and Write reports the full length it was
// handed, as io.Writer requires.
func TestTailBufferKeepsShortOutputWhole(t *testing.T) {
	b := newTailBuffer(64)
	n, err := b.Write([]byte("panic: boom\n"))
	if err != nil || n != 12 {
		t.Fatalf("Write = (%d, %v), want (12, nil)", n, err)
	}
	if got := string(b.Bytes()); got != "panic: boom\n" {
		t.Errorf("Bytes = %q, want the whole short write", got)
	}
}

// os/exec hands a writer whatever a read returned, empty reads included. An
// empty write is a no-op that still reports (0, nil): it must neither error nor
// disturb the tail already retained.
func TestTailBufferEmptyWriteIsANoOp(t *testing.T) {
	b := newTailBuffer(64)
	b.Write([]byte("panic: boom\n"))

	n, err := b.Write(nil)
	if err != nil || n != 0 {
		t.Fatalf("Write(nil) = (%d, %v), want (0, nil)", n, err)
	}
	if n, err := b.Write([]byte{}); err != nil || n != 0 {
		t.Fatalf("Write(empty) = (%d, %v), want (0, nil)", n, err)
	}
	if got := string(b.Bytes()); got != "panic: boom\n" {
		t.Errorf("Bytes = %q, want the tail untouched by an empty write", got)
	}
}

// The window cannot be forgotten. A zero limit makes every write oversized and
// therefore discarded whole, so a `tailBuffer{}` that compiles would lose a
// node's entire stderr silently — the constructor makes that unrepresentable
// the way MaxCycles has no unbounded spelling.
func TestNewTailBufferRejectsAWindowlessBuffer(t *testing.T) {
	for _, limit := range []int{0, -1} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("newTailBuffer(%d) returned a buffer that would discard every write", limit)
				}
			}()
			newTailBuffer(limit)
		}()
	}
}

// End to end through the real exec seam: a node that floods stderr far past the
// retention window and then dies must still hand its consumer a BOUNDED failure
// cause containing the line that matters — the last one. This is the property
// the retention constant exists for; comparing that constant against
// maxStderrInError only restates how the two were declared.
func TestRun_FloodedStderrYieldsABoundedFailureCause(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shebang script; this pins the unix path")
	}
	stub := writeStub(t, `#!/bin/sh
echo "HEAD-MARKER-that-must-not-survive-the-flood" >&2
head -c 200000 /dev/zero | tr '\0' 'x' >&2
echo "" >&2
echo "fatal: the last line is the one that matters" >&2
printf '%s\n' '{"session_id":"s-flood","result":"done","total_cost_usd":0.01}'
exit 3
`)
	r := NewCLIRunner(RuntimeClaude, WithBinary(stub))
	outcome, err := r.Run(context.Background(), NodeInvocation{Prompt: testPrompt, PermissionMode: "dontAsk"})
	if err != nil {
		t.Fatalf("a flooding node with a parseable envelope is an outcome, not a Run error: %v", err)
	}
	if outcome.ExitCode != 3 {
		t.Fatalf("exit code = %d, want 3", outcome.ExitCode)
	}
	if !strings.HasSuffix(outcome.FailureCause, "fatal: the last line is the one that matters") {
		t.Errorf("FailureCause = %q, want it to end with the node's fatal line", outcome.FailureCause)
	}
	// 200 KB in, at most what a consumer reads out (plus tailOf's marker).
	if got, limit := len(outcome.FailureCause), maxStderrInError+64; got > limit {
		t.Errorf("FailureCause is %d bytes after 200 KB of stderr, want at most %d", got, limit)
	}
	if strings.Contains(outcome.FailureCause, "HEAD-MARKER") {
		t.Errorf("the head of a 200 KB stderr reached the cause; the tail, not the head, is what survives: %q", outcome.FailureCause)
	}
}
