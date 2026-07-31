// Package invariants holds repo-wide structural tests. This one enforces the
// "exactly four exec seams" rule from ADR 0002, ADR 0005 and ADR 0006: only
// runner.ClaudeCLIRunner, verify.ShellVerifier, worktree.GitManager and
// browser.ExecOpener may spawn processes, so only their files may import
// os/exec. A new importer means a new spawner, which needs its own ADR before
// it lands.
package invariants

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// allowedExecImporters is the exhaustive list of non-test files (relative to
// the repo root, slash-separated) that may import os/exec. Each entry belongs
// to one of the four exec seams; the platform-specific procgroup files are
// part of their seam's process-spawning machinery.
//
// To add an entry here, first write an ADR introducing the new seam
// (see docs/adr/0002, docs/adr/0005 and docs/adr/0006).
var allowedExecImporters = map[string]bool{
	// Seam 1: runner.ClaudeCLIRunner (ADR 0001/0002)
	"internal/runner/claude.go":            true,
	"internal/runner/procgroup_unix.go":    true,
	"internal/runner/procgroup_windows.go": true,
	// Seam 2: verify.ShellVerifier (ADR 0002)
	"internal/verify/shell.go":             true,
	"internal/verify/procgroup_unix.go":    true,
	"internal/verify/procgroup_windows.go": true,
	// Seam 3: worktree.GitManager (ADR 0005)
	"internal/worktree/git.go": true,
	// Seam 4: browser.ExecOpener (ADR 0006) — the build-tagged argv files
	// only pick the launcher command; exec.go alone imports os/exec.
	"internal/browser/exec.go": true,
}

// scannedDirs are the source trees the invariant covers: all production code
// lives under these two roots.
var scannedDirs = []string{"internal", "cmd"}

func TestOnlyTheFourExecSeamsImportOsExec(t *testing.T) {
	repoRoot := filepath.Join("..", "..")

	importers := map[string]bool{}
	fset := token.NewFileSet()
	for _, dir := range scannedDirs {
		root := filepath.Join(repoRoot, dir)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range file.Imports {
				importPath, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					return err
				}
				if importPath == "os/exec" {
					rel, err := filepath.Rel(repoRoot, path)
					if err != nil {
						return err
					}
					importers[filepath.ToSlash(rel)] = true
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}

	var unexpected, stale []string
	for file := range importers {
		if !allowedExecImporters[file] {
			unexpected = append(unexpected, file)
		}
	}
	for file := range allowedExecImporters {
		if !importers[file] {
			stale = append(stale, file)
		}
	}
	sort.Strings(unexpected)
	sort.Strings(stale)

	for _, file := range unexpected {
		t.Errorf("%s imports os/exec but is not one of the four exec seams "+
			"(runner.ClaudeCLIRunner, verify.ShellVerifier, worktree.GitManager, "+
			"browser.ExecOpener). A fifth spawner needs its own ADR — see "+
			"docs/adr/0002, docs/adr/0005 and docs/adr/0006. Depend on the "+
			"NodeRunner/Verifier/worktree.Provider/browser.Opener interfaces "+
			"instead, or write the ADR and extend allowedExecImporters.", file)
	}
	for _, file := range stale {
		t.Errorf("%s is in allowedExecImporters but no longer imports os/exec; "+
			"remove the stale entry so the allowlist stays exact.", file)
	}
}
