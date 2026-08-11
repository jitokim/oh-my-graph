package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/graphs"
	"github.com/jitokim/oh-my-graph/internal/graph"
)

// embeddedGraphNames is the set `init` is expected to unpack: whatever the
// binary actually carries, as slash-separated paths relative to the payload
// root ("haiku-smoke.yaml", "fragments/e2e-verify.yaml"). Reading it from the
// embed FS rather than a hardcoded list is deliberate — the embed patterns are
// globs, so a template or fragment added to graphs/ must be covered by these
// tests without anyone remembering to edit them. Walking rather than
// ReadDir'ing the root is equally deliberate: `//go:embed *.yaml` does not
// descend, and a test that only ever looked one level deep is how a shipped
// fragments/ directory went missing from the binary once.
func embeddedGraphNames(t *testing.T) []string {
	t.Helper()
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
		t.Fatalf("read embedded graphs: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("no graphs are embedded — the //go:embed patterns in graphs/embed.go matched nothing")
	}
	return names
}

// embeddedFragmentNames is the fragments/ half of the payload — the files a
// migrated template's `use:` resolves against (ADR 0013).
func embeddedFragmentNames(t *testing.T) []string {
	t.Helper()
	var fragments []string
	for _, name := range embeddedGraphNames(t) {
		if strings.HasPrefix(name, "fragments/") {
			fragments = append(fragments, name)
		}
	}
	return fragments
}

// remainingTree lists every path under root — directories included — relative
// to root and slash-separated. Directories count because a nested payload
// makes them part of the rollback promise: an empty graphs/fragments/ left
// behind is still a half-unpacked tree.
func remainingTree(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel != "." {
			found = append(found, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return found
}

// pathAndAncestors expands a payload-relative path into itself plus every
// directory above it, so a test that pre-creates one blocked path knows the
// full set of paths that legitimately survive a rolled-back failure.
func pathAndAncestors(name string) []string {
	parts := strings.Split(name, "/")
	paths := make([]string, 0, len(parts))
	for i := 1; i <= len(parts); i++ {
		paths = append(paths, strings.Join(parts[:i], "/"))
	}
	return paths
}

// --- the fresh-directory case ------------------------------------------------

// TestInitGraphs_WritesEveryEmbeddedGraph is the whole point of the
// subcommand: after `go install` the user has a binary and nothing else, so
// `init` must leave a graphs/ directory on disk whose contents are byte-for-
// byte what the binary carries. Without this, README's first command names a
// file that only exists in a repo checkout.
func TestInitGraphs_WritesEveryEmbeddedGraph(t *testing.T) {
	dir := t.TempDir()
	var out strings.Builder
	if err := initGraphs(&out, dir); err != nil {
		t.Fatalf("init into a fresh directory failed: %v", err)
	}

	for _, name := range embeddedGraphNames(t) {
		path := filepath.Join(dir, "graphs", filepath.FromSlash(name))
		got, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("init did not write %s: %v", name, err)
			continue
		}
		want, err := graphs.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read embedded %s: %v", name, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s on disk differs from the embedded copy", name)
		}
	}
}

// TestInitGraphs_CreatesTheGraphsDirectory pins that a target directory that
// does not exist yet is created rather than reported as an error — `init` is
// the first command a new user runs, so it cannot require them to mkdir first.
func TestInitGraphs_CreatesTheGraphsDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "brand", "new")
	if err := initGraphs(io.Discard, dir); err != nil {
		t.Fatalf("init into a missing directory failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "graphs")); err != nil {
		t.Errorf("graphs/ should have been created: %v", err)
	}
}

// TestInitGraphs_ListsEveryFileItWrote pins the output contract: the user is
// told exactly which paths appeared, one per line, plus a count. A silent
// unpack would leave them guessing what `init` did to their directory.
func TestInitGraphs_ListsEveryFileItWrote(t *testing.T) {
	dir := t.TempDir()
	var out strings.Builder
	if err := initGraphs(&out, dir); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	names := embeddedGraphNames(t)
	for _, name := range names {
		want := "wrote " + filepath.Join(dir, "graphs", filepath.FromSlash(name))
		if !strings.Contains(out.String(), want) {
			t.Errorf("listing should contain %q:\n%s", want, out.String())
		}
	}
	if !strings.Contains(out.String(), fmt.Sprintf("%d file(s) written to %s", len(names), filepath.Join(dir, "graphs"))) {
		t.Errorf("listing should end with a summary naming the target dir:\n%s", out.String())
	}
	// The next step is the Quickstart's own smoke command, so a user who runs
	// `init` never has to go back to the README to find the second command.
	if !strings.Contains(out.String(), "oh-my-graph run "+filepath.Join(dir, "graphs", "haiku-smoke.yaml")) {
		t.Errorf("listing should point at the smoke graph as the next step:\n%s", out.String())
	}
}

