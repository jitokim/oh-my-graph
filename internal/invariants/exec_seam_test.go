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

// skippedDirs are the directory NAMES the repo-root walk does not descend
// into, at any depth. Everything else in the repo is scanned.
//
// The walk is deliberately an inversion: it used to enumerate the trees to
// cover (internal, cmd, graphs), which meant a new top-level Go package was
// invisible to the guard that enforces this project's core security property —
// and the failure mode of a guard that scans nothing is a silent green CI, not
// an error. A skip-list fails the other way: a directory nobody thought to
// exclude is scanned, and the worst case is a false positive somebody has to
// look at.
//
// The entries:
//
//   - dot-directories (.git, .github, .claude) and underscore-prefixed ones are
//     matched by rule below, not listed here — Go's own tooling ignores them
//     as package directories, so nothing compiled can live there.
//   - testdata: excluded from every Go build by the toolchain itself, so a .go
//     file there is never linked into a binary and cannot be a spawner.
//   - vendor: not present today (this repo has no vendored Go modules), but
//     `go mod vendor` would otherwise drop thousands of third-party files into
//     the walk, many of which legitimately import os/exec. Note that
//     internal/serve/ui/vendor holds vendored JAVASCRIPT and no .go files, so
//     skipping it costs nothing either way.
//   - bin: the gitignored build output directory (`make build`).
//
// A botched skip-list cannot fail silently: the stale check in
// TestOnlyTheFourExecSeamsImportOsExec asserts that every allowlisted file was
// FOUND to import os/exec, so a walk that stopped reaching internal/ reports
// all eight allowlisted files stale (measured, by adding "internal" here) and
// goes red rather than passing on an empty scan.
var skippedDirs = map[string]bool{
	"testdata": true,
	"vendor":   true,
	"bin":      true,
}

// skipDir reports whether the walk should skip the directory named name — a
// listed exclusion above, or a dot/underscore directory, which the Go toolchain
// itself never treats as a package directory. The repo root arrives as "." from
// filepath.Rel and must never be skipped by the dot rule.
func skipDir(name string) bool {
	if name == "." || name == ".." {
		return false
	}
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
		return true
	}
	return skippedDirs[name]
}

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

// walkRepoGoFiles parses every non-test .go file in the repository — the whole
// tree from the root down, minus skipDir — and calls visit with each file's
// repo-relative slash-separated path and its parsed AST.
//
// Walking from the root rather than from a list of trees is what keeps this
// guard honest: a new top-level package is covered the day it lands, without
// anyone remembering to enroll it. Test files are excluded because a test may
// legitimately spawn a helper process; the invariant is about production code.
func walkRepoGoFiles(t *testing.T, repoRoot string, visit func(rel string, file *ast.File)) {
	t.Helper()

	fset := token.NewFileSet()
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// A full parse, not parser.ImportsOnly: TestNoDirectProcessSpawns
		// needs the call expressions, and one walk serves both checks.
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		visit(filepath.ToSlash(rel), file)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", repoRoot, err)
	}
}

