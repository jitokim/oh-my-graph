package coordinator

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/jitokim/oh-my-graph/internal/fence"
	"github.com/jitokim/oh-my-graph/internal/graph"
)

// The reuse catalog is ADR 0038's menu: the operator's own fragment files that
// trusted code has judged safe to offer an unreviewed planner by identifier.
// This file is the scan and its admission rules (§2.1, §2.2, §2.2.1, §9.1–§9.3)
// and nothing else: the prompt block is reusemenu.go's and the splice is
// reusesplice.go's.
//
// Admission is strict on purpose. A fragment a planner may cite is, on the
// committed path, the one repository-authored instruction channel the engine
// opens without a person seeing it first (§9.2), so everything that is not its
// prompt must be something a planner could have written itself. Every rule
// below reads what internal/graph's loader derived — the file is never parsed
// here.

// reuseCatalogSubdir is where the catalog is read from, relative to the
// invocation root (§2.1): the location `oh-my-graph init` unpacks the shipped
// fragments into, and the sibling a graph in <root>/graphs/ resolves `use:`
// against.
const reuseCatalogSubdir = "graphs"

// maxReuseSummaryBytes bounds a catalog entry's one-line summary (§9.2).
const maxReuseSummaryBytes = 200

// reuseReadOnlyTools is every tool an admitted fragment's node may declare,
// and the one place that set is written (ADR 0038 §9.2, #338). It is a strict
// subset of plannedToolAllowlist: that list bounds what a planner may write
// itself, and it also holds Edit, Write, Bash(go *) and Bash(make *), the last
// two of which run arbitrary code. A fragment is repository-authored text the
// committed path opens with nobody reading it first, so it may bring only
// tools that neither write a file nor run a command. Admission
// (admitInspection) and the post-splice backstop (checkSplicedNodes) both
// judge against it, through reuseToolOutsideReadOnly.
var reuseReadOnlyTools = []string{"Read", "Glob", "Grep"}

// reuseReadOnlyToolSet is reuseReadOnlyTools as a lookup set.
var reuseReadOnlyToolSet = toSet(reuseReadOnlyTools)

// reuseToolOutsideReadOnly is the first of tools that is not a member of
// reuseReadOnlyTools, if any.
func reuseToolOutsideReadOnly(tools []string) (string, bool) {
	for _, tool := range tools {
		if !reuseReadOnlyToolSet[tool] {
			return tool, true
		}
	}
	return "", false
}

// singleNodeContribution is what a single-node fragment contributes: its body
// is merged onto the citing node and declares no id of its own.
const singleNodeContribution = "1 node, merged into the node that names it"

// reuseNodeIDPlaceholder stands for the citing node's id in a multi-node
// entry's contribution, which the planner chooses only when it cites.
const reuseNodeIDPlaceholder = "<your-node-id>"

// ReuseCatalog is one scan of the catalog directory: what is offered to the
// planner and why everything else is not. It is a value, built per call and
// held by whoever renders the planner prompt, so validation can later be
// against the menu the planner was actually shown rather than a re-scan.
type ReuseCatalog struct {
	// Dir is the absolute directory scanned; "" when the invocation root could
	// not be resolved at all.
	Dir     string
	Offered []ReuseEntry
	Skipped []ReuseSkip
}

// ReuseEntry is one admitted fragment: the four fields the planner is shown
// (§2.2) and the three facts the run pins it by (§2.3 C.2).
type ReuseEntry struct {
	// ID is the fragment's `fragment:` key, which is also its file stem.
	ID string
	// Contributes is singleNodeContribution, or for a multi-node fragment the
	// count and its declared ids rendered as <your-node-id>/<internal-id>.
	Contributes string
	// Binds is the slots a citation must bind: the declared substitutions the
	// body actually references.
	Binds []string
	// Summary is the description as one line of at most maxReuseSummaryBytes,
	// with control and format characters removed.
	Summary string
	// Source is the absolute path of the file, Bytes its size and SHA256 the
	// hex digest of the bytes the admission rules judged.
	Source string
	Bytes  int64
	SHA256 string
}

// ReuseSkip is one fragment file left off the menu, with the first rule it
// failed.
type ReuseSkip struct {
	Source string
	Reason ReuseSkipReason
	// Detail is the sentence that says which slot, tool, node or field it was.
	Detail string
}

// ReuseSkipReason is the fixed vocabulary a skipped file is counted under, so
// a printout can say "skipped 6 (3 tool not read-only, 3 non-prompt slot)".
type ReuseSkipReason string

