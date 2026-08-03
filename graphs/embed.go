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

// FS holds every YAML graph in this directory. The pattern is a glob rather
// than a hardcoded list so a new template added here ships automatically —
// there is no second place to remember to update.
//
//go:embed *.yaml
var FS embed.FS
