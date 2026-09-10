// Package docsclaims is a test-only guard over the user-facing documents.
//
// It carries no production code — the whole guard lives in claims_test.go, so
// `make test` runs it and no separate runner exists to forget. What it checks
// is narrow, and it checks it in three directions.
//
// The first is a document ASSERTING what the code has falsified: a document
// may not state an absolute that this repository's own code has already made
// untrue. In that direction it never demands that a document mention anything;
// silence is not a finding, which is what keeps the guard from turning into a
// list of subjects every document has to cover.
//
// The second is a document DENYING what the code shipped, which the first
// direction cannot see for exactly that reason — a document that stops short
// of the behaviour reads as silence. It is not opened up into a coverage
// requirement: each `stated` claim pins ONE named sentence in ONE document,
// and it earns its place by having already drifted. Both of the claims there
// are the same shape of miss: 456d374 shipped a behaviour and corrected
// docs/LIMITATIONS.md, DESIGN.md said the opposite of the new behaviour and
// was not in the diff, and nothing was red.
//
// The third is a document naming a verdict TOKEN in a graph that graph does
// not carry, which neither of the first two can see: such a sentence asserts
// no absolute the code falsified and denies nothing that shipped — it is
// simply wrong about a file it names. DESIGN.md said `FAIL` on `haiku-smoke`'s
// `write` while `graphs/haiku-smoke.yaml` carried no FAIL at all, and every
// test in the tree stayed green: 6d49bff wrote that sentence and 1de664f
// corrected it, by reading. A `namedInGraph` claim declares no anchor, because
// the coordinate it promises is a file rather than a line, and opening the
// file is what resolves it.
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