// TestInitGraphs_EveryUnpackedTemplateLoads is the end-to-end promise `init`
// actually makes: not "files appeared" but "the graphs a `go install` user now
// has can be run". It loads each unpacked template through graph.LoadFile —
// the same path `run` uses — from the unpacked directory itself, so a template
// citing `use: <fragment>` (ADR 0013) has to find its fragments/ sibling in
// what `init` wrote, not in this repo's checkout.
//
// This is the test whose absence let `//go:embed *.yaml` ship two templates
// that died at load with "no fragment file at the fragment location": every
// other init test compares the unpacked bytes against the embedded bytes, and
// both were equally missing the fragments.
func TestInitGraphs_EveryUnpackedTemplateLoads(t *testing.T) {
	dir := t.TempDir()
	if err := initGraphs(io.Discard, dir); err != nil {
		t.Fatalf("init into a fresh directory failed: %v", err)
	}
	target := filepath.Join(dir, "graphs")

	loaded := 0
	for _, name := range embeddedGraphNames(t) {
		if strings.Contains(name, "/") {
			continue // a fragment is a node definition, not a graph — it loads through the template that cites it
		}
		if _, err := graph.LoadFile(filepath.Join(target, name)); err != nil {
			t.Errorf("unpacked template %s does not load: %v", name, err)
			continue
		}
		loaded++
	}
	if loaded == 0 {
		t.Fatal("no templates were loaded — the assertion is satisfiable by an empty payload")
	}
}

// TestInitGraphs_UnpacksTheFragmentsDirectory pins the nested half of the
// payload directly, so the diagnosis is one line rather than a load error two
// tests away: `init` must leave graphs/fragments/ on disk, byte-for-byte what
// the binary carries.
func TestInitGraphs_UnpacksTheFragmentsDirectory(t *testing.T) {
	fragments := embeddedFragmentNames(t)
	if len(fragments) == 0 {
		t.Fatal("no fragments are embedded — the fragments/*.yaml pattern in graphs/embed.go matched nothing")
	}
	dir := t.TempDir()
	if err := initGraphs(io.Discard, dir); err != nil {
		t.Fatalf("init into a fresh directory failed: %v", err)
	}

	for _, name := range fragments {
		got, err := os.ReadFile(filepath.Join(dir, "graphs", filepath.FromSlash(name)))
		if err != nil {
			t.Errorf("init did not write %s: %v", name, err)
			continue
		}
		want, err := graphs.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read embedded %s: %v", name, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s on disk differs from the embedded copy", name)
		}
	}
}

// --- the occupied-path case ----------------------------------------------------

// TestInitGraphs_KeepsEveryExistingFile is the destructive-mistake guard: a
// user who edited a shipped template and re-runs `init` (or runs it in a repo
// checkout) must keep their file, and be told it was kept. The assertion
// checks the surviving bytes, not just the exit, because "it succeeded" is
// equally satisfiable by a command that restored the shipped copy.
func TestInitGraphs_KeepsEveryExistingFile(t *testing.T) {
	cases := []struct {
		name     string
		occupied string
	}{
		{name: "first file taken", occupied: embeddedGraphNames(t)[0]},
		{name: "last file taken", occupied: embeddedGraphNames(t)[len(embeddedGraphNames(t))-1]},
		{name: "smoke graph taken", occupied: "haiku-smoke.yaml"},
		// A nested payload path: the check must judge fragments/ too, or a
		// user's edited fragment is silently restored to the shipped one.
		{name: "fragment taken", occupied: embeddedFragmentNames(t)[0]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "graphs")
			occupied := filepath.Join(target, filepath.FromSlash(tc.occupied))
			if err := os.MkdirAll(filepath.Dir(occupied), 0o755); err != nil {
				t.Fatalf("prepare target dir: %v", err)
			}
			const mine = "name: mine\nnodes:\n  - { id: a, prompt: a }\n"
			if err := os.WriteFile(occupied, []byte(mine), 0o644); err != nil {
				t.Fatalf("pre-create %s: %v", tc.occupied, err)
			}

			var out strings.Builder
			if err := initGraphs(&out, dir); err != nil {
				t.Fatalf("init over an existing file should top up, not fail: %v", err)
			}

			survived, readErr := os.ReadFile(occupied)
			if readErr != nil {
				t.Fatalf("the pre-existing file disappeared: %v", readErr)
			}
			if string(survived) != mine {
				t.Errorf("the pre-existing file was overwritten:\n%s", survived)
			}
			// Losing an edit is impossible, but a user must never have to
			// DIFF to find out which of their files `init` left alone.
			if !strings.Contains(out.String(), "kept  "+occupied) {
				t.Errorf("listing should name %q as kept:\n%s", occupied, out.String())
			}
		})
	}
}

