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
// and still left docs/EXAMPLES.md's isolation bullet telling a reader that an
// MCP-dependent `auto` run "will stop working", full stop; 77c9b6a fixed that
// one. The bullet is named rather than numbered for the reason the claim list
// gives: that line was :342 when 77c9b6a found it and is :351 today. Reading
// a file is not checking it, so the check here is a predicate rather than a
// hand-written grep, and the document set it runs over is walked rather than
// listed so that scope can never be the miss either.
//
// The same reasoning reaches the addresses this guard prints. A failure hands
// its reader a file:line into the code that falsified the sentence, and a line
// number goes wrong on its own the moment somebody inserts a line above it. So
// each claim also carries an anchor per coordinate — a line of code that must
// occur exactly once in the file it names, on the line the address names — and
// TestClaimAddressesResolve re-resolves every one of them against the tree,
// naming the address to write instead when the code has moved.
package docsclaims
