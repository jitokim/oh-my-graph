package serve

import (
	"os"
	"runtime/debug"
	"strings"
)

// BuildLabel names the binary that is serving a page: the release version the
// caller passes in, plus whatever can be established about *which build of it*
// this is — `v0.5.2 (cef30c6, built 2026-08-09 14:02)`.
//
// It exists because a `serve` process outlives the tree it was built from. The
// server holds its port for as long as it runs, keeps answering, and keeps
// serving the code it was compiled from while `bin/oh-my-graph` is rebuilt
// underneath it — which reads, from a browser, exactly like the new build
// misbehaving. (It was once reported as "serve dies silently and a stale binary
// holds the port"; neither half was true. `serve` reports a bind failure by
// name and points at --port, and a run's embedded live view binds port 0, so it
// cannot hold a fixed one. The only thing that was silent was which build was
// answering.)
//
// The version alone would NOT settle that, which is the whole reason the two
// details below are here: every build between two tags carries the same
// version, so a week-old process and this afternoon's both say v0.5.2.
//
//   - The revision comes from runtime/debug.ReadBuildInfo, which the go tool
//     stamps from VCS at link time. It is absent more often than it looks: a
//     `-buildvcs=false` build, a module build off the proxy, and — measured on
//     this repo, 2026-08-09, go1.26.5 — a build from a linked git WORKTREE,
//     which is how this project's own graph lanes build. So it is never the
//     only thing distinguishing two builds.
//   - The build time is the running executable's own mtime, read ONCE here at
//     startup rather than per request: `go build -o` replaces the file at that
//     path, so a later stat would report the build that replaced this one — the
//     exact confusion this label exists to end. It moves on every rebuild,
//     stamp or no stamp.
//
// No dependency, no exec seam, and nothing read at request time.
func BuildLabel(version string) string {
	label := "v" + strings.TrimPrefix(version, "v")
	detail := strings.Join(nonEmpty(buildRevision(), buildTime()), ", ")
	if detail != "" {
		label += " (" + detail + ")"
	}
	return label
}

// buildRevision is the short VCS revision the toolchain stamped, marked when
// the tree it was built from had uncommitted changes — two processes can report
// the same commit and be running different code, and that is exactly the case a
// bare revision would mislead about. "" when nothing was stamped.
func buildRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	var revision, modified string
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value
		}
	}
	if revision == "" {
		return ""
	}
	if len(revision) > 7 {
		revision = revision[:7]
	}
	if modified == "true" {
		revision += "-dirty"
	}
	return revision
}

// buildTime is the running executable's mtime, to the minute, in local time —
// the reader is looking at a page served by their own machine, and "is this
// today's build" is a local-clock question. "" when the executable cannot be
// located or stat'd, which is not worth an error: the label degrades, the
// server does not.
func buildTime() string {
	path, err := os.Executable()
	if err != nil {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return "built " + info.ModTime().Local().Format("2006-01-02 15:04")
}

// nonEmpty drops the empty strings, so a missing detail leaves no stray
// separator behind.
func nonEmpty(values ...string) []string {
	kept := make([]string, 0, len(values))
	for _, v := range values {
		if v != "" {
			kept = append(kept, v)
		}
	}
	return kept
}
