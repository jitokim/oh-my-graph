package main

import (
	"errors"
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
	// The skew marker has to stay silent on a tree that matches the payload,
	// or it is noise on the most common re-run there is.
	if strings.Contains(out.String(), "DIFFERS") {
		t.Errorf("an unchanged tree must not be reported as differing:\n%s", out.String())
	}
}

// TestInitGraphs_MarksAKeptFileThatDiffersFromThePayload is the version-skew
// case a top-up creates and a refusal could not: `init` writes the file a
// later release added while KEEPING the older copy of one it depends on, and
// the mismatch — a template binding a `with:` key the kept fragment does not
// declare — surfaces at graph.LoadFile, naming a node, nowhere near the `init`
// that assembled it. Marking the kept file is what turns that into a hint the
// user already has on screen; without it they must diff the tree against the
// binary to find out which of their kept files is merely stale.
func TestInitGraphs_MarksAKeptFileThatDiffersFromThePayload(t *testing.T) {
	names := embeddedGraphNames(t)
	if len(names) < 2 {
		t.Skip("needs two embedded graphs to tell a marked file from an unmarked one")
	}
	dir := t.TempDir()
	if err := initGraphs(io.Discard, dir); err != nil {
		t.Fatalf("first init failed: %v", err)
	}
	target := filepath.Join(dir, "graphs")
	stale := filepath.Join(target, filepath.FromSlash(names[0]))
	if err := os.WriteFile(stale, []byte("name: mine\nnodes:\n  - { id: a, prompt: a }\n"), 0o644); err != nil {
		t.Fatalf("rewrite %s: %v", names[0], err)
	}

	var out strings.Builder
	if err := initGraphs(&out, dir); err != nil {
		t.Fatalf("a top-up over a differing file must still succeed: %v", err)
	}
	if want := "kept  " + stale + " (already there — not replaced; DIFFERS from this binary's copy)"; !strings.Contains(out.String(), want) {
		t.Errorf("the differing file should be marked, want %q:\n%s", want, out.String())
	}
	if !strings.Contains(out.String(), "1 of those differ from this binary's copy") {
		t.Errorf("the summary should count the differing files:\n%s", out.String())
	}
	// Every other file matches the payload byte for byte, so marking any of
	// them would make the mark meaningless.
	for _, name := range names[1:] {
		path := filepath.Join(target, filepath.FromSlash(name))
		if want := "kept  " + path + " (already there — not replaced)\n"; !strings.Contains(out.String(), want) {
			t.Errorf("an identical file must be kept unmarked, want %q:\n%s", want, out.String())
		}
	}
	// The mark is a report, not an action: the file itself is still theirs.
	if got, err := os.ReadFile(stale); err != nil || !strings.Contains(string(got), "name: mine") {
		t.Errorf("the differing file must be left untouched, got %q (err %v)", got, err)
	}
}

// TestInitGraphs_RollsBackWhenAWriteFailsMidLoop covers the case the per-file
// existence check structurally cannot: a path that is free when the check
// looks at it but occupied by the time the write reaches it. A dangling
// symlink is the deterministic stand-in — os.Stat follows it and reports "not
// there", so the check routes the path to written-not-kept, while O_EXCL
// refuses it, so the write fails after earlier files have already landed.
// Without rollback the user is left with exactly the half-unpacked graphs/ the
// docstring, README and DESIGN.md all promise they cannot get.
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