// TestInitGraphs_TopsUpWhatIsMissing is the other half, and the reason the
// refusal became a top-up: one occupied path must not stop the payload files
// that are NOT there from landing. Without this, a user who ran `init` once
// could never receive a fragment the binary gained later — which is exactly
// what happened to graphs/fragments/pr-publish.yaml at v0.5.3 (ADR 0013,
// update of 2026-08-12).
func TestInitGraphs_TopsUpWhatIsMissing(t *testing.T) {
	names := embeddedGraphNames(t)
	if len(names) < 2 {
		t.Skip("needs at least two embedded graphs to distinguish a top-up from a refusal")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "graphs")
	// Occupy the FIRST path in walk order: a command that still refused on
	// sight of an existing file would stop before reaching any other.
	occupied := names[0]
	occupiedPath := filepath.Join(target, filepath.FromSlash(occupied))
	if err := os.MkdirAll(filepath.Dir(occupiedPath), 0o755); err != nil {
		t.Fatalf("prepare target dir: %v", err)
	}
	if err := os.WriteFile(occupiedPath, []byte("mine\n"), 0o644); err != nil {
		t.Fatalf("pre-create %s: %v", occupied, err)
	}

	var out strings.Builder
	if err := initGraphs(&out, dir); err != nil {
		t.Fatalf("init must top up around an existing file: %v", err)
	}

	for _, name := range names[1:] {
		path := filepath.Join(target, filepath.FromSlash(name))
		got, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("init did not write the missing %s: %v", name, err)
			continue
		}
		want, err := graphs.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read embedded %s: %v", name, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s on disk differs from the embedded copy", name)
		}
	}
	if got, err := os.ReadFile(occupiedPath); err != nil || string(got) != "mine\n" {
		t.Errorf("the occupied file must be untouched, got %q (err %v)", got, err)
	}
	if !strings.Contains(out.String(), fmt.Sprintf("%d file(s) written to %s", len(names)-1, target)) {
		t.Errorf("summary should count only the files written:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "1 file(s) were already there") {
		t.Errorf("summary should count what it kept:\n%s", out.String())
	}
}

// TestInitGraphs_SecondRunIsANoOp pins idempotence, the property that makes
// re-running `init` the supported way to collect a payload addition: over a
// directory that already holds the whole payload it writes nothing, keeps
// everything, and succeeds — so a user can run it without first working out
// whether they need to.
func TestInitGraphs_SecondRunIsANoOp(t *testing.T) {
	names := embeddedGraphNames(t)
	dir := t.TempDir()
	if err := initGraphs(io.Discard, dir); err != nil {
		t.Fatalf("first init failed: %v", err)
	}
	before := remainingTree(t, filepath.Join(dir, "graphs"))

	var out strings.Builder
	if err := initGraphs(&out, dir); err != nil {
		t.Fatalf("a second init must succeed: %v", err)
	}
	if after := remainingTree(t, filepath.Join(dir, "graphs")); !reflect.DeepEqual(after, before) {
		t.Errorf("a second init changed the tree:\n%v\n%v", before, after)
	}
	if !strings.Contains(out.String(), "0 file(s) written to ") {
		t.Errorf("a second init should report nothing written:\n%s", out.String())
	}
	if !strings.Contains(out.String(), fmt.Sprintf("%d file(s) were already there", len(names))) {
		t.Errorf("a second init should report the whole payload kept:\n%s", out.String())
	}
}

