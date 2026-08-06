// The CLI half of ADR 0016's build evidence: the pre-flight that refuses an
// unrunnable --verify-cmd before a planner call is paid for, and the two
// disclosures — what the sinks will run when a command was supplied, and what
// the run will NOT check when one was not.
//
// The engine half (attachment, serialization, the retry cap, detection) lives
// in internal/coordinator and is not repeated here. What lives here is
// everything that depends on argv or on the terminal.
package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
)

// verifyShellMetachars are the characters whose presence anywhere in a
// --verify-cmd means the string is a shell FRAGMENT rather than a plain
// program invocation — a pipeline, a conditional, a substitution, an
// assignment, a redirect, a glob.
//
// Their presence switches the pre-flight below OFF, deliberately. `sh -c` is
// what actually runs the command (verify.ShellVerifier), and re-implementing
// its word splitting here to decide a refusal would be a second, worse shell:
// `cd sub && make` starts with `cd`, which is a builtin on Linux and a file on
// macOS, so a naive check would refuse a working command on one platform only.
// The pre-flight's job is to catch the typo and the missing toolchain, not to
// parse shell.
const verifyShellMetachars = "|&;<>()$`\\\"'\n*?[]{}~="

// checkVerifyExecutable refuses a --verify-cmd whose program cannot be found or
// cannot be executed, naming what was tried.
//
// It runs at flag-parse time, which is the whole point: the alternative is that
// the user pays for a planner call (and, on a validation refusal, a second one)
// and only then learns that `./gradlew` is not in this directory. A build
// command that was never going to run is the cheapest possible thing to
// diagnose, and it must stay cheap.
//
// It NEVER spawns anything — this file imports no os/exec and adds no fifth
// exec seam (internal/invariants). Resolution is os.Stat over the same
// candidates a shell would consider: the path itself when the program is
// written as one, every $PATH entry otherwise.
//
// Three things it deliberately does not do: parse shell (see
// verifyShellMetachars), check anything on Windows (there is no execute bit,
// and PATHEXT resolution belongs to the interpreter), or guarantee the command
// will succeed. A `--verify-cmd 'true'` still passes this and still verifies
// nothing — the ledger reports provenance, never adequacy (ADR 0016).
//
// It has one false refusal, and it is the price of not parsing shell: a bare
// shell BUILTIN with no metacharacter in it (`--verify-cmd 'ulimit -n'`) is a
// command `sh -c` would run and this refuses, because no file answers to it.
// Knowing which words are builtins is knowing the interpreter — the thing
// verifyShellMetachars stands down rather than half-reimplement — and a
// builtin as a build command is not a case worth a table that would be wrong
// on some shell somewhere. The escape hatch is the same one a fragment takes:
// any metacharacter, `ulimit -n && true` included, skips this entirely.
func checkVerifyExecutable(command string) error {
	command = strings.TrimSpace(command)
	if command == "" || strings.ContainsAny(command, verifyShellMetachars) {
		return nil
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	program := strings.Fields(command)[0]
	if strings.ContainsRune(program, filepath.Separator) {
		return checkExecutableFile(command, program, resolvedPath(program))
	}
	var searched []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		if isExecutableFile(filepath.Join(dir, program)) {
			return nil
		}
		searched = append(searched, dir)
	}
	return fmt.Errorf("verify command %q cannot run: %q is not in any PATH directory. "+
		"The command is checked before the planner call, so this costs nothing — fix it and re-run.\nSearched:%s",
		command, program, searchedList(searched))
}

// searchedList renders the PATH entries a failed search covered, one per line.
//
// The whole PATH inline is several terminal lines of colon-separated text the
// reader has to split by eye to find the directory they expected to be in it,
// which is the one thing this message is for. An empty PATH says so rather
// than trailing a blank line.
func searchedList(dirs []string) string {
	if len(dirs) == 0 {
		return " nothing — PATH is empty"
	}
	return "\n  " + strings.Join(dirs, "\n  ")
}

// resolvedPath makes a path-shaped program absolute for the message, so a
// refusal names the file that was actually looked for rather than the relative
// spelling the user typed. An unresolvable cwd leaves the path as typed —
// worse prose, never a wrong refusal.
func resolvedPath(program string) string {
	abs, err := filepath.Abs(program)
	if err != nil {
		return program
	}
	return abs
}

// checkExecutableFile reports why path cannot be run, distinguishing the two
// failures a user fixes differently: the file is not there (wrong directory,
// wrong name, a checkout that has not been built), or it is there without an
// execute bit (a fresh clone of a wrapper script, a file that lost its mode
// through an archive).
func checkExecutableFile(command, program, path string) error {
	info, err := os.Stat(path)
	switch {
	// The *PathError's own text repeats the path this message already names,
	// so the not-exists case — by far the common one — states it once.
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("verify command %q cannot run: tried %s — no such file. "+
			"The command is checked before the planner call, so this costs nothing — fix it and re-run",
			command, path)
	case err != nil:
		return fmt.Errorf("verify command %q cannot run: tried %s — %v", command, path, err)
	case info.IsDir():
		return fmt.Errorf("verify command %q cannot run: tried %s — it is a directory", command, path)
	case info.Mode().Perm()&0o111 == 0:
		return fmt.Errorf("verify command %q cannot run: tried %s — it is not executable (mode %v); `chmod +x %s`",
			command, path, info.Mode().Perm(), program)
	}
	return nil
}

// isExecutableFile reports whether path is a file some execute bit is set on.
// A directory named like the program is not a candidate, which is the one way
// a PATH search can otherwise "find" something unrunnable.
func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0
}

// noteVerifyAttachments discloses, with the plan, exactly which nodes the
// engine will run the user's command at and under what bound.
//
// It is printed for the same reason the agent and skill mappings are: trusted
// code quietly adding an engine-run shell command to the graph a human is
// about to approve would defeat the reason the attachment lives in trusted
// code at all. It matters most under --plan-only, whose entire bargain is that
// a user can see what a run would do before paying for the run — a preview
// that showed the topology but not the command the engine would execute at its
// sinks would be showing the cheap half.
//
// Silence means the zero-config path, which noteVerifyAdvice then describes.
func noteVerifyAttachments(w io.Writer, attachments []coordinator.VerifyAttachment) {
	if len(attachments) == 0 {
		return
	}
	fmt.Fprintf(w, "  build evidence (--verify-cmd): the ENGINE runs this itself at each sink node below,\n"+
		"  after that node's own subprocess, one at a time — a sink that fails it fails the run:\n")
	for _, a := range attachments {
		fmt.Fprintf(w, "    + %s: `%s` (timeout %s)\n", a.NodeID, a.Command, a.Timeout)
	}
}

// noteVerifyAdvice prints ADR 0016 §3's line when no --verify-cmd was given:
// what this run will not check, and the one flag that would change it.
//
// dir is the invocation directory, scanned for build markers. Detection informs
// and never grants: what a found marker buys is one line of printed prose with
// a suggested command in it, which the human then chooses to accept or not. A
// per-node detector that DERIVED a grant would let node 1 of the same run
// create a package.json and widen node 2's tool set — a plan bootstrapping its
// own grant, with no attacker anywhere.
//
// Printed by `auto` only. `chat` shares planAndExecute but has no flag to point
// at, and advice a reader cannot act on is noise.
func noteVerifyAdvice(w io.Writer, v coordinator.VerifyCommand, dir string) {
	if advice := coordinator.VerifyAdvice(v, coordinator.DetectBuildSignals(dir)); advice != "" {
		fmt.Fprint(w, advice)
	}
}
