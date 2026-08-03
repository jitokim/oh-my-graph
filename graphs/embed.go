// Package graphs embeds the pipelines this repo ships itself with, so they
// travel inside the binary. `go install` copies one executable and nothing
// else: without this, a fresh user has a working `oh-my-graph` but no
// graphs/ directory, and the Quickstart's first real command names a file
// that only exists inside a repo checkout. `oh-my-graph init` unpacks these
// bytes into the user's own graphs/ directory (see cmd/oh-my-graph/init.go).
//
// This package holds no logic and spawns nothing — it exists because a
// //go:embed pattern cannot reach into a parent directory, so embedding
// graphs/*.yaml requires a Go file rooted here.
package graphs

import "embed"

// FS holds every YAML graph in this directory AND every fragment file in the
// fragments/ subdirectory. Both patterns are globs rather than hardcoded lists
// so a new template or fragment added here ships automatically — there is no
// second place to remember to update.
//
// The fragments/ pattern is load-bearing, not tidiness: a template that cites
// `use: <name>` (ADR 0013) resolves it against its own fragments/ sibling ON
// DISK, so a binary that embedded the templates but not the fragments would
// unpack graphs that cannot load. `//go:embed *.yaml` does not descend into
// subdirectories, which is exactly how that regression happened once.
//
//go:embed *.yaml fragments/*.yaml
var FS embed.FS