// The rules are applied in this order and a file records the first it fails.
const (
	// ReuseSkipLoadError: the file, or a fragment it cites, does not resolve.
	ReuseSkipLoadError ReuseSkipReason = "load error"
	// ReuseSkipAdvisory: the loader raised an advisory over the file's
	// citation chain (§9.3).
	ReuseSkipAdvisory ReuseSkipReason = "advisory"
	// ReuseSkipDescription: a description line looks like a fence marker, or
	// nothing printable is left of it.
	ReuseSkipDescription ReuseSkipReason = "description"
	// ReuseSkipNonPromptSlot: a slot lands, possibly through a nested
	// citation, somewhere other than a prompt: scalar (§2.2's inertness).
	ReuseSkipNonPromptSlot ReuseSkipReason = "non-prompt slot"
	// ReuseSkipTool: a declared tool is not a member of reuseReadOnlyTools
	// (§9.2) — exact membership of plannedToolAllowlist is not enough.
	ReuseSkipTool ReuseSkipReason = "tool not read-only"
	// ReuseSkipPermissionMode: a node declares any permission_mode (§2.2.1).
	ReuseSkipPermissionMode ReuseSkipReason = "permission_mode"
	// ReuseSkipPlannerRefused: a node declares a field plannedNodeRefusals
	// refuses from a planner (§9.2's third test).
	ReuseSkipPlannerRefused ReuseSkipReason = "planner-refused field"
)

// SkippedByReason counts the skipped files per reason.
func (c ReuseCatalog) SkippedByReason() map[ReuseSkipReason]int {
	counts := make(map[ReuseSkipReason]int)
	for _, skip := range c.Skipped {
		counts[skip.Reason]++
	}
	return counts
}

// scanReuseCatalog scans <invocation root>/graphs/fragments/*.yaml, the root
// resolved exactly as Plan resolves it for the unisolated-settings scan
// (resolveInvocationRoot(c.invocationDir)). An unresolvable root and a missing
// directory are both an empty catalog, not an error — a run with no catalog is
// today's run. The error is for a directory that exists and cannot be listed.
func scanReuseCatalog(invocationDir string) (ReuseCatalog, error) {
	root, ok := resolveInvocationRoot(invocationDir)
	if !ok {
		return ReuseCatalog{}, nil
	}
	graphDir := filepath.Join(root.dir, reuseCatalogSubdir)
	catalog := ReuseCatalog{Dir: filepath.Join(graphDir, "fragments")}
	entries, err := os.ReadDir(catalog.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return catalog, nil
	}
	if err != nil {
		return ReuseCatalog{}, fmt.Errorf("scan reuse catalog %q: %w", catalog.Dir, err)
	}
	// os.ReadDir sorts by name, so the menu's order is the directory's and
	// two scans of one directory agree.
	for _, entry := range entries {
		name, isYAML := strings.CutSuffix(entry.Name(), ".yaml")
		if !isYAML || entry.IsDir() {
			continue
		}
		offered, skip := admitReuseFragment(graphDir, name)
		if skip != nil {
			catalog.Skipped = append(catalog.Skipped, *skip)
			continue
		}
		catalog.Offered = append(catalog.Offered, offered)
	}
	return catalog, nil
}

// reuseCatalog is the catalog one planning call offers: the scan of this
// Coordinator's invocation root, or nothing at all when reuse is off — in
// which case the directory is never read. plannerBase is its one caller.
func (c *Coordinator) reuseCatalog() (ReuseCatalog, error) {
	if c.reuseOff {
		return ReuseCatalog{}, nil
	}
	return scanReuseCatalog(c.invocationDir)
}

// admitReuseFragment applies every admission rule to one file and returns
// either its entry or the first rule it failed.
func admitReuseFragment(graphDir, name string) (ReuseEntry, *ReuseSkip) {
	inspection, err := graph.InspectFragment(graphDir, name)
	return admitInspection(filepath.Join(graphDir, "fragments", name+".yaml"), inspection, err)
}

// readmitReuseData applies the same rules to bytes the caller already read —
// the splice's re-admission (ADR 0038 §2.3), which judges what it is about to
// splice and never the catalog's record of an earlier read.
func readmitReuseData(graphDir, name string, data []byte) (ReuseEntry, *ReuseSkip) {
	inspection, err := graph.InspectFragmentData(graphDir, name, data)
	return admitInspection(filepath.Join(graphDir, "fragments", name+".yaml"), inspection, err)
}

