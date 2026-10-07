package coordinator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// The splice is ADR 0038 §2.3's catalog step: the first of the
// post-validation mutations, after validatePlannedNodes has judged every
// citation against the offered menu and before anything else touches the
// graph. It is where C.2 (the file changed since the menu was rendered) and
// C.3 (the same shape cited twice) are answered, and the reason graph.json
// holds the resolved graph and never a reuse: or bind: key.

// ReuseCatalogFileName is the per-run record of what the catalog offered and
// what the plan took, written beside graph.json (ADR 0038 §2.3 C.2).
const ReuseCatalogFileName = "reuse-catalog.json"

// ReuseRecord is what a plan tells the operator about the reuse catalog: the
// scan the planner was shown and the citations trusted code spliced. nil on a
// Plan means reuse was off, and nothing about reuse is printed or written.
type ReuseRecord struct {
	// Catalog is the scan the menu was rendered from: Catalog.Dir is the
	// directory scanned, len(Catalog.Offered) the offered count, and
	// Catalog.SkippedByReason() the skipped counts.
	Catalog ReuseCatalog
	// Citations is every reuse: node the plan carried, in plan order.
	Citations []ReuseCitation
}

// ReuseCitation is one planned node's citation, as spliced: the node that
// cited, the entry it named, and the file and digest that were spliced.
type ReuseCitation struct {
	NodeID  string
	EntryID string
	Source  string
	SHA256  string
}

// spliceReuse resolves every reuse: node of a validated planned graph. For
// each citation, in plan order, it re-reads the entry's recorded source and
// re-hashes it — a mismatch fails the plan naming the id, the path and both
// digests — and re-computes admission from the bytes it read, never from the
// catalog's record. It then splices with internal/graph's fragment machinery,
// runs EVERY spliced node through plannedNodeRefusals, the read-only tool rule
// and the no-permission_mode rule, and runs Graph.Validate.
//
// spec is the planner's JSON reply that g was parsed from. The returned spec
// is the resolved graph's JSON, so the saved graph.json carries no reuse: or
// bind:. A graph with no citation is returned unchanged, spec and all.
func spliceReuse(g *graph.Graph, spec []byte, offered []ReuseEntry) (*graph.Graph, []byte, []ReuseCitation, error) {
	var citations []ReuseCitation
	pinned := make(map[string][]byte)
	graphDir := ""
	for _, node := range g.Nodes {
		if node.Reuse == "" {
			continue
		}
		entry, ok := offeredEntry(offered, node.Reuse)
		if !ok {
			return nil, nil, nil, fmt.Errorf("planned node %q cites the reusable shape %q, which is not on the menu this plan was shown", node.ID, node.Reuse)
		}
		// The recorded source must be the one place the id resolves to, or the
		// bytes checked here and the bytes the loader's id names would differ.
		dir := filepath.Dir(filepath.Dir(entry.Source))
		if entry.Source != filepath.Join(dir, "fragments", entry.ID+".yaml") || (graphDir != "" && dir != graphDir) {
			return nil, nil, nil, fmt.Errorf("planned node %q cites the reusable shape %q, whose recorded source %s is not where that id resolves", node.ID, entry.ID, entry.Source)
		}
		graphDir = dir

		data, err := os.ReadFile(entry.Source)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("planned node %q cites the reusable shape %q, and its file %s could not be re-read for the splice: %w", node.ID, entry.ID, entry.Source, err)
		}
		sum := sha256.Sum256(data)
		if digest := hex.EncodeToString(sum[:]); digest != entry.SHA256 {
			return nil, nil, nil, fmt.Errorf("planned node %q cites the reusable shape %q, whose file %s changed after the menu was rendered: offered with sha256 %s, now sha256 %s — the plan was made against bytes that no longer exist, so it is refused rather than spliced with different ones",
				node.ID, entry.ID, entry.Source, entry.SHA256, digest)
		}
		// Every read of one entry matched the same digest, so they are the
		// same bytes and the first is the one spliced.
		if _, seen := pinned[entry.ID]; !seen {
			if _, skip := readmitReuseData(graphDir, entry.ID, data); skip != nil {
				return nil, nil, nil, fmt.Errorf("planned node %q cites the reusable shape %q, whose file %s no longer passes admission (%s): %s",
					node.ID, entry.ID, entry.Source, skip.Reason, skip.Detail)
			}
			pinned[entry.ID] = data
		}
		citations = append(citations, ReuseCitation{NodeID: node.ID, EntryID: entry.ID, Source: entry.Source, SHA256: entry.SHA256})
	}
	if len(citations) == 0 {
		return g, spec, nil, nil
	}

	spliced, err := graph.SpliceReuse(spec, graphDir, pinned)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("splice the cited reusable shapes: %w", err)
	}
	if err := checkSplicedNodes(spliced, citations); err != nil {
		return nil, nil, nil, err
	}
	if err := spliced.Validate(); err != nil {
		return nil, nil, nil, fmt.Errorf("the spliced graph is invalid: %w", err)
	}
	// Re-encoded and re-parsed, as attachVerification does, so the graph that
	// runs is the one graph.json replays.
	resolved, err := json.Marshal(spliced)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("re-encode the spliced graph: %w", err)
	}
	reparsed, err := graph.Parse(resolved)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("re-parse the spliced graph: %w", err)
	}
	return reparsed, resolved, citations, nil
}

