package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// The tests below are #363's: a planned node with the stock Edit grant rewrote
// the --verify-cmd script and the run still recorded a verified PASS. Every one
// runs against t.TempDir() files and a FakeVerifier, so nothing is spawned.

const okScript = "#!/bin/sh\nexit 0\n"

// writeFile writes content to dir/name, creating parents, and returns the path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// resolved is the symlink-free absolute path a pin records — on macOS a
// t.TempDir() under /var resolves to /private/var.
func resolved(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

// pinnedPaths is the Path of every pin, in order.
func pinnedPaths(pins []PinnedFile) []string {
	out := make([]string, 0, len(pins))
	for _, p := range pins {
		out = append(out, p.Path)
	}
	return out
}

// wantPinFault asserts err is a *PinChangedError naming path and saying it
// changed since launch.
func wantPinFault(t *testing.T, err error, path string) {
	t.Helper()
	var pinErr *PinChangedError
	if !errors.As(err, &pinErr) {
		t.Fatalf("err = %v, want a *PinChangedError", err)
	}
	if pinErr.Path != path {
		t.Errorf("PinChangedError.Path = %q, want %q", pinErr.Path, path)
	}
	// The file comes last: a node's recorded detail keeps only a fault's tail.
	if msg := err.Error(); !strings.HasSuffix(msg, "pinned file "+path+" changed since launch") {
		t.Errorf("error %q must END by naming %s and saying it changed since launch", msg, path)
	}
}

func TestPinCommand_RecordsWordPathAndDigest(t *testing.T) {
	dir := t.TempDir()
	script := writeFile(t, dir, "check.sh", okScript)

	pins := PinCommand("./check.sh", dir)

	sum := sha256.Sum256([]byte(okScript))
	want := []PinnedFile{{Word: "./check.sh", Path: resolved(t, script), Digest: hex.EncodeToString(sum[:])}}
	if !reflect.DeepEqual(pins, want) {
		t.Fatalf("pins = %+v, want %+v", pins, want)
	}
	// The same content pins to the same digest; different content does not.
	other := t.TempDir()
	writeFile(t, other, "check.sh", okScript)
	if again := PinCommand("./check.sh", other); again[0].Digest != pins[0].Digest {
		t.Errorf("identical content pinned to %s and %s", pins[0].Digest, again[0].Digest)
	}
	writeFile(t, other, "check.sh", "#!/bin/sh\nexit 1\n")
	if again := PinCommand("./check.sh", other); again[0].Digest == pins[0].Digest {
		t.Error("different content pinned to the same digest")
	}
}

// TestPinningVerifier_UnchangedFileGivesTheInnerResult (#363): with nothing
// edited the decorator is invisible — a pass, a fail and an inner error all
// arrive exactly as the inner verifier produced them.
func TestPinningVerifier_UnchangedFileGivesTheInnerResult(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "check.sh", okScript)

	for _, tc := range []struct {
		name    string
		command string
		result  Result
		err     error
	}{
		{name: "pass", command: "./check.sh", result: Result{ExitCode: 0, Output: "ok\n", ScrubbedFromEnv: []string{"OPENAI_API_KEY"}}},
		{name: "fail", command: "./check.sh --fail", result: Result{ExitCode: 3, Output: "FAIL\n"}},
		{name: "error", command: "./check.sh --slow", err: &TimeoutError{Command: "./check.sh --slow"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner := NewFakeVerifier(map[string]Result{tc.command: tc.result})
			if tc.err != nil {
				inner.InjectError(tc.command, tc.err)
			}
			v := NewPinningVerifier(inner, tc.command, PinCommand(tc.command, dir))

			got, err := v.Verify(context.Background(), Request{Command: tc.command, Cwd: dir})

			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			if !reflect.DeepEqual(got, tc.result) {
				t.Errorf("result = %+v, want the inner %+v", got, tc.result)
			}
			if n := inner.InvocationCount(tc.command); n != 1 {
				t.Errorf("inner verifier ran %d times, want 1", n)
			}
		})
	}
}

