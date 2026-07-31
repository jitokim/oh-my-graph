//go:build windows

package browser

// openArgv is the Windows launcher: `start` is a cmd.exe builtin, not an
// executable, so it must run through `cmd /c`. The empty string is start's
// window-title argument — without it, a quoted URL would be consumed as the
// title instead of opened.
func openArgv(url string) []string {
	return []string{"cmd", "/c", "start", "", url}
}
