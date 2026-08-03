package main

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/graphs"
)

// embeddedGraphNames is the set `init` is expected to unpack: whatever the
// binary actually carries. Reading it from the embed FS rather than a
// hardcoded list is deliberate — the embed pattern is a glob, so a template
// added to graphs/ must be covered by these tests without anyone remembering
// to edit them.
func embeddedGraphNames(t *testing.T) []string {
	t.Helper()
	entries, err := fs.ReadDir(graphs.FS, ".")
	if err != nil {
		t.Fatalf("read embedded graphs: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no graphs are embedded — the //go:embed pattern in graphs/embed.go matched nothing")
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
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
		path := filepath.Join(dir, "graphs", name)
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
		want := "wrote " + filepath.Join(dir, "graphs", name)
		if !strings.Contains(out.String(), want) {
			t.Errorf("listing should contain %q:\n%s", want, out.String())
		}
	}
	if !strings.Contains(out.String(), "graph(s) written to "+filepath.Join(dir, "graphs")) {
		t.Errorf("listing should end with a summary naming the target dir:\n%s", out.String())
	}
	// The next step is the Quickstart's own smoke command, so a user who runs
	// `init` never has to go back to the README to find the second command.
	if !strings.Contains(out.String(), "oh-my-graph run "+filepath.Join(dir, "graphs", "haiku-smoke.yaml")) {
		t.Errorf("listing should point at the smoke graph as the next step:\n%s", out.String())
	}
}

// --- the refusal case ---------------------------------------------------------

// TestInitGraphs_RefusesToOverwrite is the destructive-mistake guard: a user
// who edited a shipped template and re-runs `init` (or runs it in a repo
// checkout) must get an error, not a silent restore of their file. The
// assertion checks the surviving bytes, not just the error, because "it
// failed" is satisfiable by a command that clobbered the file and then failed.
func TestInitGraphs_RefusesToOverwrite(t *testing.T) {
	cases := []struct {
		name     string
		occupied string
	}{
		{name: "first file taken", occupied: embeddedGraphNames(t)[0]},
		{name: "last file taken", occupied: embeddedGraphNames(t)[len(embeddedGraphNames(t))-1]},
		{name: "smoke graph taken", occupied: "haiku-smoke.yaml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "graphs")
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatalf("prepare target dir: %v", err)
			}
			const mine = "name: mine\nnodes:\n  - { id: a, prompt: a }\n"
			occupied := filepath.Join(target, tc.occupied)
			if err := os.WriteFile(occupied, []byte(mine), 0o644); err != nil {
				t.Fatalf("pre-create %s: %v", tc.occupied, err)
			}

			var out strings.Builder
			err := initGraphs(&out, dir)
			if err == nil {
				t.Fatal("init must refuse to write over an existing file")
			}
			if !strings.Contains(err.Error(), occupied) {
				t.Errorf("error should name the offending path %q: %v", occupied, err)
			}

			survived, readErr := os.ReadFile(occupied)
			if readErr != nil {
				t.Fatalf("the pre-existing file disappeared: %v", readErr)
			}
			if string(survived) != mine {
				t.Errorf("the pre-existing file was overwritten:\n%s", survived)
			}
		})
	}
}

// TestInitGraphs_WritesNothingWhenItRefuses pins the all-or-nothing half of
// the refusal: one occupied path aborts the whole command before any file is
// created, so the user is never left with a half-unpacked directory to clean
// up by hand.
func TestInitGraphs_WritesNothingWhenItRefuses(t *testing.T) {
	names := embeddedGraphNames(t)
	if len(names) < 2 {
		t.Skip("needs at least two embedded graphs to distinguish partial writes")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "graphs")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("prepare target dir: %v", err)
	}
	// Occupy the last name alphabetically: a command that wrote as it went
	// would have created every earlier file before noticing.
	occupied := names[len(names)-1]
	if err := os.WriteFile(filepath.Join(target, occupied), []byte("mine\n"), 0o644); err != nil {
		t.Fatalf("pre-create %s: %v", occupied, err)
	}

	var out strings.Builder
	if err := initGraphs(&out, dir); err == nil {
		t.Fatal("init must refuse when a target file exists")
	}

	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatalf("read target dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != occupied {
		t.Errorf("refusal must write nothing, found %d entries in %s", len(entries), target)
	}
	if out.String() != "" {
		t.Errorf("a refused init should print no listing:\n%s", out.String())
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
// exit 1 when a target file already exists.
func TestMainExitCode_InitMapsToZeroAndOne(t *testing.T) {
	dir := t.TempDir()
	if code := mainExitCode([]string{"init", dir}); code != 0 {
		t.Errorf("init into a fresh directory exited %d, want 0", code)
	}
	if code := mainExitCode([]string{"init", dir}); code != 1 {
		t.Errorf("a second init over the same directory exited %d, want 1", code)
	}
	if code := mainExitCode([]string{"init", dir, "extra"}); code != 1 {
		t.Errorf("init with an extra argument exited %d, want 1", code)
	}
}
