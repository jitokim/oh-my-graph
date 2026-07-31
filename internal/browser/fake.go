package browser

import (
	"context"
	"sync"
)

// FakeOpener is the scripted Opener that keeps auto-open paths spawn-free in
// tests — the mirror of runner.FakeRunner, verify.FakeVerifier and
// worktree.FakeManager. It records every URL it is asked to open and can be
// scripted to fail, so a test can prove both that a path opened the right URL
// and that a failed launch is handled, without ever touching a real browser.
type FakeOpener struct {
	mu sync.Mutex
	// err, when set, is what every Open returns (an injected launcher
	// failure: no display, no registered handler).
	err error
	// urls is every URL Open was asked for, in order.
	urls []string
}

// NewFakeOpener builds a FakeOpener that succeeds on every Open.
func NewFakeOpener() *FakeOpener { return &FakeOpener{} }

// InjectError scripts every subsequent Open to return err.
func (f *FakeOpener) InjectError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// Open records the URL and returns the scripted error, if any, without
// spawning anything. A failed Open is still recorded — exactly like
// worktree.FakeManager, the call happened even if it was scripted to fail.
func (f *FakeOpener) Open(ctx context.Context, url string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.urls = append(f.urls, url)
	return f.err
}

// URLs returns every URL Open was asked for, in order — the evidence a test
// asserts against. Returns a copy; never nil.
func (f *FakeOpener) URLs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.urls))
	copy(out, f.urls)
	return out
}