func TestOnlyTheFourExecSeamsImportOsExec(t *testing.T) {
	repoRoot := filepath.Join("..", "..")

	importers := map[string]bool{}
	walkRepoGoFiles(t, repoRoot, func(rel string, file *ast.File) {
		if _, ok := importLocalName(file, "os/exec"); ok {
			importers[rel] = true
		}
	})

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

// directSpawnSelectors are the package-qualified calls that start a process
// WITHOUT going through os/exec — the bypass the import allowlist above cannot
// see, because a file using one of them need not import os/exec at all.
//
// Keyed by import path so each is matched against the file's own local name for
// that package (an alias, or the path's last segment), never against a bare
// identifier that could belong to something else entirely.
//
// This is a selector scan and deliberately NOT an import allowlist: `syscall`
// is imported by eight non-test files in this repo for signal handling, flock,
// filesystem-type probing and pid probing, none of which spawn anything. An
// import-based check would be noise, and noise that gets muted guards nothing.
// Nothing in the repo calls any of these today; the scan exists so that stays
// true by test rather than by habit.
var directSpawnSelectors = map[string][]string{
	"os":      {"StartProcess"},
	"syscall": {"ForkExec", "StartProcess", "Exec", "CreateProcess"},
}

// TestNoDirectProcessSpawns closes the gap under TestOnlyTheFourExecSeamsImportOsExec:
// that test proves only four files import os/exec, but os/exec is not the only
// way to spawn. os.StartProcess and syscall.ForkExec are the same capability one
// layer down, and a file calling them would sail past an import allowlist.
//
// The rule is absolute rather than allowlisted: even the four seams route their
// spawns through os/exec (which is what TestExecSeamCallSitesScrubEnv can then
// verify scrubs the child env). A direct fork/exec anywhere — seam or not — would
// be a spawn with no scrub check over it, so there is nobody to exempt.
func TestNoDirectProcessSpawns(t *testing.T) {
	repoRoot := filepath.Join("..", "..")

	type finding struct{ file, call string }
	var found []finding

	walkRepoGoFiles(t, repoRoot, func(rel string, file *ast.File) {
		for _, call := range directSpawnsIn(file) {
			found = append(found, finding{rel, call})
		}
	})

	sort.Slice(found, func(i, j int) bool {
		if found[i].file != found[j].file {
			return found[i].file < found[j].file
		}
		return found[i].call < found[j].call
	})
	for _, f := range found {
		t.Errorf("%s calls %s, which spawns a process without going through os/exec — so "+
			"the import allowlist in TestOnlyTheFourExecSeamsImportOsExec cannot see it and "+
			"TestExecSeamCallSitesScrubEnv cannot prove its child environment is scrubbed. "+
			"Route the spawn through one of the four exec seams, or write the ADR for a new "+
			"one (docs/adr/0002, 0005, 0006).", f.file, f.call)
	}
}

// directSpawnsIn reports every directSpawnSelectors call file makes, as
// "<import path>.<name>". Each selector is matched against the file's OWN local
// name for that package, so an aliased — or dot — import is followed rather
// than assumed.
//
// TestNoDirectProcessSpawns runs this over the repo; TestDirectSpawnScanFollowsTheImport
// runs it over sources that deliberately do spawn, which is the only way to
// prove the scan would see one, since a green repo scan is also what a scan
// that matches nothing produces.
func directSpawnsIn(file *ast.File) []string {
	var found []string
	for importPath, names := range directSpawnSelectors {
		local, ok := importLocalName(file, importPath)
		if !ok {
			continue
		}
		for _, name := range names {
			ast.Inspect(file, func(n ast.Node) bool {
				call, isCall := n.(*ast.CallExpr)
				if !isCall {
					return true
				}
				if isPkgCall(call, local, name) {
					found = append(found, importPath+"."+name)
				}
				return true
			})
		}
	}
	return found
}

// isPkgCall reports whether call is `<pkg>.<name>(...)`.
//
// pkg is the file's local name for the import, which importLocalName reports as
// "." for a dot import. A dot import puts the package's exported names into the
// file's own scope, so `os.StartProcess(...)` is written `StartProcess(...)`:
// there is no selector to match, and matching only selectors would let a dot
// import walk straight past the direct-spawn scan.
func isPkgCall(call *ast.CallExpr, pkg, name string) bool {
	if pkg == "." {
		id, ok := call.Fun.(*ast.Ident)
		return ok && id.Name == name
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

// TestDirectSpawnScanFollowsTheImport feeds the scan sources that DO spawn, so
// that TestNoDirectProcessSpawns' green means "nothing spawns" rather than "the
// scan matches nothing". The dot-import cases are the ones that motivated it:
// a bare `StartProcess(...)` under `import . "os"` is the same capability with
// no selector on it.
func TestDirectSpawnScanFollowsTheImport(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "dot-imported os.StartProcess",
			src: `package p
import . "os"
func f() { StartProcess("/bin/sh", nil, nil) }`,
			want: []string{"os.StartProcess"},
		},
		{
			name: "dot-imported syscall.ForkExec",
			src: `package p
import . "syscall"
func f() { ForkExec("/bin/sh", nil, nil) }`,
			want: []string{"syscall.ForkExec"},
		},
		{
			name: "aliased syscall.Exec",
			src: `package p
import sys "syscall"
func f() { sys.Exec("/bin/sh", nil, nil) }`,
			want: []string{"syscall.Exec"},
		},
		{
			name: "plain os.StartProcess",
			src: `package p
import "os"
func f() { os.StartProcess("/bin/sh", nil, nil) }`,
			want: []string{"os.StartProcess"},
		},
		{
			name: "a dot import that spawns nothing",
			src: `package p
import . "os"
func f() { Getenv("HOME") }`,
			want: nil,
		},
		{
			name: "a same-named call with no such import",
			src: `package p
func StartProcess(string) {}
func f() { StartProcess("/bin/sh") }`,
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "probe.go", tc.src, 0)
			if err != nil {
				t.Fatalf("parse probe source: %v", err)
			}
			got := directSpawnsIn(file)
			sort.Strings(got)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("directSpawnsIn = %v, want %v", got, tc.want)
			}
		})
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
//   - (b) the function enclosing that call assigns
//     <recv>.Env = childenv.Scrub(...) on the SAME *exec.Cmd receiver the
//     constructor produced (`cmd := exec.CommandContext(...)` → `cmd.Env = …`),
//     and does so BEFORE that receiver is executed (.Run/.Start/.Output/
//     .CombinedOutput) or returned. This is the assignment that keeps the child
//     on subscription billing instead of a silent fallback to the metered API;
//     pinning both the receiver AND the order stops a `.Env =` on an unrelated
//     variable, or one placed after the process already ran, from passing.
//
// The receiver identifier differs per seam (each file names its own local), so
// the test discovers it from the constructor rather than hard-coding a name.
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

			// (b) that function assigns <recv>.Env = childenv.Scrub(...) on the
			// SAME *exec.Cmd receiver the constructor produced, and does so
			// BEFORE that receiver is executed or returned. A `.Env =` on an
			// unrelated variable, or one placed after the process already ran,
			// must not satisfy this — pinning the receiver and the order is the
			// whole point of the strengthened check.
			fn := spawnFuncs[0]

			// Identify the receiver: the single identifier the sole
			// exec.Command/exec.CommandContext call is assigned to.
			recvName, constructPos, ok := execConstructReceiver(fn, execName)
			if !ok {
				t.Fatalf("%s: %s calls exec.Command/exec.CommandContext but not as the right-hand "+
					"side of a simple `x := exec.Command(...)` assignment to a single identifier, so "+
					"the test cannot pin the *exec.Cmd receiver whose .Env must be scrubbed. Assign "+
					"the constructor to one variable and set <that>.Env = %s.Scrub(...) on it before "+
					"running or returning it.", rel, fn.Name.Name, childenvName)
			}

			// Find the scrub assignment on that same receiver.
			var scrubPos token.Pos
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if assign, ok := n.(*ast.AssignStmt); ok && isScrubEnvAssign(assign, recvName, childenvName) {
					scrubPos = assign.Pos()
				}
				return true
			})
			if !scrubPos.IsValid() {
				t.Errorf("%s: %s constructs *exec.Cmd %q but never assigns %s.Env = %s.Scrub(...) on "+
					"that same receiver. Every exec seam's call site MUST set the scrubbed child "+
					"environment on the constructed command before the process runs, or "+
					"ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN leak into the child and it silently "+
					"falls back to metered API billing — the one invariant childenv.Scrub exists to "+
					"hold (see CLAUDE.md, docs/adr/0002, 0005, 0006).", rel, fn.Name.Name, recvName, recvName, childenvName)
				return
			}

			// Ordering: the scrub must run on the already-built *exec.Cmd …
			if scrubPos <= constructPos {
				t.Errorf("%s: %s assigns %s.Env = %s.Scrub(...) at or before it constructs %q with "+
					"exec.Command/exec.CommandContext; the scrub must run on the already-built "+
					"*exec.Cmd. Move the scrub assignment below the constructor.", rel, fn.Name.Name, recvName, childenvName, recvName)
			}

			// … and before that receiver is first executed
			// (.Run/.Start/.Output/.CombinedOutput) or returned. If the scrub
			// lands after that, the process has already run — or left the
			// function to a caller that will run it — with the parent's
			// unscrubbed environment.
			if usePos, used := firstReceiverExecOrReturn(fn, recvName); used && scrubPos >= usePos {
				t.Errorf("%s: %s assigns %s.Env = %s.Scrub(...) only AFTER it runs or returns %q; by "+
					"then the child has already been spawned (or handed to a caller that will spawn "+
					"it) with the parent's unscrubbed environment. Set the scrubbed env before the "+
					"first .Run()/.Start()/.Output()/.CombinedOutput() call on %q or before returning "+
					"it.", rel, fn.Name.Name, recvName, childenvName, recvName, recvName)
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
// on the receiver identifier named recv — the call site's assignment of the
// scrubbed child environment onto the *exec.Cmd the constructor produced. It
// pins BOTH the `.Env` selector's receiver (so a scrub written onto some
// unrelated *exec.Cmd variable does not count) and the childenv.Scrub call on
// the right.
func isScrubEnvAssign(assign *ast.AssignStmt, recv, childenvName string) bool {
	if len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
		return false
	}
	lhs, ok := assign.Lhs[0].(*ast.SelectorExpr)
	if !ok || lhs.Sel.Name != "Env" {
		return false
	}
	recvIdent, ok := lhs.X.(*ast.Ident)
	if !ok || recvIdent.Name != recv {
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

// execConstructReceiver finds the `<ident> := <execName>.Command(...)` (or `=`)
// statement inside fn and returns the receiver identifier's name and the
// position of that construction. ok is false when the sole exec.Command call is
// NOT the right-hand side of a simple assignment to a single identifier — an
// inline argument, a bare expression statement, a multi-name or multi-value
// assignment — because then there is no single receiver whose .Env the test can
// pin the scrub to, and passing silently would defeat the check.
func execConstructReceiver(fn *ast.FuncDecl, execName string) (name string, pos token.Pos, ok bool) {
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		assign, isAssign := n.(*ast.AssignStmt)
		if !isAssign || len(assign.Rhs) != 1 {
			return true
		}
		call, isCall := assign.Rhs[0].(*ast.CallExpr)
		if !isCall || !isExecCommandCall(call, execName) {
			return true
		}
		if len(assign.Lhs) == 1 {
			if id, isIdent := assign.Lhs[0].(*ast.Ident); isIdent {
				name, pos, ok = id.Name, assign.Pos(), true
			}
		}
		return true
	})
	return name, pos, ok
}

