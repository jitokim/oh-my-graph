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

// initGraphs writes every graph embedded in the binary (see package graphs)
// to <dir>/graphs/, creating the directory if needed, and prints one line per
// file written. It exists because `go install` ships only an executable: this
// is what turns a bare binary into the working tree the README's first command
// assumes.
//
// It never overwrites. If any target file already exists the whole command
// fails naming that path and nothing at all is written — the existence sweep
// runs to completion before the first byte lands, so a user who runs `init`
// twice, or into a directory holding their own edited copies, cannot end up
// with a half-replaced set. The writes themselves use O_EXCL so a file that
// appears between the sweep and the write is still refused rather than
// clobbered; because that refusal happens mid-loop, a failing write also
// removes the files this run already created, so "nothing at all is written"
// holds for the whole command and not just for the sweep. The listing is
// buffered for the same reason: it names files only once they are all there
// to stay.
//
// Nothing here spawns a process: unpacking is pure file I/O over bytes already
// linked into the binary.
func initGraphs(w io.Writer, dir string) error {
	entries, err := fs.ReadDir(graphs.FS, ".")
	if err != nil {
		return fmt.Errorf("init: read embedded graphs: %w", err)
	}
	if len(entries) == 0 {
		return fmt.Errorf("init: no graphs are embedded in this binary")
	}
	target := filepath.Join(dir, initGraphsDir)

	// Refuse first, write second: see the all-or-nothing contract above.
	for _, entry := range entries {
		path := filepath.Join(target, entry.Name())
		switch _, err := os.Stat(path); {
		case err == nil:
			return fmt.Errorf("init: %s already exists — nothing was written (move or delete it, or run `oh-my-graph init <dir>` somewhere else)", path)
		case !errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("init: check %s: %w", path, err)
		}
	}

	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("init: create %s: %w", target, err)
	}
	var listing strings.Builder
	written := make([]string, 0, len(entries))
	for _, entry := range entries {
		data, err := graphs.FS.ReadFile(entry.Name())
		if err != nil {
			return undoWrites(written, fmt.Errorf("init: read embedded graph %q: %w", entry.Name(), err))
		}
		path := filepath.Join(target, entry.Name())
		if err := writeNewFile(path, data); err != nil {
			return undoWrites(written, fmt.Errorf("init: write %s: %w", path, err))
		}
		written = append(written, path)
		fmt.Fprintf(&listing, "wrote %s\n", path)
	}

	io.WriteString(w, listing.String())
	fmt.Fprintf(w, "%d graph(s) written to %s\n", len(entries), target)
	// The cheapest real end-to-end check, quoted from the Quickstart — but only
	// when that graph is actually part of what was just written.
	if smoke := filepath.Join(target, smokeGraphFile); fileWasWritten(entries, smokeGraphFile) {
		fmt.Fprintf(w, "next: mkdir -p /tmp/omg-smoke && oh-my-graph run %s --input dir=/tmp/omg-smoke\n", smoke)
	}
	return nil
}

// smokeGraphFile is the shipped two-node graph the Quickstart runs; `init`
// points at it as the next step.
const smokeGraphFile = "haiku-smoke.yaml"

// fileWasWritten reports whether name is among the embedded entries, so the
// next-step hint cannot name a graph this binary does not carry.
func fileWasWritten(entries []fs.DirEntry, name string) bool {
	for _, entry := range entries {
		if entry.Name() == name {
			return true
		}
	}
	return false
}

// undoWrites removes the files this run created and returns cause unchanged.
// It is the rollback half of the all-or-nothing contract: the pre-flight sweep
// cannot see a file that appears while the loop is running, so the loop has to
// be able to put the directory back the way it found it. Removal errors are
// deliberately dropped — the caller's problem is cause, and reporting a failed
// cleanup instead would hide why `init` stopped.
func undoWrites(written []string, cause error) error {
	for _, path := range written {
		os.Remove(path)
	}
	return cause
}

// writeNewFile creates path with data, failing if path already exists. The
// O_EXCL is the enforcement half of the no-overwrite promise: the caller's
// pre-flight sweep decides all-or-nothing, this makes the individual write
// itself incapable of destroying a file.
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
