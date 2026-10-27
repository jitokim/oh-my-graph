package main

import (
	"fmt"
	"io"
	"runtime/debug"

	"github.com/jitokim/oh-my-graph/internal/runfeed"
)

// Version is the oh-my-graph release version printed by `oh-my-graph version`.
var Version = "0.19.0"

// printVersion writes the version line to w. Split from the dispatch switch so
// the output is testable without capturing os.Stdout.
func printVersion(w io.Writer) {
	fmt.Fprintf(w, "oh-my-graph %s\n", Version)
}

// Commit and Modified are the fallback build stamp, set at link time by
// `make build` (-ldflags "-X main.Commit=... -X main.Modified=true"). Go
// embeds vcs.revision only when .git is a directory, so a binary built in a
// linked git worktree, where .git is a file, would otherwise record a bare
// version that reads the same as a release (#373). Modified is a string, not a
// bool, because -X can only set strings; it counts as dirty only when it is
// exactly "true". Both stay "" in any build that does not set them.
var (
	Commit   = ""
	Modified = ""
)

// currentEngine is the build stamp every leg records in state.json and on its
// run_started (#373): Version as it stands now — so a binary built with
// -ldflags -X main.Version=... records that value — plus a commit. The commit
// is the VCS revision the Go toolchain embedded when there is one, and
// otherwise the link-time Commit and Modified. Read per leg rather than cached,
// so it is always this process's own build.
func currentEngine() runfeed.Engine {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return engineFromSettings(Version, nil, Commit, Modified)
	}
	return engineFromSettings(Version, info.Settings, Commit, Modified)
}

// engineFromSettings derives the stamp from a build's settings list and the
// link-time fallback. The build info wins: its vcs.revision and vcs.modified
// are the commit and dirtiness whenever vcs.revision is present, and the
// fallback is ignored entirely then, so a stale -X main.Commit never overrides
// a real revision and a fallback Modified never marks a build-info commit
// dirty. Only with no vcs.revision is the commit fallbackCommit, dirty when
// fallbackModified is exactly "true". The commit is absent when neither source
// has one; modified is set only together with a commit, because a dirty tree
// with no revision to be dirty against says nothing.
func engineFromSettings(version string, settings []debug.BuildSetting, fallbackCommit, fallbackModified string) runfeed.Engine {
	engine := runfeed.Engine{Version: version}
	modified := false
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			engine.Commit = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if engine.Commit == "" {
		engine.Commit = fallbackCommit
		modified = fallbackModified == "true"
	}
	engine.Modified = modified && engine.Commit != ""
	return engine
}
