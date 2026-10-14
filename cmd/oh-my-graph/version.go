package main

import (
	"fmt"
	"io"
	"runtime/debug"

	"github.com/jitokim/oh-my-graph/internal/runfeed"
)

// Version is the oh-my-graph release version printed by `oh-my-graph version`.
var Version = "0.18.0"

// printVersion writes the version line to w. Split from the dispatch switch so
// the output is testable without capturing os.Stdout.
func printVersion(w io.Writer) {
	fmt.Fprintf(w, "oh-my-graph %s\n", Version)
}

// currentEngine is the build stamp every leg records in state.json and on its
// run_started (#373): Version as it stands now — so a binary built with
// -ldflags -X main.Version=... records that value — plus the VCS revision the
// Go toolchain embedded, when there is one. Read per leg rather than cached,
// so it is always this process's own build.
func currentEngine() runfeed.Engine {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return engineFromSettings(Version, nil)
	}
	return engineFromSettings(Version, info.Settings)
}

// engineFromSettings derives the stamp from a build's settings list. The commit
// is vcs.revision, absent when the build carries no VCS info; modified is set
// only for vcs.modified=true AND a commit, because a dirty tree with no
// revision to be dirty against says nothing.
func engineFromSettings(version string, settings []debug.BuildSetting) runfeed.Engine {
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
	engine.Modified = modified && engine.Commit != ""
	return engine
}
