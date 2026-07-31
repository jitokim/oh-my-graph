//go:build darwin

package browser

// openArgv is the macOS launcher: `open <url>` hands the URL to Launch
// Services, which dispatches it to the user's default browser.
func openArgv(url string) []string {
	return []string{"open", url}
}
