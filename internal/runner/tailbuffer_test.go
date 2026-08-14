package runner

import (
	"bytes"
	"strings"
	"testing"
)

// A node's stderr is collected to read at most maxStderrInError bytes of it, so
// collecting it whole is unbounded growth for a fixed need: the writer keeps a
// constant amount no matter how much a chatty CLI writes.
func TestTailBufferRetainsOnlyTheTailUnderFloodOfWrites(t *testing.T) {
	b := tailBuffer{limit: 64}
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

// A single write larger than the whole window must be truncated on its own,
// not appended and left to a later trim that may never come.
func TestTailBufferTruncatesOneOversizedWrite(t *testing.T) {
	b := tailBuffer{limit: 16}
	b.Write([]byte(strings.Repeat("x", 4096) + "TAIL"))

	if got := string(b.Bytes()); got != "xxxxxxxxxxxxTAIL" {
		t.Errorf("tail = %q, want the last 16 bytes of the write", got)
	}
	if got := len(b.buf); got != 16 {
		t.Errorf("held %d bytes internally, want exactly the limit", got)
	}
}

// Under the limit nothing is dropped and Write reports the full length it was
// handed, as io.Writer requires.
func TestTailBufferKeepsShortOutputWhole(t *testing.T) {
	b := tailBuffer{limit: 64}
	n, err := b.Write([]byte("panic: boom\n"))
	if err != nil || n != 12 {
		t.Fatalf("Write = (%d, %v), want (12, nil)", n, err)
	}
	if got := string(b.Bytes()); got != "panic: boom\n" {
		t.Errorf("Bytes = %q, want the whole short write", got)
	}
}

// The bound is what a node's stderr actually gets: the tail the runner keeps is
// wide enough to hold everything any consumer reads (maxStderrInError).
func TestStderrRetentionExceedsWhatConsumersRead(t *testing.T) {
	if maxStderrRetained <= maxStderrInError {
		t.Fatalf("retention %d must exceed the %d bytes consumers read", maxStderrRetained, maxStderrInError)
	}
}
