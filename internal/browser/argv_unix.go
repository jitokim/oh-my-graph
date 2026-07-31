//go:build unix && !darwin

package browser

// openArgv is the freedesktop launcher: `xdg-open <url>` hands the URL to the
// desktop environment's registered default browser. It covers Linux and the
// BSDs alike — anywhere xdg-utils is the convention.
func openArgv(url string) []string {
	return []string{"xdg-open", url}
}
