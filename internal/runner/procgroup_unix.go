//go:build unix

package runner

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the claude child in a process group of its own (pgid ==
// the child's pid) so the whole tree it spawns can be signalled as one unit.
// Without it the child shares the engine's group, and killing that group would
// take the engine down with it.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup SIGKILLs every process in the child's group. A negative pid
// means "the process group with this id", which is what reaches claude's own
// descendants — an MCP server, a Bash tool's subprocess — and not just the CLI
// that forked them.
//
// It returns nil once the group is gone: os/exec treats a nil result from Cancel
// as a successful interruption and reports the context's own error, which is the
// verdict Run wants to surface.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		// The group may not exist (already reaped) or may never have been
		// created if Setpgid did not take; fall back to the single process so a
		// cancel is never a no-op.
		return cmd.Process.Kill()
	}
	return nil
}