// TestUnpackedTree_UndoRemovesTheDirectoriesThisRunCreated pins the half of
// the rollback promise the init-level test structurally cannot stage: to make
// a write fail that test must pre-create graphs/ to put the blocker inside,
// which is precisely the case where graphs/ is the user's and must survive.
// Here the run creates graphs/ itself, so a failure has to take it back out —
// an empty graphs/ left behind is the "leaves no tree it made" claim broken.
func TestUnpackedTree_UndoRemovesTheDirectoriesThisRunCreated(t *testing.T) {
	root := t.TempDir()
	found := filepath.Join(root, "mine")
	if err := os.MkdirAll(found, 0o755); err != nil {
		t.Fatalf("pre-create %s: %v", found, err)
	}

	u := &unpackedTree{}
	created := filepath.Join(root, "graphs")
	if err := u.mkdirAll(created); err != nil {
		t.Fatalf("create %s: %v", created, err)
	}
	nested := filepath.Join(created, "fragments")
	if err := u.mkdirAll(nested); err != nil {
		t.Fatalf("create %s: %v", nested, err)
	}
	if err := u.mkdirAll(found); err != nil { // already there: never a cleanup candidate
		t.Fatalf("stat %s: %v", found, err)
	}
	file := filepath.Join(nested, "e2e-verify.yaml")
	if err := os.WriteFile(file, []byte("id: a\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
	u.files = append(u.files, file)

	cause := fmt.Errorf("the write that failed")
	// errors.Is rather than identity: a removal that fails is joined onto the
	// cause, so what undo promises is that the cause stays REACHABLE, not that
	// it is the only thing returned. Nothing fails to remove here, so this run
	// gets the cause itself back — asserting the weaker claim keeps the test
	// about the rollback instead of about the error's shape.
	if got := u.undo(cause); !errors.Is(got, cause) {
		t.Errorf("undo must still carry the cause, got %v", got)
	}
	if _, err := os.Stat(created); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("graphs/ was created by this run and must not survive, got %v", err)
	}
	if _, err := os.Stat(found); err != nil {
		t.Errorf("a directory the run only found must survive undo: %v", err)
	}
}

// TestUnpackedTree_UndoReportsARemovalItCouldNotMake is the failure half: when
// cleanup cannot take a file back out, the tree is left partial, and an error
// that named only the original write would say `init` rolled back when it did
// not. The cause must survive alongside it — the caller still has to be able
// to see WHY the run stopped.
func TestUnpackedTree_UndoReportsARemovalItCouldNotMake(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the directory permissions this test uses to make a removal fail")
	}
	root := t.TempDir()
	u := &unpackedTree{}
	created := filepath.Join(root, "graphs")
	if err := u.mkdirAll(created); err != nil {
		t.Fatalf("create %s: %v", created, err)
	}
	file := filepath.Join(created, "haiku-smoke.yaml")
	if err := os.WriteFile(file, []byte("id: a\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
	u.files = append(u.files, file)
	// A directory the run cannot write is a removal it cannot make.
	if err := os.Chmod(created, 0o555); err != nil {
		t.Fatalf("chmod %s: %v", created, err)
	}
	// Restoring the mode is what lets t.TempDir's own cleanup remove the tree:
	// a failure here leaves the temp directory behind, so it is reported.
	t.Cleanup(func() {
		if err := os.Chmod(created, 0o755); err != nil {
			t.Errorf("restore permissions on %s: %v", created, err)
		}
	})

	cause := fmt.Errorf("the write that failed")
	got := u.undo(cause)
	if !errors.Is(got, cause) {
		t.Fatalf("the cause must stay reachable through a failed cleanup, got %v", got)
	}
	if !strings.Contains(got.Error(), file) {
		t.Errorf("a file cleanup could not remove must be named:\n%v", got)
	}
	// graphs/ itself is still there and non-empty, which os.Remove refuses on
	// purpose (never RemoveAll) — that refusal is the documented choice, so it
	// must NOT be reported as a second failure.
	if strings.Contains(got.Error(), "remove "+created+":") {
		t.Errorf("a non-empty directory left alone by design is not a cleanup failure:\n%v", got)
	}
}

// TestInitGraphs_RefusesANonRegularEntryOnAPayloadPath pins that `kept` means
// "your copy of this file is already here". A directory standing where a
// template belongs satisfies a plain existence check, so reporting it kept
// would exit 0 over a tree in which that template cannot load at all.
func TestInitGraphs_RefusesANonRegularEntryOnAPayloadPath(t *testing.T) {
	names := embeddedGraphNames(t)
	dir := t.TempDir()
	blocked := filepath.Join(dir, "graphs", filepath.FromSlash(names[0]))
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatalf("pre-create %s: %v", blocked, err)
	}

	var out strings.Builder
	err := initGraphs(&out, dir)
	if err == nil {
		t.Fatalf("a directory on a payload path must be an error, got success:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), blocked) {
		t.Errorf("the offending path must be named, got: %v", err)
	}
	if strings.Contains(out.String(), "kept") {
		t.Errorf("a directory must never be reported kept:\n%s", out.String())
	}
}

// --- argv and exit codes -------------------------------------------------------

func TestRunInit_ArgvErrors(t *testing.T) {
	if err := runInit([]string{"a", "b"}); err == nil || !strings.Contains(err.Error(), `"b"`) {
		t.Errorf("an extra argument should be named, got: %v", err)
	}
}

// TestRunInit_HelpDoesNotCreateADirectoryNamedHelp pins #200's worst case:
// `init --help` used to read "--help" as the target directory, CREATE it and
// unpack the whole example payload into it, then exit 0 — the one defective
// subcommand with a filesystem side effect. This drives runInit from a
// scratch, otherwise-empty working directory so a regression here is caught
// as a stray directory, not just a wrong error string. Both spelled forms are
// checked.
func TestRunInit_HelpDoesNotCreateADirectoryNamedHelp(t *testing.T) {
	for _, arg := range []string{"--help", "-h"} {
		t.Run(arg, func(t *testing.T) {
			cwd := chdirTemp(t)

			err := runInit([]string{arg})
			var usage *usageRequest
			if !errors.As(err, &usage) {
				t.Fatalf("runInit([%q]) = %v (%T), want a *usageRequest", arg, err, err)
			}
			if !strings.Contains(usage.Error(), "oh-my-graph init") {
				t.Errorf("usage.Error() = %q, want it to name `init`'s synopsis", usage.Error())
			}

			entries, err := os.ReadDir(cwd)
			if err != nil {
				t.Fatalf("read scratch dir: %v", err)
			}
			if len(entries) != 0 {
				t.Errorf("`init %s` left %d entries in a directory that started empty: %v", arg, len(entries), entries)
			}
			if _, err := os.Stat(filepath.Join(cwd, arg)); err == nil {
				t.Errorf("`init %s` created a directory literally named %q", arg, arg)
			}
		})
	}
}

// TestRunInit_DashPrefixedPositionalIsNotATargetDirectory is the guard the
// other direction: an unrecognised flag standing in init's directory slot
// must be reported as an unknown flag, never created as a directory.
func TestRunInit_DashPrefixedPositionalIsNotATargetDirectory(t *testing.T) {
	cwd := chdirTemp(t)

	err := runInit([]string{"--bogus"})
	if err == nil {
		t.Fatal("expected an error for an unknown flag in the directory slot")
	}
	if !strings.Contains(err.Error(), `unknown flag "--bogus"`) {
		t.Errorf("err = %v, want it to name the unrecognised flag", err)
	}
	if _, statErr := os.Stat(filepath.Join(cwd, "--bogus")); statErr == nil {
		t.Error(`runInit([--bogus]) created a directory literally named "--bogus"`)
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
