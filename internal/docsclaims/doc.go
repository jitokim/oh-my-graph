// Package docsclaims is a test-only guard over the user-facing documents.
//
// It carries no production code — the whole guard lives in claims_test.go, so
// `make test` runs it and no separate runner exists to forget. What it checks
// is narrow: a document may not state an absolute that this repository's own
// code has already falsified. It never demands that a document mention
// anything; silence is not a finding.
//
// It exists because the last sweep missed a sentence, not a file. d537739 —
// the commit that shipped --accept-loaded-user-config — conditioned both of
// the absolutes below where they stood in docs/EXAMPLES.md, alongside
// DESIGN.md, SECURITY.md, README.md, README.ko.md and docs/LIMITATIONS.md,
// and still left docs/EXAMPLES.md:341 telling a reader that an MCP-dependent
// `auto` run "will stop working", full stop; 77c9b6a fixed that one. Reading
// a file is not checking it, so the check here is a predicate rather than a
// hand-written grep, and the document set it runs over is walked rather than
// listed so that scope can never be the miss either.
package docsclaims
