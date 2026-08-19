package main

import (
	"strings"
	"testing"
)

// TestNewRunID_UniqueWithinASecond guards the run-id uniqueness fix: run ids
// double as run directory names, so two ids minted in the same second used to
// collide and clobber each other's artifacts. Rapid successive calls must all
// differ — the nanosecond timestamp and the per-process sequence suffix
// guarantee it even on a platform with a coarse wall clock.
func TestNewRunID_UniqueWithinASecond(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := newRunID()
		if seen[id] {
			t.Fatalf("newRunID returned a duplicate id %q on call %d", id, i)
		}
		seen[id] = true
	}
}

// TestNewRunID_FilesystemSafe keeps the id usable as a single directory name
// under runsRoot: no path separators, no path traversal, nothing hidden.
func TestNewRunID_FilesystemSafe(t *testing.T) {
	id := newRunID()
	if strings.ContainsAny(id, `/\`) {
		t.Fatalf("run id %q contains a path separator", id)
	}
	if strings.HasPrefix(id, ".") || strings.Contains(id, "..") {
		t.Fatalf("run id %q is not a plain visible directory name", id)
	}
}

// TestNewRunID_NeverBeginsWithDash pins the fact #200's fix rests on: the
// timestamp format always opens with a digit, so an argv element beginning
// with "-" standing in a positional slot is never a legitimate run id and can
// always be read as a flag instead (see argslot.go). Minted across many calls
// — spanning the per-process sequence suffix rollover, not just one id — so a
// future change to the format cannot silently drop the leading digit and
// leave that rule unsound.
func TestNewRunID_NeverBeginsWithDash(t *testing.T) {
	for i := 0; i < 1000; i++ {
		if id := newRunID(); strings.HasPrefix(id, "-") {
			t.Fatalf("newRunID returned %q, which begins with \"-\" on call %d — argslot.go's positional-slot rule depends on this never happening", id, i)
		}
	}
}
