package runstate

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// --- a second acquire while the lock is held is refused -----------------------

// TestAcquireLock_SecondAcquireInSameProcessFails proves that acquiring an
// already-held lock path is refused with a *LockHeldError naming the path.
// Both acquires happen in this one test process, so what is exercised is the
// O_EXCL create-refusal on an existing file — the cross-process guarantee (two
// `resume` invocations racing on the same run id) rests on the kernel making
// that O_EXCL create atomic, which no same-process test can demonstrate and
// this suite deliberately does not spawn a second process to try.
func TestAcquireLock_SecondAcquireInSameProcessFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume.lock")

	release, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("first AcquireLock: %v", err)
	}
	defer release()

	_, err = AcquireLock(path)
	var held *LockHeldError
	if !errors.As(err, &held) {
		t.Fatalf("second AcquireLock = %T: %v, want *LockHeldError", err, err)
	}
	if held.Path != path {
		t.Fatalf("LockHeldError.Path = %q, want %q", held.Path, path)
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error should name the exact lock path to delete: %v", err)
	}
}

// --- release frees the path for a subsequent acquire -------------------------

func TestAcquireLock_ReleaseAllowsReacquire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume.lock")

	release, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("first AcquireLock: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}

	release2, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	defer release2()
}

// --- the lock file carries the holding pid -----------------------------------

func TestAcquireLock_FileContainsPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume.lock")
	release, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	defer release()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
	got, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("lock file content %q is not a pid: %v", data, err)
	}
	if got != os.Getpid() {
		t.Fatalf("lock file pid = %d, want %d", got, os.Getpid())
	}
}

// --- AcquireLock creates its parent directory --------------------------------

func TestAcquireLock_CreatesParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".oh-my-graph", "runs", "run-1", "resume.lock")
	release, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock into a missing directory tree: %v", err)
	}
	defer release()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lock file not created: %v", err)
	}
}
