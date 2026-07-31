// Package browser is the browser-open seam: it opens a URL — today, the
// `serve` live-view address — in the user's default browser through the
// operating system's own launcher (`open` on macOS, `xdg-open` on other
// unixes, `cmd /c start` on Windows).
//
// It is deliberately NOT part of any existing seam. Launching a browser is
// neither a claude invocation, nor an evidence command, nor worktree
// provisioning; teaching one of those seams to also open URLs would give it —
// and its fake — two unrelated reasons to change. Instead browser-open gets
// its own, narrower interface with its own single exec-owning implementation.
// See docs/adr/0006.
//
// The project invariant this restates (it does not weaken it): exactly four
// objects in oh-my-graph may spawn a process — runner.ClaudeCLIRunner,
// verify.ShellVerifier, worktree.GitManager and browser.ExecOpener — each
// behind its own injected interface, each scrubbing the child environment
// through internal/childenv. No other package imports os/exec.
package browser

import (
	"context"
	"errors"
)

// Opener opens a URL in the user's default browser.
//
// The interface carries no policy: deciding WHETHER to open (run/auto's TTY
// gate, the --no-web opt-out) is the caller's job. An Opener only ever
// answers "open this".
type Opener interface {
	Open(ctx context.Context, url string) error
}

// ErrNoOpener is the sentinel behind RefusingOpener's refusal, so a caller can
// match on it without string matching.
var ErrNoOpener = errors.New(
	"no browser Opener was injected, so the URL cannot be opened; " +
		"wire browser.NewExecOpener() in, or open the printed URL yourself",
)

// NotConfiguredError names the URL a caller tried to open with no Opener wired
// in. It unwraps to ErrNoOpener.
type NotConfiguredError struct {
	URL string
}

func (e *NotConfiguredError) Error() string {
	return "cannot open " + e.URL + ": " + ErrNoOpener.Error()
}

func (e *NotConfiguredError) Unwrap() error { return ErrNoOpener }

// RefusingOpener is the default Opener: it refuses every request instead of
// running anything, mirroring worktree.RefusingProvider. It exists so that
// forgetting to inject a real Opener fails loudly and locally rather than
// silently spawning a launcher from a test — which is exactly how a suite
// that promises "zero real spawns" starts quietly breaking that promise.
//
// A caller that never asks to open a URL never reaches an Opener at all, so
// this default costs existing paths nothing.
type RefusingOpener struct{}

// NewRefusingOpener returns the refuse-everything default Opener.
func NewRefusingOpener() RefusingOpener { return RefusingOpener{} }

// Open always fails, naming the URL it declined to open.
func (RefusingOpener) Open(_ context.Context, url string) error {
	return &NotConfiguredError{URL: url}
}
