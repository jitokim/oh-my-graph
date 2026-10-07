package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
)

// noteReuse is the plan screen's reuse disclosure (ADR 0038 §2.5), under ADR
// 0022 §8's rule that a claim about the operator's files on disk puts the path
// on the screen. One line for the scan — the directory read, the offered
// count, the skipped counts by reason — then one line per citation with the
// entry id, the source path and the full digest reuse-catalog.json records, so
// the screen and the file can be compared character for character.
//
// nil is reuse off (--no-reuse), and prints nothing: a run with the flag looks
// exactly like a run from before the catalog existed. A plan made with reuse
// on always carries a record, even one that cites nothing, so the scan line
// prints whenever the catalog was read.
func noteReuse(w io.Writer, record *coordinator.ReuseRecord) {
	if record == nil {
		return
	}
	catalog := record.Catalog
	dir := catalog.Dir
	if dir == "" {
		dir = "(no invocation root resolved, nothing read)"
	}
	fmt.Fprintf(w, "  reuse: scanned %s — %d offered, %s\n", dir, len(catalog.Offered), skippedSummary(catalog))
	for _, c := range record.Citations {
		fmt.Fprintf(w, "    %s cites %s — %s sha256:%s\n", c.NodeID, c.EntryID, c.Source, c.SHA256)
	}
}

// skippedSummary is the skipped half of the scan line: the total, then each
// reason's count in name order, so two screens of one catalog read the same.
func skippedSummary(catalog coordinator.ReuseCatalog) string {
	counts := catalog.SkippedByReason()
	if len(counts) == 0 {
		return "0 skipped"
	}
	reasons := make([]string, 0, len(counts))
	for reason := range counts {
		reasons = append(reasons, string(reason))
	}
	sort.Strings(reasons)
	parts := make([]string, len(reasons))
	for i, reason := range reasons {
		parts[i] = fmt.Sprintf("%s: %d", reason, counts[coordinator.ReuseSkipReason(reason)])
	}
	return fmt.Sprintf("%d skipped (%s)", len(catalog.Skipped), strings.Join(parts, ", "))
}
