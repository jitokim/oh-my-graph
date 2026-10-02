//go:build unix && !aix && !solaris

package conventions

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestRead_FIFOSwappedAfterStatIsRefusedWithoutHanging is #296: a path that
// statOne validated as a regular file, then swapped for a FIFO no one writes,
// must be refused at once. A blocking open would wait for a writer forever and
// hold the launch before any node starts; the test finishing is half the
// assertion.
func TestRead_FIFOSwappedAfterStatIsRefusedWithoutHanging(t *testing.T) {
	path := writeFile(t, t.TempDir(), "style.md", "use tabs\n")
	f, err := statOne(path)
	if err != nil {
		t.Fatalf("statOne: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("this filesystem cannot host a FIFO: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- f.read() }()
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("read blocked on a FIFO with no writer; the open must not wait for one")
	}
	var refusal *RefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("want *RefusalError, got %T: %v", err, err)
	}
	if msg := err.Error(); !strings.Contains(msg, "style.md") || !strings.Contains(msg, "no longer a regular file") {
		t.Errorf("message does not name the path and the reason: %s", msg)
	}
}
