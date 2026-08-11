package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jitokim/oh-my-graph/graphs"
)

// initGraphsDir is the directory `init` unpacks into, relative to the target
// directory. The name is fixed rather than a flag because every doc, the
// Quickstart and the Makefile smoke target all say `graphs/<file>.yaml`: the
// point of `init` is that those paths start working, not that they become
// configurable.
const initGraphsDir = "graphs"

// runInit is the `init` subcommand: parse argv (an optional target directory,
// defaulting to the current one) and unpack the embedded example graphs.
func runInit(args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("init: unexpected argument %q (usage: oh-my-graph init [dir])", args[1])
	}
	dir := "."
	if len(args) == 1 {
		dir = args[0]
	}
	return initGraphs(os.Stdout, dir)
}

// initGraphs writes every file embedded in the binary (see package graphs) to
// <dir>/graphs/, creating the directory tree if needed, and prints one line
// per payload file: `wrote` for one it created, `kept` for one that was
// already there. It exists because `go install` ships only an executable:
// this is what turns a bare binary into the working tree the README's first
// command assumes.
//
// The payload is NESTED, not flat: a template that cites `use: <name>`
// (ADR 0013) resolves it against its own fragments/ sibling on disk, so
// unpacking graphs/*.yaml without graphs/fragments/*.yaml would leave the user
// with templates that fail to load. The walk therefore mirrors the embedded
// tree verbatim, and the counts report every file, nested ones included.
//
// It never overwrites, and it is a TOP-UP: a target path that already exists
// is left exactly as it is and named on stdout, while the payload files that
// are missing are written. Re-running `init` is therefore the way a user gets
// a file the binary gained after their first one — `fragments/pr-publish.yaml`
// shipped at v0.5.3, two releases after fragments/ itself, and until this a
// `go install` user who had ever run `init` had NO command that could hand it
// to them: the whole command failed on the first path that existed. Skipping
// cannot produce the half-replaced set that all-or-nothing refusal existed to
// prevent, because it modifies no existing file at all — an edited template is
// kept, said so on stdout, and the shipped copy is the user's to fetch by
// moving theirs aside and re-running.
//
// The writes still use O_EXCL, so a file that appears between the check and
// the write is refused rather than clobbered; because that refusal happens
// mid-loop, a failing write also removes the files this run created — and the
// subdirectories it created for them — so a failed `init` leaves the tree as
// it found it. The listing is buffered for the same reason: it names files
// only once they are all there to stay.
//
// Nothing here spawns a process: unpacking is pure file I/O over bytes already
// linked into the binary.
func initGraphs(w io.Writer, dir string) error {
	names, err := embeddedPaths()
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return fmt.Errorf("init: no graphs are embedded in this binary")
	}
	target := filepath.Join(dir, initGraphsDir)

	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("init: create %s: %w", target, err)
	}
	var listing strings.Builder
	unpacked := &unpackedTree{}
	written, kept := 0, 0
	for _, name := range names {
		path := filepath.Join(target, filepath.FromSlash(name))
		// Existing means kept, never replaced: see the top-up contract above.
		switch _, err := os.Stat(path); {
		case err == nil:
			kept++
			fmt.Fprintf(&listing, "kept  %s (already there — not replaced)\n", path)
			continue
		case !errors.Is(err, fs.ErrNotExist):
			return unpacked.undo(fmt.Errorf("init: check %s: %w", path, err))
		}
		data, err := graphs.FS.ReadFile(name)
		if err != nil {
			return unpacked.undo(fmt.Errorf("init: read embedded graph %q: %w", name, err))
		}
		if err := unpacked.mkdirAll(filepath.Dir(path)); err != nil {
			return unpacked.undo(fmt.Errorf("init: create %s: %w", filepath.Dir(path), err))
		}
		if err := writeNewFile(path, data); err != nil {
			return unpacked.undo(fmt.Errorf("init: write %s: %w", path, err))
		}
		unpacked.files = append(unpacked.files, path)
		written++
		fmt.Fprintf(&listing, "wrote %s\n", path)
	}

	io.WriteString(w, listing.String())
	fmt.Fprintf(w, "%d file(s) written to %s\n", written, target)
	if kept > 0 {
		fmt.Fprintf(w, "%d file(s) were already there and were left untouched — to take the shipped copy of one, move yours aside and run `init` again\n", kept)
	}
	// The cheapest real end-to-end check, quoted from the Quickstart — but only
	// when that graph is part of the payload this binary carries. After a
	// top-up it is on disk either way, written by this run or kept from an
	// earlier one.
	if smoke := filepath.Join(target, smokeGraphFile); payloadCarries(names, smokeGraphFile) {
		fmt.Fprintf(w, "next: mkdir -p /tmp/omg-smoke && oh-my-graph run %s --input dir=/tmp/omg-smoke\n", smoke)
	}
	return nil
}

// embeddedPaths lists every embedded file as a slash-separated path relative
// to the graphs package directory ("haiku-smoke.yaml",
// "fragments/e2e-verify.yaml"), in the walk's lexical order. Directories are
// not entries of their own: they are created on the way to the files inside
// them, so an empty directory can never be unpacked.
func embeddedPaths() ([]string, error) {
	var names []string
	err := fs.WalkDir(graphs.FS, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			names = append(names, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("init: read embedded graphs: %w", err)
	}
	return names, nil
}

// smokeGraphFile is the shipped two-node graph the Quickstart runs; `init`
// points at it as the next step.
const smokeGraphFile = "haiku-smoke.yaml"

// payloadCarries reports whether name is among the embedded paths, so the
// next-step hint cannot name a graph this binary does not carry.
func payloadCarries(names []string, name string) bool {
	for _, candidate := range names {
		if candidate == name {
			return true
		}
	}
	return false
}

// unpackedTree records what this run created — files, and the subdirectories
// created to hold them — so a failure partway through can put the target
// directory back the way it found it. It is the rollback half of the top-up
// contract: what the run keeps it never touched, and what the run wrote it can
// undo, so a failed `init` is indistinguishable from one that never ran. A
// nested payload makes the directories part of that promise: leaving an empty
// graphs/fragments/ behind is still a half-unpacked tree.
type unpackedTree struct {
	files []string
	dirs  []string // creation order; removed in reverse so children go first
}

// mkdirAll creates dir, recording every directory that did not already exist
// so undo can remove exactly those and nothing else — a directory the user
// already had is never a candidate for cleanup.
func (u *unpackedTree) mkdirAll(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	u.dirs = append(u.dirs, dir)
	return nil
}

// undo removes what this run created and returns cause unchanged. Removal
// errors are deliberately dropped — the caller's problem is cause, and
// reporting a failed cleanup instead would hide why `init` stopped. The
// directory removals use os.Remove rather than RemoveAll: a directory that
// somehow gained a file this run did not write is left alone.
func (u *unpackedTree) undo(cause error) error {
	for _, path := range u.files {
		os.Remove(path)
	}
	for i := len(u.dirs) - 1; i >= 0; i-- {
		os.Remove(u.dirs[i])
	}
	return cause
}

// writeNewFile creates path with data, failing if path already exists. The
// O_EXCL is the enforcement half of the no-overwrite promise: the caller's
// per-file check decides written-or-kept, this makes the individual write
// itself incapable of destroying a file — including one that appeared after
// that check looked.
func writeNewFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
