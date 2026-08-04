// Package invariants holds repo-wide structural tests. This one enforces the
// "exactly four exec seams" rule from ADR 0002, ADR 0005 and ADR 0006: only
// runner.ClaudeCLIRunner, verify.ShellVerifier, worktree.GitManager and
// browser.ExecOpener may spawn processes, so only their files may import
// os/exec. A new importer means a new spawner, which needs its own ADR before
// it lands.
package invariants

import (
	"go/ast"
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
// lives under these roots. `graphs` is one of them — it holds the //go:embed
// declaration for the shipped example graphs, and a file added there would
// otherwise be outside this walk.
var scannedDirs = []string{"internal", "cmd", "graphs"}

// childenvImportPath is this repo's shared child-env scrub package — the one
// TestExecSeamCallSitesScrubEnv requires each seam's call site to route its
// child environment through (cmd.Env = childenv.Scrub(...)).
const childenvImportPath = "github.com/jitokim/oh-my-graph/internal/childenv"

// execSeamCallSites are the files that actually CONSTRUCT and spawn a process —
// exactly one per exec seam. Unlike allowedExecImporters this list deliberately
// EXCLUDES the platform-specific procgroup files: those import os/exec only to
// mutate an already-built *exec.Cmd (SysProcAttr, Process.Kill), they never call
// exec.Command/exec.CommandContext themselves, so they have no spawn to scrub.
// Keep this in step with the four seams in allowedExecImporters.
var execSeamCallSites = []string{
	"internal/runner/claude.go", // Seam 1: runner.ClaudeCLIRunner (ADR 0001/0002)
	"internal/verify/shell.go",  // Seam 2: verify.ShellVerifier (ADR 0002)
	"internal/worktree/git.go",  // Seam 3: worktree.GitManager (ADR 0005)
	"internal/browser/exec.go",  // Seam 4: browser.ExecOpener (ADR 0006)
}

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

// TestExecSeamCallSitesScrubEnv closes the defense-in-depth gap that the import
// allowlist above cannot: TestOnlyTheFourExecSeamsImportOsExec guards WHICH
// files may spawn a process, but not that they ACTUALLY scrub the child env at
// the call site. A future edit could add a second, unscrubbed exec.Command to an
// already-allowlisted file and CI would stay green.
//
// For each of the four spawn-site files this asserts, structurally:
//
//   - (a) the file has EXACTLY ONE exec.Command/exec.CommandContext call — the
//     one env-scrubbed constructor per seam, so a second (unaudited) spawn is a
//     red test; and
//   - (b) the function enclosing that call also assigns
//     cmd.Env = childenv.Scrub(...), the assignment that keeps the child on
//     subscription billing instead of a silent fallback to the metered API.
//
// It matches the `.Env` selector and a call to childenv.Scrub — never a
// particular variable name, since the *exec.Cmd receiver differs per seam.
func TestExecSeamCallSitesScrubEnv(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	fset := token.NewFileSet()

	for _, rel := range execSeamCallSites {
		t.Run(rel, func(t *testing.T) {
			// Full AST this time — the checks live in the function bodies, not
			// the import block, so parser.ImportsOnly will not do.
			file, err := parser.ParseFile(fset, filepath.Join(repoRoot, filepath.FromSlash(rel)), nil, 0)
			if err != nil {
				t.Fatalf("parsing %s: %v", rel, err)
			}

			execName, ok := importLocalName(file, "os/exec")
			if !ok {
				t.Fatalf("%s is an exec-seam call site but does not import os/exec.", rel)
			}
			childenvName, ok := importLocalName(file, childenvImportPath)
			if !ok {
				t.Fatalf("%s is an exec-seam call site but does not import %s — its call "+
					"site cannot be scrubbing the child env through childenv.Scrub.", rel, childenvImportPath)
			}

			// (a) exactly one exec.Command/exec.CommandContext in the whole file.
			totalCalls := 0
			ast.Inspect(file, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok && isExecCommandCall(call, execName) {
					totalCalls++
				}
				return true
			})
			if totalCalls != 1 {
				t.Fatalf("%s has %d exec.Command/exec.CommandContext call sites, want exactly 1. "+
					"Each exec seam funnels every spawn through a single env-scrubbed *exec.Cmd "+
					"constructor; a second call site is a second, unaudited spawn that the import "+
					"allowlist cannot catch. Route it through the existing builder, or write an ADR "+
					"for a new seam (docs/adr/0002, 0005, 0006).", rel, totalCalls)
			}

			// Locate the function enclosing that single call site.
			var spawnFuncs []*ast.FuncDecl
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				encloses := false
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					if call, ok := n.(*ast.CallExpr); ok && isExecCommandCall(call, execName) {
						encloses = true
					}
					return true
				})
				if encloses {
					spawnFuncs = append(spawnFuncs, fn)
				}
			}
			if len(spawnFuncs) != 1 {
				t.Fatalf("%s: the exec.Command/exec.CommandContext call site is not inside exactly "+
					"one function (found %d enclosing functions); cannot verify it scrubs the child env.",
					rel, len(spawnFuncs))
			}

			// (b) that function assigns cmd.Env = childenv.Scrub(...).
			fn := spawnFuncs[0]
			scrubbed := false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if assign, ok := n.(*ast.AssignStmt); ok && isScrubEnvAssign(assign, childenvName) {
					scrubbed = true
				}
				return true
			})
			if !scrubbed {
				t.Errorf("%s: %s constructs an *exec.Cmd but never assigns cmd.Env = %s.Scrub(...). "+
					"Every exec seam's call site MUST set the scrubbed child environment before the "+
					"process runs, or ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN leak into the child and "+
					"it silently falls back to metered API billing — the one invariant childenv.Scrub "+
					"exists to hold (see CLAUDE.md, docs/adr/0002, 0005, 0006).", rel, fn.Name.Name, childenvName)
			}
		})
	}
}

// importLocalName returns the identifier a file uses to refer to importPath and
// whether it imports it at all. An explicit alias wins; otherwise Go binds the
// package's own name, which for every import here is the path's last segment
// (os/exec -> exec, .../internal/childenv -> childenv).
func importLocalName(file *ast.File, importPath string) (string, bool) {
	for _, imp := range file.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p != importPath {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name, true
		}
		if i := strings.LastIndex(p, "/"); i >= 0 {
			return p[i+1:], true
		}
		return p, true
	}
	return "", false
}

// isExecCommandCall reports whether call is `<execName>.Command(...)` or
// `<execName>.CommandContext(...)` — the two os/exec entry points that build a
// spawnable *exec.Cmd. execName is the file's local name for the os/exec import.
func isExecCommandCall(call *ast.CallExpr, execName string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != execName {
		return false
	}
	return sel.Sel.Name == "Command" || sel.Sel.Name == "CommandContext"
}

// isScrubEnvAssign reports whether assign is `<recv>.Env = <childenvName>.Scrub(...)`
// — the call site's assignment of the scrubbed child environment. It matches on
// the `.Env` selector on the left and a call to childenv.Scrub on the right, not
// on any particular variable name, since the *exec.Cmd receiver differs per seam.
func isScrubEnvAssign(assign *ast.AssignStmt, childenvName string) bool {
	if len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
		return false
	}
	lhs, ok := assign.Lhs[0].(*ast.SelectorExpr)
	if !ok || lhs.Sel.Name != "Env" {
		return false
	}
	call, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok {
		return false
	}
	scrub, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || scrub.Sel.Name != "Scrub" {
		return false
	}
	pkg, ok := scrub.X.(*ast.Ident)
	return ok && pkg.Name == childenvName
}