// firstReceiverExecOrReturn returns the earliest position inside fn at which the
// receiver named recv is either executed (a .Run/.Start/.Output/.CombinedOutput
// call on it) or returned. used is false when the receiver is neither run nor
// returned inside fn. The four real seam builders return the *exec.Cmd for a
// sibling function to run, so the return statement is the ordering boundary; a
// caller that inlined the run would hit the method-call boundary instead.
func firstReceiverExecOrReturn(fn *ast.FuncDecl, recv string) (pos token.Pos, used bool) {
	record := func(p token.Pos) {
		if !used || p < pos {
			pos, used = p, true
		}
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			if isReceiverRunCall(node, recv) {
				record(node.Pos())
			}
		case *ast.ReturnStmt:
			for _, res := range node.Results {
				if id, ok := res.(*ast.Ident); ok && id.Name == recv {
					record(node.Pos())
				}
			}
		}
		return true
	})
	return pos, used
}

// isReceiverRunCall reports whether call executes the *exec.Cmd named recv — one
// of recv.Run(), recv.Start(), recv.Output() or recv.CombinedOutput(), the four
// os/exec methods that actually spawn the process.
func isReceiverRunCall(call *ast.CallExpr, recv string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || id.Name != recv {
		return false
	}
	switch sel.Sel.Name {
	case "Run", "Start", "Output", "CombinedOutput":
		return true
	}
	return false
}
