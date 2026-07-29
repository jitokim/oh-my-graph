//go:build !windows

package verify

import (
	"context"
	"testing"
)

// TestInterpreter_Unix pins the unix/darwin half of the OS-aware interpreter
// split. The constants live in build-tagged files, so this is the only place
// that can state what they must be here — and it states it as literals, not by
// re-reading the value under test.
func TestInterpreter_Unix(t *testing.T) {
	if defaultShell != "sh" {
		t.Errorf("defaultShell = %q, want %q", defaultShell, "sh")
	}
	if shellFlag != "-c" {
		t.Errorf("shellFlag = %q, want %q", shellFlag, "-c")
	}
}

// TestBuildCmd_UsesTheUnixInterpreter proves the constants above are the ones
// that actually reach argv: the command goes to `sh -c` as ONE argument.
func TestBuildCmd_UsesTheUnixInterpreter(t *testing.T) {
	cmd := NewShellVerifier().buildCmd(context.Background(), Request{Command: echoCommand})

	assertArgv(t, cmd.Args, []string{"sh", "-c", echoCommand})
}