// TestPinningVerifier_EditBeforeVerifyFaultsWithoutRunning (#363): the issue's
// own case. The script is rewritten between launch and the verification; the
// verification is a fault naming the file and the inner verifier never runs.
// Restoring the launch content makes it evidence again.
func TestPinningVerifier_EditBeforeVerifyFaultsWithoutRunning(t *testing.T) {
	dir := t.TempDir()
	script := writeFile(t, dir, "check.sh", okScript)
	const command = "./check.sh"
	inner := NewFakeVerifier(map[string]Result{command: {ExitCode: 0}})
	v := NewPinningVerifier(inner, command, PinCommand(command, dir))

	writeFile(t, dir, "check.sh", "#!/bin/sh\n# tests skipped\nexit 0\n")
	_, err := v.Verify(context.Background(), Request{Command: command, Cwd: dir})
	wantPinFault(t, err, resolved(t, script))
	if n := inner.InvocationCount(command); n != 0 {
		t.Errorf("inner verifier ran %d times after a pre-check fault, want 0", n)
	}

	writeFile(t, dir, "check.sh", okScript)
	if _, err := v.Verify(context.Background(), Request{Command: command, Cwd: dir}); err != nil {
		t.Errorf("restored launch content: err = %v, want the inner result", err)
	}

	pinnedPath := resolved(t, script)
	if err := os.Remove(script); err != nil {
		t.Fatal(err)
	}
	_, err = v.Verify(context.Background(), Request{Command: command, Cwd: dir})
	wantPinFault(t, err, pinnedPath)
}

// TestPinCommand_SkipsWordsThatNameNoRegularFile (#363): a missing argument, an
// unreadable file, a directory, a bare program name and every operator are not
// pinned and cause no fault — while the one real file in the same line still
// is, and still faults when edited.
func TestPinCommand_SkipsWordsThatNameNoRegularFile(t *testing.T) {
	dir := t.TempDir()
	script := writeFile(t, dir, "check.sh", okScript)
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	words := []string{"go", "missing.txt", "sub", "&&", "||", ";", "|", ">", "./check.sh"}
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		locked := writeFile(t, dir, "locked.sh", okScript)
		if err := os.Chmod(locked, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })
		words = append(words, "locked.sh")
	} else {
		t.Log("unreadable-file case skipped: running as root or on windows")
	}
	command := "go test ./... && cat missing.txt || ls sub ; ./check.sh | wc -l > /dev/null; " +
		"sh locked.sh 2>&1"

	pins := PinCommand(command, dir)
	if got, want := pinnedPaths(pins), []string{resolved(t, script)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pinned %v, want only %v (words %v must not pin)", got, want, words)
	}

	inner := NewFakeVerifier(map[string]Result{command: {ExitCode: 0}})
	v := NewPinningVerifier(inner, command, pins)
	if _, err := v.Verify(context.Background(), Request{Command: command, Cwd: dir}); err != nil {
		t.Fatalf("unpinnable words caused a fault: %v", err)
	}
	writeFile(t, dir, "check.sh", "#!/bin/sh\nexit 0 # edited\n")
	_, err := v.Verify(context.Background(), Request{Command: command, Cwd: dir})
	wantPinFault(t, err, resolved(t, script))
}

// TestCommandWords_OperatorsSeparateAndRedirectTargetsDrop (#363): operators
// are separators even with no space around them, and an output redirection's
// target is not a file the command reads.
func TestCommandWords_OperatorsSeparateAndRedirectTargetsDrop(t *testing.T) {
	got := commandWords("make&&./a.sh||b;c|d >out.log 2>>err.log <in.txt 2>&1 (e)")
	want := []string{"make", "./a.sh", "b", "c", "d", "2", "in.txt", "2", "e"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("commandWords = %q, want %q", got, want)
	}
}

