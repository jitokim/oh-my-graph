package browser

// Unlike the git and shell seams, whose tests run their real binaries (their
// contract IS the subprocess boundary, and git/sh are safe to run in CI), a
// real launcher here would pop a browser window on whoever runs the suite. So
// this seam's tests stop at the built *exec.Cmd, which carries everything the
// seam promises: the platform launcher argv and the scrubbed environment.

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestExecOpener_BuildsThePlatformLauncherArgv(t *testing.T) {
	const url = "http://127.0.0.1:8642/"
	cmd := NewExecOpener().openCmd(context.Background(), url)

	if want := openArgv(url); !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("built argv %v, want the platform launcher argv %v", cmd.Args, want)
	}
	// On every platform the URL is the final argument, handed over verbatim —
	// never interpolated into a shell line.
	if got := cmd.Args[len(cmd.Args)-1]; got != url {
		t.Errorf("launcher argv must end with the verbatim URL, got %q", got)
	}
}

// TestExecOpener_ScrubsBillingVarsFromLauncherChildren asserts the call-site
// half of the subscription-auth guarantee for the fourth spawner: the billing-
// switching variables are deleted from every launcher child's env (the URL
// handler it dispatches to is arbitrary user-configured code that may
// legitimately invoke claude).
func TestExecOpener_ScrubsBillingVarsFromLauncherChildren(t *testing.T) {
	o := NewExecOpener()
	o.environ = func() []string {
		return []string{
			"ANTHROPIC_API_KEY=sk-live-secret",
			"ANTHROPIC_AUTH_TOKEN=tok-secret",
			"OPENAI_API_KEY=sk-openai-secret",
			"CODEX_API_KEY=sk-codex-secret",
			"HOME=/home/u",
		}
	}

	cmd := o.openCmd(context.Background(), "http://127.0.0.1:8642/")
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") || strings.HasPrefix(kv, "ANTHROPIC_AUTH_TOKEN=") ||
			strings.HasPrefix(kv, "OPENAI_API_KEY=") || strings.HasPrefix(kv, "CODEX_API_KEY=") {
			t.Errorf("billing variable leaked into the launcher child env: %s", kv)
		}
	}
	found := false
	for _, kv := range cmd.Env {
		if kv == "HOME=/home/u" {
			found = true
		}
	}
	if !found {
		t.Error("scrub removed more than the billing variables")
	}
}

// TestExecOpener_RefusesNonLoopbackURLs pins the seam's URL-shape contract:
// only plain http on a loopback host may reach the launcher. On Windows the
// argv reaches `cmd /c start`, where cmd.exe expands metacharacters before
// argv parsing, so the safe URL shape is enforced HERE rather than assumed
// of callers.
func TestExecOpener_RefusesNonLoopbackURLs(t *testing.T) {
	accept := []string{
		"http://127.0.0.1:8642/",
		"http://localhost:8642/",
		"http://[::1]:8642/",
	}
	for _, u := range accept {
		if err := requireLoopbackHTTP(u); err != nil {
			t.Errorf("requireLoopbackHTTP(%q) = %v, want accept", u, err)
		}
	}
	reject := []string{
		"https://127.0.0.1:8642/",         // wrong scheme
		"http://example.com/",             // not loopback
		"http://127.0.0.1:8642/?q=%CD%CD", // query — cmd.exe metacharacter territory
		"http://127.0.0.1:8642/#frag",     // fragment
		"http://user:pw@127.0.0.1:8642/",  // userinfo
		"http://127.0.0.2.example.com/",   // loopback-looking hostname
		"://not-a-url",
	}
	for _, u := range reject {
		if err := requireLoopbackHTTP(u); err == nil {
			t.Errorf("requireLoopbackHTTP(%q) accepted, want refusal", u)
		}
	}
	// And the refusal is live at Open itself, before any spawn.
	if err := NewExecOpener().Open(context.Background(), "http://example.com/"); err == nil {
		t.Error("Open must refuse a non-loopback URL before spawning anything")
	}
}