// TestInitGraphs_RollsBackWhenAWriteFailsMidLoop covers the case the pre-flight
// sweep structurally cannot: a path that is free when the sweep looks at it but
// occupied by the time the loop reaches it. A dangling symlink is the
// deterministic stand-in — os.Stat follows it and reports "not there", so the
// sweep passes, while O_EXCL refuses it, so the write fails after earlier files
// have already landed. Without rollback the user is left with exactly the
// half-unpacked graphs/ the docstring, README and DESIGN.md all promise they
// cannot get.
func TestInitGraphs_RollsBackWhenAWriteFailsMidLoop(t *testing.T) {
	names := embeddedGraphNames(t)
	if len(names) < 2 {
		t.Skip("needs at least two embedded graphs for a write to fail after another succeeded")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "graphs")
	// The last path in walk order, so every other write succeeds first —
	// including the ones inside the nested fragments/ directory, whose
	// creation the rollback therefore also has to undo.
	blocked := names[len(names)-1]
	blockedPath := filepath.Join(target, filepath.FromSlash(blocked))
	if err := os.MkdirAll(filepath.Dir(blockedPath), 0o755); err != nil {
		t.Fatalf("prepare target dir: %v", err)
	}
	if err := os.Symlink(filepath.Join(dir, "nowhere.yaml"), blockedPath); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}

	var out strings.Builder
	err := initGraphs(&out, dir)
	if err == nil {
		t.Fatal("init must fail when a target path is taken by the time it writes")
	}
	if !strings.Contains(err.Error(), blockedPath) {
		t.Errorf("error should name the offending path %q: %v", blocked, err)
	}

	want := pathAndAncestors(blocked)
	if got := remainingTree(t, target); !reflect.DeepEqual(got, want) {
		t.Errorf("a failed init must leave nothing behind, %s holds %v, want %v", target, got, want)
	}
	// A listing naming files that were rolled back would send the user looking
	// for graphs that are not there.
	if out.String() != "" {
		t.Errorf("a failed init should print no listing:\n%s", out.String())
	}
}

// TestInitGraphs_TopUpRestoresAFragmentTheTreeIsMissing is the end-to-end
// version of the top-up, on the payload half that actually broke: a tree that
// predates a shipped fragment. Deleting one stands in for the v0.5.3 user
// whose `init` ran before `pr-publish.yaml` existed — after re-running `init`
// the fragment is back AND every template still loads, which is the promise a
// bare "the file appeared" assertion does not make.
func TestInitGraphs_TopUpRestoresAFragmentTheTreeIsMissing(t *testing.T) {
	fragments := embeddedFragmentNames(t)
	if len(fragments) == 0 {
		t.Fatal("no fragments are embedded — the fragments/*.yaml pattern in graphs/embed.go matched nothing")
	}
	dir := t.TempDir()
	if err := initGraphs(io.Discard, dir); err != nil {
		t.Fatalf("first init failed: %v", err)
	}
	target := filepath.Join(dir, "graphs")
	absent := filepath.Join(target, filepath.FromSlash(fragments[0]))
	if err := os.Remove(absent); err != nil {
		t.Fatalf("remove %s: %v", fragments[0], err)
	}

	var out strings.Builder
	if err := initGraphs(&out, dir); err != nil {
		t.Fatalf("re-running init must deliver the missing fragment: %v", err)
	}
	if !strings.Contains(out.String(), "wrote "+absent) {
		t.Errorf("listing should name the restored fragment:\n%s", out.String())
	}
	got, err := os.ReadFile(absent)
	if err != nil {
		t.Fatalf("the missing fragment was not restored: %v", err)
	}
	want, err := graphs.FS.ReadFile(fragments[0])
	if err != nil {
		t.Fatalf("read embedded %s: %v", fragments[0], err)
	}
	if string(got) != string(want) {
		t.Errorf("%s on disk differs from the embedded copy", fragments[0])
	}
	for _, name := range embeddedGraphNames(t) {
		if strings.Contains(name, "/") {
			continue // a fragment loads through the template that cites it
		}
		if _, err := graph.LoadFile(filepath.Join(target, name)); err != nil {
			t.Errorf("template %s does not load after a top-up: %v", name, err)
		}
	}
}

// --- argv and exit codes -------------------------------------------------------

func TestRunInit_ArgvErrors(t *testing.T) {
	if err := runInit([]string{"a", "b"}); err == nil || !strings.Contains(err.Error(), `"b"`) {
		t.Errorf("an extra argument should be named, got: %v", err)
	}
}

// TestMainExitCode_InitMapsToZeroAndOne pins the shell contract end to end
// through run()'s subcommand switch: exit 0 unpacking into a fresh directory,
// exit 0 again over the same directory (a top-up that writes nothing is a
// success — scripting `init` before a run must not need an `|| true`), exit 1
// on a usage error.
func TestMainExitCode_InitMapsToZeroAndOne(t *testing.T) {
	dir := t.TempDir()
	if code := mainExitCode([]string{"init", dir}); code != 0 {
		t.Errorf("init into a fresh directory exited %d, want 0", code)
	}
	if code := mainExitCode([]string{"init", dir}); code != 0 {
		t.Errorf("a second init over the same directory exited %d, want 0", code)
	}
	if code := mainExitCode([]string{"init", dir, "extra"}); code != 1 {
		t.Errorf("init with an extra argument exited %d, want 1", code)
	}
}
