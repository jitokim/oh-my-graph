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
	"github.com/jitokim/oh-my-graph/internal/runstate"
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

// checkVerifyFlags is the whole parse-time gate on the --verify-cmd /
// --verify-timeout pair, applied identically by every subcommand that registers
// it — `auto` at plan time and `resume` at leg time (ADR 0016 §4).
//
// It exists so the two cannot diverge. The ceiling and the value object are the
// coordinator's already, but "which of the two checks a subcommand remembered to
// run" is exactly the kind of per-subcommand detail #198 was: a flag lane that
// wired one command and not the other. subcommand names the caller so the
// refusal reads as that command's, matching every other parse error here.
func checkVerifyFlags(subcommand string, v coordinator.VerifyCommand) error {
	if err := v.Validate(); err != nil {
		return fmt.Errorf("%s: %w", subcommand, err)
	}
	if err := checkVerifyExecutable(v.Command); err != nil {
		return fmt.Errorf("%s: %w", subcommand, err)
	}
	return nil
}

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
// sinks would be showing the cheap half. A resumed leg prints it too
// (continueRun): that leg's command is re-supplied on its own command line, so
// what it attached is exactly as worth stating as a plan's.
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

// noteMissingBuildEvidence is the other half of the plan screen's build-evidence
// slot: noteVerifyAttachments states what the engine WILL run at each sink, and
// this states that it will run nothing. The slot says one or the other and never
// neither (ADR 0030 §2.5b) — an absence stated beside the topology is what makes
// chat's [y/N] a disclosure at all, and it is the screen `--plan-only` prints.
//
// evidence is nil for every surface that never asks the question — a
// hand-written `run`, a resumed leg — and those screens are unchanged.
func noteMissingBuildEvidence(w io.Writer, attachments []coordinator.VerifyAttachment, evidence *coordinator.BuildEvidenceOutcome) {
	if len(attachments) > 0 || evidence == nil || evidence.Answer == coordinator.BuildEvidenceAttached {
		return
	}
	fmt.Fprint(w, "  build evidence: NONE — nothing in this run compiles or tests your code, so every\n"+
		"  verdict below is the model's, about its own work.\n")
	if files := evidence.SignalFiles(); len(files) > 0 {
		fmt.Fprintf(w, "    detected in this directory: %s\n", strings.Join(files, ", "))
	} else {
		fmt.Fprint(w, "    no build signal was detected in this directory\n")
	}
	switch evidence.Answer {
	case coordinator.BuildEvidenceDeclared:
		fmt.Fprintf(w, "    you said so with %s; this run's state.json records it\n", evidence.DeclaredBy)
	case coordinator.BuildEvidenceDisclosed:
		// chat's one keystroke covers two questions — run this plan, and accept
		// that it proves nothing — so the second one is put in front of it here,
		// which is the only place chat can meet it (ADR 0030 §2.6).
		fmt.Fprint(w, "    approving this plan accepts that; this run's state.json records it\n")
	}
}

// noteVerifyAdvice prints ADR 0016 §3's line when no --verify-cmd was given:
// what this run will not check, and the one flag that would change it.
//
// declaration is what the operator said about the absence, which is the one
// thing that turns this from a warning into a receipt (ADR 0030 §2.5b).
//
// Printed by `auto` only. `chat` shares the sweep below but passes io.Discard:
// it registers no verification flags, so advice naming one is advice a reader
// cannot act on, and its disclosure is the plan screen instead.
func noteVerifyAdvice(w io.Writer, v coordinator.VerifyCommand, declaration coordinator.BuildDeclaration, signals []coordinator.BuildSignal) {
	if advice := coordinator.VerifyAdvice(v, declaration, signals); advice != "" {
		fmt.Fprint(w, advice)
	}
}

// answerBuildEvidence is ADR 0030's gate at the CLI: ONE scan of the invocation
// directory feeding all three consumers — the refusal, the advice line, and the
// record the run keeps of what was detected and how it answered.
//
// One scan is the point of this helper existing at all. Two call sites asking
// the same question of two `dir` arguments would let the gate refuse over one
// directory while the advice line described another, and every assertion about
// what the refusal SAYS would still pass; there is nothing in the text that pins
// where it looked.
//
// dir is the invocation directory: the same tree the planned nodes and the
// evidence command would both run in. Detection still never grants — what a
// found marker buys is a printed suggestion and, since ADR 0030, a STOP. A
// per-node detector that derived a grant would let node 1 of the same run create
// a package.json and widen node 2's tool set; this runs once per invocation,
// before the planner call, so nothing a node writes is ever detected.
func answerBuildEvidence(w io.Writer, v coordinator.VerifyCommand, declaration coordinator.BuildDeclaration, dir string) (*coordinator.BuildEvidenceOutcome, error) {
	signals := coordinator.DetectBuildSignals(dir)
	outcome, err := coordinator.RequireBuildEvidence(v, declaration, signals)
	if err != nil {
		return nil, err
	}
	noteVerifyAdvice(w, v, declaration, signals)
	return &outcome, nil
}

// buildEvidenceRecord is the outcome as the snapshot stores it: the answer, what
// the human typed, and the marker FILENAMES — never the detection table's
// suggested commands, so nothing executable can reach a run directory this way
// (ADR 0016 §4, ADR 0030 §2.5a). nil in, nil out: a surface that never asked the
// question records no answer.
func buildEvidenceRecord(outcome *coordinator.BuildEvidenceOutcome) *runstate.BuildEvidence {
	if outcome == nil {
		return nil
	}
	return &runstate.BuildEvidence{
		Answer:     outcome.Answer,
		DeclaredBy: string(outcome.DeclaredBy),
		Signals:    outcome.SignalFiles(),
	}
}