// TestPinningVerifier_RepointedSymlinkFaults (#363): a symlink pinned at launch
// and re-pointed at a file with IDENTICAL content still faults, because the
// file the word names is not the one pinned. Left alone, it verifies.
func TestPinningVerifier_RepointedSymlinkFaults(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	dir := t.TempDir()
	target := writeFile(t, dir, "real/check.sh", okScript)
	writeFile(t, dir, "decoy/check.sh", okScript)
	link := filepath.Join(dir, "check.sh")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	const command = "./check.sh"
	pins := PinCommand(command, dir)
	if got, want := pinnedPaths(pins), []string{resolved(t, target)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pinned %v, want the symlink's target %v", got, want)
	}
	inner := NewFakeVerifier(map[string]Result{command: {ExitCode: 0}})
	v := NewPinningVerifier(inner, command, pins)

	if _, err := v.Verify(context.Background(), Request{Command: command, Cwd: dir}); err != nil {
		t.Fatalf("untouched symlink: err = %v, want the inner result", err)
	}

	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "decoy", "check.sh"), link); err != nil {
		t.Fatal(err)
	}
	_, err := v.Verify(context.Background(), Request{Command: command, Cwd: dir})
	wantPinFault(t, err, resolved(t, target))
	if n := inner.InvocationCount(command); n != 1 {
		t.Errorf("inner verifier ran %d times, want only the untouched run", n)
	}
}

// TestPinningVerifier_EditDuringVerifyFaultsAfter (#363): a file changed while
// the command runs — after the pre-check passed — is caught by the post-check,
// and the inner verifier's exit 0 is discarded rather than reported.
func TestPinningVerifier_EditDuringVerifyFaultsAfter(t *testing.T) {
	dir := t.TempDir()
	script := writeFile(t, dir, "check.sh", okScript)
	const command = "./check.sh"

	quiet := NewFakeVerifier(map[string]Result{command: {ExitCode: 0, Output: "ok\n"}})
	quiet.OnVerify(command, func(context.Context) {})
	got, err := NewPinningVerifier(quiet, command, PinCommand(command, dir)).
		Verify(context.Background(), Request{Command: command, Cwd: dir})
	if err != nil || got.Output != "ok\n" {
		t.Fatalf("no edit during the run: (%+v, %v), want the inner result", got, err)
	}

	editing := NewFakeVerifier(map[string]Result{command: {ExitCode: 0, Output: "ok\n"}})
	editing.OnVerify(command, func(context.Context) {
		writeFile(t, dir, "check.sh", "#!/bin/sh\nexit 0 # rewritten mid-run\n")
	})
	got, err = NewPinningVerifier(editing, command, PinCommand(command, dir)).
		Verify(context.Background(), Request{Command: command, Cwd: dir})
	wantPinFault(t, err, resolved(t, script))
	if !reflect.DeepEqual(got, Result{}) {
		t.Errorf("result = %+v, want none: a post-check fault replaces the exit-0 Result", got)
	}
	if n := editing.InvocationCount(command); n != 1 {
		t.Errorf("inner verifier ran %d times, want 1 (the pre-check passed)", n)
	}
}

// TestPinCommand_QuotedWordsResolve (#363): single and double quotes group a
// word with spaces in it and are removed, so the word names the right file.
func TestPinCommand_QuotedWordsResolve(t *testing.T) {
	dir := t.TempDir()
	double := writeFile(t, dir, "my dir/run it.sh", okScript)
	single := writeFile(t, dir, "other file.txt", "data\n")
	writeFile(t, dir, "my", "decoy: the unquoted first half of a split word\n")
	command := `sh "./my dir/run it.sh" 'other file.txt'`

	pins := PinCommand(command, dir)

	if got, want := pinnedPaths(pins), []string{resolved(t, double), resolved(t, single)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pinned %v, want %v", got, want)
	}
	if pins[0].Word != "./my dir/run it.sh" || pins[1].Word != "other file.txt" {
		t.Errorf("words = %q, %q; want the quotes removed", pins[0].Word, pins[1].Word)
	}
	inner := NewFakeVerifier(map[string]Result{command: {ExitCode: 0}})
	v := NewPinningVerifier(inner, command, pins)
	if _, err := v.Verify(context.Background(), Request{Command: command, Cwd: dir}); err != nil {
		t.Fatalf("unchanged quoted files: %v", err)
	}
	writeFile(t, dir, "other file.txt", "changed\n")
	_, err := v.Verify(context.Background(), Request{Command: command, Cwd: dir})
	wantPinFault(t, err, resolved(t, single))
}