// admitInspection is the admission rules over one inspection, in order.
func admitInspection(source string, inspection *graph.FragmentInspection, err error) (ReuseEntry, *ReuseSkip) {
	skip := func(reason ReuseSkipReason, detail string) (ReuseEntry, *ReuseSkip) {
		return ReuseEntry{}, &ReuseSkip{Source: source, Reason: reason, Detail: detail}
	}
	if err != nil {
		return skip(ReuseSkipLoadError, err.Error())
	}
	if len(inspection.Advisories) > 0 {
		return skip(ReuseSkipAdvisory, inspection.Advisories[0].String())
	}
	summary, problem := reuseSummary(inspection.Description)
	if problem != "" {
		return skip(ReuseSkipDescription, problem)
	}
	if len(inspection.Strays) > 0 {
		return skip(ReuseSkipNonPromptSlot, inspection.Strays[0].String()+
			" — a slot the planner binds may reach only a prompt: scalar, where planner text already lands")
	}
	for _, node := range inspection.Nodes {
		if tool, outside := reuseToolOutsideReadOnly(node.AllowedTools); outside {
			return skip(ReuseSkipTool, fmt.Sprintf("node %q declares tool %q, which is not one of the read-only tools a reusable shape may bring (%s)",
				node.ID, tool, strings.Join(reuseReadOnlyTools, ", ")))
		}
	}
	for _, node := range inspection.Nodes {
		if node.PermissionMode != "" {
			return skip(ReuseSkipPermissionMode, fmt.Sprintf("node %q declares permission_mode %s — a reusable shape declares none, since a spliced declaration is a key the planner is forbidden arriving by another door",
				node.ID, node.PermissionMode))
		}
	}
	for _, node := range inspection.Nodes {
		// The namespace a multi-node splice mints is the splicer's, never
		// written — the loader refused a '/' in every id a fragment file
		// wrote — so the body is judged under the id its own file gave it,
		// exactly as validatePlannedNodeID's own comment frames the rule.
		node.ID = node.ID[strings.LastIndex(node.ID, "/")+1:]
		if refusals := plannedNodeRefusals(node, nil); len(refusals) > 0 {
			return skip(ReuseSkipPlannerRefused, refusals[0].Reason)
		}
	}

	digest := sha256.Sum256(inspection.Data)
	return ReuseEntry{
		ID:          inspection.Name,
		Contributes: reuseContribution(inspection.IDs),
		Binds:       inspection.Binds,
		Summary:     summary,
		Source:      inspection.Source,
		Bytes:       int64(len(inspection.Data)),
		SHA256:      hex.EncodeToString(digest[:]),
	}, nil
}

// reuseContribution renders what citing a fragment adds to the plan.
func reuseContribution(ids []string) string {
	if len(ids) == 0 {
		return singleNodeContribution
	}
	spliced := make([]string, 0, len(ids))
	for _, id := range ids {
		spliced = append(spliced, reuseNodeIDPlaceholder+"/"+id)
	}
	return fmt.Sprintf("%d nodes: %s", len(ids), strings.Join(spliced, ", "))
}

// reuseSummary cuts a description to the one line the planner is shown, or
// says why it cannot be shown at all. Every line is judged for the fence-marker
// shape BEFORE the lines are folded: once folded, a marker line would sit
// mid-sentence and no longer look like one, but a reader of the file — and a
// later renderer that wraps — would still see it.
//
// Control and format characters are removed rather than escaped: a newline in
// the summary would be a second line in the planner prompt, and a bidi override
// or zero-width character is text a reviewer of the menu cannot see. The cut is
// fence.Truncate's, which lands on a UTF-8 boundary and announces itself.
func reuseSummary(description string) (string, string) {
	lines := strings.FieldsFunc(description, func(r rune) bool {
		return r == '\n' || r == '\r' || r == '\v' || r == '\f' || r == '\u0085' || r == ' ' || r == ' '
	})
	for _, line := range lines {
		if fence.LooksLikeMarker(line) {
			return "", fmt.Sprintf("description line %q looks like a fence marker, and the summary is placed into the planner prompt", line)
		}
	}
	visible := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, strings.Join(lines, " "))
	summary := strings.Join(strings.Fields(visible), " ")
	if summary == "" {
		return "", "description has no printable text to summarise"
	}
	return fence.Truncate(summary, maxReuseSummaryBytes), ""
}
