//go:build windows

package verify

import "os/exec"

// setProcessGroup is a no-op on Windows: there is no pgid to set, and job
// objects (the real equivalent) are more machinery than this seam needs.
func setProcessGroup(_ *exec.Cmd) {}

// killProcessGroup falls back to killing the direct child, which is what
// os/exec's own cancellation would have done. Windows verification therefore
// keeps the pre-existing behaviour rather than gaining tree-kill; the point of
// this file is that the package still compiles and cancel still kills.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