// TestPinningVerifier_RelativeWordResolvesAgainstRequestCwd (#363): a relative
// word is re-resolved where the verification runs — Request.Cwd, or the
// process cwd when that is empty — so the same word in another directory, with
// other content, is a different file and faults.
func TestPinningVerifier_RelativeWordResolvesAgainstRequestCwd(t *testing.T) {
	launch := t.TempDir()
	script := writeFile(t, launch, "check.sh", okScript)
	elsewhere := t.TempDir()
	writeFile(t, elsewhere, "check.sh", "#!/bin/sh\nexit 0 # another tree\n")
	const command = "./check.sh"
	inner := NewFakeVerifier(map[string]Result{command: {ExitCode: 0}})
	v := NewPinningVerifier(inner, command, PinCommand(command, launch))

	if _, err := v.Verify(context.Background(), Request{Command: command, Cwd: launch}); err != nil {
		t.Fatalf("Cwd = launch dir: %v", err)
	}
	_, err := v.Verify(context.Background(), Request{Command: command, Cwd: elsewhere})
	wantPinFault(t, err, resolved(t, script))

	t.Chdir(launch)
	if _, err := v.Verify(context.Background(), Request{Command: command}); err != nil {
		t.Errorf("empty Cwd in the launch dir: %v, want the process cwd used", err)
	}
	t.Chdir(elsewhere)
	_, err = v.Verify(context.Background(), Request{Command: command})
	wantPinFault(t, err, resolved(t, script))
}

// TestPinningVerifier_InterpolatedRequestIsStillGuarded (#363): the scheduler
// interpolates {{ }} tokens before the verifier sees the command, so the
// Request's Command need not equal the pinned string. It is still guarded:
// unchanged it reaches the inner verifier, edited it faults without running.
func TestPinningVerifier_InterpolatedRequestIsStillGuarded(t *testing.T) {
	dir := t.TempDir()
	script := writeFile(t, dir, "check.sh", okScript)
	const pinned, interpolated = "./check.sh --within {{ self.timeout }}", "./check.sh --within 5m0s"
	inner := NewFakeVerifier(map[string]Result{interpolated: {ExitCode: 0, Output: "ok\n"}})
	v := NewPinningVerifier(inner, pinned, PinCommand(pinned, dir))

	got, err := v.Verify(context.Background(), Request{Command: interpolated, Cwd: dir})
	if err != nil || got.Output != "ok\n" {
		t.Fatalf("unchanged file: (%+v, %v), want the inner result", got, err)
	}
	writeFile(t, dir, "check.sh", "#!/bin/sh\nexit 0 # edited\n")
	_, err = v.Verify(context.Background(), Request{Command: interpolated, Cwd: dir})
	wantPinFault(t, err, resolved(t, script))
	if n := inner.InvocationCount(interpolated); n != 1 {
		t.Errorf("inner verifier ran %d times, want only the unchanged run", n)
	}
}

// TestPinCommand_RelativeWordRecordsTheFullyResolvedPath (#363): a relative
// word pinned against the process cwd records the path with EVERY symlink
// followed, the cwd's own ancestors included — on macOS /tmp and t.TempDir()'s
// /var are symlinks into /private, and $PWD keeps the unresolved spelling.
func TestPinCommand_RelativeWordRecordsTheFullyResolvedPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	root := t.TempDir()
	real := filepath.Join(root, "real")
	script := writeFile(t, real, "check.sh", okScript)
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Chdir(link)

	pins := PinCommand("./check.sh", ".")
	if got, want := pinnedPaths(pins), []string{resolved(t, script)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pinned %v, want the fully resolved %v", got, want)
	}

	inner := NewFakeVerifier(map[string]Result{"./check.sh": {ExitCode: 0}})
	v := NewPinningVerifier(inner, "./check.sh", pins)
	if _, err := v.Verify(context.Background(), Request{Command: "./check.sh"}); err != nil {
		t.Fatalf("unchanged file, empty Cwd: %v", err)
	}
	if _, err := v.Verify(context.Background(), Request{Command: "./check.sh", Cwd: "."}); err != nil {
		t.Fatalf("unchanged file, Cwd \".\": %v", err)
	}
	writeFile(t, real, "check.sh", "#!/bin/sh\nexit 0 # edited\n")
	_, err := v.Verify(context.Background(), Request{Command: "./check.sh"})
	wantPinFault(t, err, resolved(t, script))
}
