package runstate

import "slices"

// WorkingTree is the invocation repository's working tree as a leg left it
// (#455): every path `git status --porcelain` listed — tracked files
// modified, added, deleted or renamed, and untracked files not ignored —
// when the leg ended. It is a report for the operator: nothing reads it to
// decide a verdict, a node status or an exit code.
type WorkingTree struct {
	// Changed is how many paths are uncommitted: len(Paths).
	Changed int `json:"changed"`
	// Paths are the uncommitted paths, relative to the repository root,
	// sorted.
	Paths []string `json:"paths"`
}

// NewWorkingTree is the record of paths, sorted, or nil for none: a clean
// tree records nothing. paths is copied.
func NewWorkingTree(paths []string) *WorkingTree {
	if len(paths) == 0 {
		return nil
	}
	sorted := slices.Sorted(slices.Values(paths))
	return &WorkingTree{Changed: len(sorted), Paths: sorted}
}

func (w *WorkingTree) clone() *WorkingTree {
	if w == nil {
		return nil
	}
	return &WorkingTree{Changed: w.Changed, Paths: slices.Clone(w.Paths)}
}
