//go:build windows

package verify

import (
	"context"
	"testing"
)

// TestInterpreter_Windows pins the Windows half of the OS-aware interpreter
// split: `sh` is not on PATH there, so a hardcoded POSIX shell would make every
// verification an unspawnable-command error.
func TestInterpreter_Windows(t *testing.T) {
	if defaultShell != "cmd" {
		t.Errorf("defaultShell = %q, want %q", defaultShell, "cmd")
	}
	if shellFlag != "/c" {
		t.Errorf("shellFlag = %q, want %q", shellFlag, "/c")
	}
}

// TestBuildCmd_UsesTheWindowsInterpreter proves the constants above are the ones
// that actually reach argv: the command goes to `cmd /c` as ONE argument.
func TestBuildCmd_UsesTheWindowsInterpreter(t *testing.T) {
	cmd := NewShellVerifier().buildCmd(context.Background(), Request{Command: echoCommand})

	assertArgv(t, cmd.Args, []string{"cmd", "/c", echoCommand})
}