// checkSplicedNodes runs every node a citation produced through the whole of
// plannedNodeRefusals — the checks a planner-written node faces, not a subset —
// so the splice is never a door for a field the planner is refused, and then
// through admission's narrower tool rule, reuseReadOnlyTools (§9.2), and its
// permission_mode rule: a spliced node declares none, not merely no bypass one
// (§2.2.1). A spliced multi-node id is judged under its own segment, as
// admission judges it: the '/' is the splicer's, which validatePlannedNodeID's
// refusal does not mean.
func checkSplicedNodes(g *graph.Graph, citations []ReuseCitation) error {
	var refusals []string
	for _, node := range g.Nodes {
		citation, ok := citationOf(node.ID, citations)
		if !ok {
			continue
		}
		label := node.ID
		node.ID = node.ID[strings.LastIndex(node.ID, "/")+1:]
		for _, refusal := range plannedNodeRefusals(node, nil, nil) {
			refusals = append(refusals, fmt.Sprintf("spliced node %q (from the reusable shape %q, %s): %s", label, citation.EntryID, citation.Source, refusal.Reason))
		}
		if tool, outside := reuseToolOutsideReadOnly(node.AllowedTools); outside {
			refusals = append(refusals, fmt.Sprintf("spliced node %q (from the reusable shape %q, %s): declares tool %q, which is not one of the read-only tools a reusable shape may bring (%s)",
				label, citation.EntryID, citation.Source, tool, strings.Join(reuseReadOnlyTools, ", ")))
		}
		if node.PermissionMode != "" {
			refusals = append(refusals, fmt.Sprintf("spliced node %q (from the reusable shape %q, %s): declares permission_mode %s — a reusable shape declares none, since a spliced declaration is a key the planner is forbidden arriving by another door",
				label, citation.EntryID, citation.Source, node.PermissionMode))
		}
	}
	if len(refusals) > 0 {
		return fmt.Errorf("the spliced graph is refused: %s", strings.Join(refusals, "; "))
	}
	return nil
}

// citationOf is the citation a spliced node came from: the citing node itself
// for a single-node shape, or a node in its namespace for a multi-node one.
func citationOf(id string, citations []ReuseCitation) (ReuseCitation, bool) {
	for _, citation := range citations {
		if id == citation.NodeID || strings.HasPrefix(id, citation.NodeID+"/") {
			return citation, true
		}
	}
	return ReuseCitation{}, false
}

// reuseRecordFile is reuse-catalog.json's shape.
type reuseRecordFile struct {
	Dir       string                `json:"dir"`
	Offered   []reuseRecordEntry    `json:"offered"`
	Citations []reuseRecordCitation `json:"citations"`
}

type reuseRecordEntry struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type reuseRecordCitation struct {
	NodeID  string `json:"node_id"`
	EntryID string `json:"entry_id"`
	Source  string `json:"source"`
	SHA256  string `json:"sha256"`
}

// WriteReuseRecord writes reuse-catalog.json into dir, beside graph.json,
// owner-only: for each offered entry its id, absolute source, size and
// SHA-256, and for each citation the node, the entry and the digest spliced.
// With reuse off (Plan.Reuse nil) it writes nothing and returns "".
func (p Plan) WriteReuseRecord(dir string) (string, error) {
	if p.Reuse == nil {
		return "", nil
	}
	record := reuseRecordFile{
		Dir:       p.Reuse.Catalog.Dir,
		Offered:   []reuseRecordEntry{},
		Citations: []reuseRecordCitation{},
	}
	for _, entry := range p.Reuse.Catalog.Offered {
		record.Offered = append(record.Offered, reuseRecordEntry{ID: entry.ID, Source: entry.Source, Bytes: entry.Bytes, SHA256: entry.SHA256})
	}
	for _, citation := range p.Reuse.Citations {
		record.Citations = append(record.Citations, reuseRecordCitation(citation))
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode %s: %w", ReuseCatalogFileName, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s dir %q: %w", ReuseCatalogFileName, dir, err)
	}
	path := filepath.Join(dir, ReuseCatalogFileName)
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", ReuseCatalogFileName, err)
	}
	return path, nil
}
