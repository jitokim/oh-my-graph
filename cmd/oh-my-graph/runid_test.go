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
