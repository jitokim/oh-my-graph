package serve

import (
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// Build is everything a served page says about the binary answering it: the
// three machine-readable atoms the page's head carries as <meta> tags, and the
// one human label its footer prints.
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
// The atoms and the label are one value, produced by one constructor, precisely
// so they cannot disagree: a page whose footer names one build while its tags
// name another would be worse than the silence this ends, and the label is
// rendered FROM the fields below rather than beside them.
type Build struct {
	// Version is the release version with any leading "v" trimmed — the exact
	// token `oh-my-graph version` prints after the program name, so comparing
	// two servers is a string equality rather than a parse.
	Version string
	// Revision is the short, -dirty-suffixed VCS revision, "" when the
	// toolchain stamped none (buildRevision).
	Revision string
	// BuiltAt is the running executable's mtime, RFC3339 in local time so that
	// two of them order; "" when the executable cannot be stat'd.
	BuiltAt string
	// Label is the human rendering of the three above —
	// `v0.5.2 (cef30c6, built 2026-08-09 14:02)` — with a missing detail
	// omitted whole rather than left as an empty "()" or a stray ", ". It is ""
	// for the zero Build, which is what a Server told nothing renders.
	Label string
}

// CurrentBuild is what this process can establish about the binary running it,
// given the release version the caller passes in (which lives in package main,
// stamped at link time).
//
// The version alone would NOT settle which build is answering, which is the
// whole reason the other two atoms are here: every build between two tags
// carries the same version, so a week-old process and this afternoon's both say
// v0.5.2.
//
//   - The revision comes from runtime/debug.ReadBuildInfo, which the go tool
//     stamps from VCS at link time. It is absent more often than it looks: a
//     `-buildvcs=false` build, a module build off the proxy, and — measured on
//     this repo, 2026-08-09, go1.26.5 — a build from a linked git WORKTREE,
//     which is how this project's own graph lanes build. So it is never the
//     only thing distinguishing two builds.
//   - The build time is the running executable's own mtime, stat'd ONCE per
//     process rather than per request (buildInstant). It moves on every
//     rebuild, stamp or no stamp.
//
// No dependency, no exec seam, and nothing read at request time.
func CurrentBuild(version string) Build {
	return newBuild(version, buildInstant())
}

// BuildLabel names the binary that is serving a page, for a reader rather than
// a script: `v0.5.2 (cef30c6, built 2026-08-09 14:02)`. It is CurrentBuild's
// own label and not a second rendering of the same facts — the footer prose has
// exactly one author.
func BuildLabel(version string) string {
	return CurrentBuild(version).Label
}

// newBuild renders every form of the build from ONE instant: the RFC3339 atom a
// script orders and the minute-precision phrase the label reads with come from
// the same builtAt, so the head and the footer of a page cannot name different
// minutes. A zero builtAt is "unknown" and renders as neither.
//
// The instant is a parameter rather than a call so that this rendering can be
// exercised against a chosen time; the ONCE-ness lives at the single call site
// in CurrentBuild, on buildInstant.
func newBuild(version string, builtAt time.Time) Build {
	b := Build{
		Version:  strings.TrimPrefix(version, "v"),
		Revision: buildRevision(),
	}
	// The label's minute-precision phrasing, in local time — the reader is
	// looking at a page served by their own machine, and "is this today's
	// build" is a local-clock question.
	var built string
	if !builtAt.IsZero() {
		local := builtAt.Local()
		b.BuiltAt = local.Format(time.RFC3339)
		built = "built " + local.Format("2006-01-02 15:04")
	}
	b.Label = "v" + b.Version
	if detail := strings.Join(nonEmpty(b.Revision, built), ", "); detail != "" {
		b.Label += " (" + detail + ")"
	}
	return b
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

// buildInstant is the running executable's mtime, read once for the life of the
// process and reused thereafter. The zero time means it could not be read.
//
// The memoization is the guarantee, not an optimization: `go build -o` replaces
// the file at that path, so a stat taken after a rebuild reports the build that
// REPLACED this one — a page that names the wrong binary is worse than a page
// that names none. Leaving that to the call sites would make it a convention
// two lines in cmd/oh-my-graph/serve.go happen to keep, and Dashboard.serverFor
// already constructs a Server per request one call away from them; sync.OnceValue
// makes it this function's property instead, where no future caller can lose it.
//
// It is also the ONLY stat: both the RFC3339 atom and the label's minute both
// come off this one value (newBuild), so nothing here reads the filesystem
// twice and nothing can report two different build times for one process.
var buildInstant = sync.OnceValue(executableMTime)

// executableMTime stats the running executable. The zero time when it cannot be
// located or stat'd, which is not worth an error: the page degrades to "unknown"
// on that atom, the server does not.
func executableMTime() time.Time {
	path, err := os.Executable()
	if err != nil {
		return time.Time{}
	}
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
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
