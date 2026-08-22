//go:build ignore

// Measure the candidate "rule 1" predicate from the batch-lane survey memo:
// "two lanes in one graph must not touch the same file".
//
// This is a MEASUREMENT, not a lint. It ships no behaviour and nothing in the
// engine calls it. The `ignore` build tag above keeps it out of
// `go build ./...` and `go test ./...`; run it explicitly:
//
//	go run docs/measurements/0034-lane-file-ownership-predicate.go
//
// (`go run` with an explicit file argument does not apply build constraints,
// which is the whole reason the tag is safe here.)
//
// PARSE, DO NOT GREP. This repo has a written scar: a `grep -c` count went
// into three documents and was wrong, because grep counted comments and
// counted per file. Every graph below goes through the repo's OWN loader —
// graph.LoadFile for a .yaml (so every fragment `use:` is spliced and every
// `with:` binding substituted BEFORE anything is counted) and graph.Parse for
// a planned graph.json. The population is resolved nodes, never source lines.
//
// The four questions, in the order they are printed:
//
//	A. the corpus and its size — every graphs/*.yaml this repo ships, plus
//	   every planned graph found in the operator corpus under
//	   $OMG_HOME/runs (default ~/.oh-my-graph/runs);
//	B. how many of those graphs even have more than one lane, since a
//	   single-lane graph can produce no hit by construction;
//	C. the hits, each with graph path, the two node ids, their worktree
//	   values and the shared path token;
//	D. totals.
//
// The point is the NOISE, not the hits. The extraction rule below was fixed
// by the survey memo before this file existed and is NOT revised afterwards:
// a rule tuned until the number looked good would measure the tuning, not the
// predicate.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// ---------------------------------------------------------------------------
// THE PREDICATE — fixed, quoted from the survey memo section B(ii)
// ---------------------------------------------------------------------------
//
// For each RESOLVED graph:
//
//	P1. group nodes by their Worktree value; every distinct non-empty value is
//	    one LANE. Nodes with an empty Worktree belong to no lane.
//	P2. for each node, apply pathToken to `prompt` and take every match as a
//	    candidate path for that node's lane. (After LoadFile, `with:` bindings
//	    are already substituted into the prompt, so binding text is covered by
//	    reading the resolved prompt and nothing extra is needed for it.)
//	P3. for each unordered pair of DISTINCT lanes, intersect the two candidate
//	    sets. Every path in a non-empty intersection is one HIT.
//
// Comparison is only ever BETWEEN lanes: sharing a file inside one lane is
// the definition of a lane (dev writes it, e2e reads it), not a finding.
//
// A hit is (graph, lane pair, path). It is printed with one representative
// node from each lane — the lowest node id in that lane carrying the token —
// and the count of carriers when there is more than one.
var pathToken = regexp.MustCompile(`[A-Za-z0-9_./-]+\.(md|go|yaml|yml|json|txt)`)

// ---------------------------------------------------------------------------
// A. the corpus
// ---------------------------------------------------------------------------

// runsDir is $OMG_HOME/runs when OMG_HOME is set, else $HOME/.oh-my-graph/runs
// — the same rule the engine uses to place a run directory.
func runsDir() string {
	if home := os.Getenv("OMG_HOME"); home != "" {
		return filepath.Join(home, "runs")
	}
	return filepath.Join(os.Getenv("HOME"), ".oh-my-graph", "runs")
}

// loadedGraph is one member of the measured population, whatever it came from.
type loadedGraph struct {
	label  string // how a reader addresses it: repo path, or run id
	origin string // "shipped" or "planned"
	g      *graph.Graph
}

// skip reasons, reported separately rather than swallowed: an omission nobody
// is told about moves the numbers silently.
const (
	skipNoGraphJSON  = "no graph.json (a hand-written run writes none)"
	skipUnparseable  = "graph.json would not parse or would not validate"
	skipNoStateJSON  = "no state.json"
	skipNotSameGraph = "graph.json exists but state.json points elsewhere (hand-written run)"
)

type scanResult struct {
	planned []loadedGraph
	skipped map[string][]string // reason -> run ids
	seen    int
	// handwrittenSources are the distinct .yaml paths hand-written runs in the
	// corpus were launched from. NOT part of the measured population — reported
	// only so a reader can see what the corpus holds beyond it.
	handwrittenSources map[string]int
}

func (s scanResult) skippedCount() int {
	n := 0
	for _, ids := range s.skipped {
		n += len(ids)
	}
	return n
}

// scanRuns classifies every run directory under dir. A run is PLANNED exactly
// when it wrote a graph.json — the planner's output — which is the artefact
// this measurement can load; a hand-written `run` writes none and points its
// snapshot at the .yaml it was given.
func scanRuns(dir string) (scanResult, error) {
	s := scanResult{skipped: map[string][]string{}, handwrittenSources: map[string]int{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return s, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		s.seen++
		statePath := filepath.Join(dir, id, "state.json")
		graphPath := filepath.Join(dir, id, "graph.json")

		if _, err := os.Stat(statePath); err != nil {
			s.skipped[skipNoStateJSON] = append(s.skipped[skipNoStateJSON], id)
			continue
		}
		raw, err := os.ReadFile(graphPath)
		if err != nil {
			s.skipped[skipNoGraphJSON] = append(s.skipped[skipNoGraphJSON], id)
			if src := graphSourcePath(statePath); src != "" {
				s.handwrittenSources[src]++
			}
			continue
		}
		// JSON is YAML, so the repo's own Parse decodes and VALIDATES a
		// planned graph.json exactly as it would the equivalent YAML. A
		// planned graph carries no `use:` (the coordinator rejects fragment
		// references on a planned node), so there is nothing left to splice.
		g, err := graph.Parse(raw)
		if err != nil {
			s.skipped[skipUnparseable] = append(s.skipped[skipUnparseable], id)
			continue
		}
		s.planned = append(s.planned, loadedGraph{label: id, origin: "planned", g: g})
	}
	return s, nil
}

// graphSourcePath pulls `graph_source_path` out of a snapshot without pulling
// in the runstate package: only this one string is wanted, and only for the
// diagnostic line about what the corpus holds outside the population.
func graphSourcePath(statePath string) string {
	raw, err := os.ReadFile(statePath)
	if err != nil {
		return ""
	}
	const key = `"graph_source_path"`
	i := strings.Index(string(raw), key)
	if i < 0 {
		return ""
	}
	rest := string(raw)[i+len(key):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	rest = rest[j+1:]
	k := strings.Index(rest, `"`)
	if k < 0 {
		return ""
	}
	return rest[:k]
}

// ---------------------------------------------------------------------------
// B/C. lanes and hits
// ---------------------------------------------------------------------------

type lane struct {
	name  string
	paths map[string][]string // path token -> node ids carrying it, sorted
}

// lanesOf applies P1 and P2 to one resolved graph.
func lanesOf(g *graph.Graph) []lane {
	byName := map[string]map[string][]string{}
	// Nodes are visited in graph order; ids are sorted at the end so a hit's
	// representative node is deterministic.
	for _, n := range g.Nodes {
		if strings.TrimSpace(n.Worktree) == "" {
			continue // belongs to no lane
		}
		l, ok := byName[n.Worktree]
		if !ok {
			l = map[string][]string{}
			byName[n.Worktree] = l
		}
		for _, m := range pathToken.FindAllString(n.Prompt, -1) {
			l[m] = append(l[m], n.ID)
		}
	}
	var out []lane
	for name, paths := range byName {
		for p := range paths {
			ids := paths[p]
			sort.Strings(ids)
			paths[p] = dedupe(ids)
		}
		out = append(out, lane{name: name, paths: paths})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func dedupe(in []string) []string {
	var out []string
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

type hit struct {
	graph    string
	laneA    string
	laneB    string
	nodeA    string
	nodeB    string
	carriers int // total nodes across both lanes carrying the token
	path     string
}

// hitsIn applies P3 to one resolved graph's lanes.
func hitsIn(label string, lanes []lane) []hit {
	var out []hit
	for i := 0; i < len(lanes); i++ {
		for j := i + 1; j < len(lanes); j++ {
			a, b := lanes[i], lanes[j]
			for p, idsA := range a.paths {
				idsB, shared := b.paths[p]
				if !shared {
					continue
				}
				out = append(out, hit{
					graph: label, laneA: a.name, laneB: b.name,
					nodeA: idsA[0], nodeB: idsB[0],
					carriers: len(idsA) + len(idsB),
					path:     p,
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].laneA != out[j].laneA {
			return out[i].laneA < out[j].laneA
		}
		if out[i].laneB != out[j].laneB {
			return out[i].laneB < out[j].laneB
		}
		return out[i].path < out[j].path
	})
	return out
}

func main() {
	var population []loadedGraph

	// ---- A1. the shipped graphs ------------------------------------------
	// graphs/*.yaml only: graphs/fragments/*.yaml are fragments, not graphs —
	// they carry no `nodes:` top level and cannot be loaded as one.
	shipped, err := filepath.Glob(filepath.Join("graphs", "*.yaml"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "glob graphs/*.yaml: %v\n", err)
		os.Exit(1)
	}
	sort.Strings(shipped)
	var shippedFailed []string
	for _, path := range shipped {
		res, err := graph.LoadFile(path)
		if err != nil {
			shippedFailed = append(shippedFailed, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		population = append(population, loadedGraph{label: path, origin: "shipped", g: res.Graph})
	}

	// ---- A2. the operator corpus -----------------------------------------
	dir := runsDir()
	scan, scanErr := scanRuns(dir)
	if scanErr == nil {
		population = append(population, scan.planned...)
	}

	// ---- print A ---------------------------------------------------------
	fmt.Println("=== A. corpus ===")
	fmt.Printf("shipped graphs (graphs/*.yaml, loaded via graph.LoadFile — fragments resolved):\n")
	for _, lg := range population {
		if lg.origin != "shipped" {
			continue
		}
		fmt.Printf("    %-32s %2d resolved nodes\n", lg.label, len(lg.g.Nodes))
	}
	if len(shippedFailed) > 0 {
		fmt.Printf("  shipped graphs that would NOT load: %d\n", len(shippedFailed))
		for _, f := range shippedFailed {
			fmt.Printf("      %s\n", f)
		}
	}

	fmt.Printf("\noperator corpus: %s\n", dir)
	if scanErr != nil {
		fmt.Printf("  DIRECTORY NOT AVAILABLE: %v\n", scanErr)
		fmt.Printf("  reporting the shipped-graph population ALONE; no operator graph was measured.\n")
	} else {
		fmt.Printf("  run directories seen:            %d\n", scan.seen)
		fmt.Printf("  skipped:                         %d\n", scan.skippedCount())
		for _, reason := range []string{skipNoStateJSON, skipNoGraphJSON, skipUnparseable, skipNotSameGraph} {
			if n := len(scan.skipped[reason]); n > 0 {
				fmt.Printf("      %-56s %d\n", reason, n)
			}
		}
		fmt.Printf("  PLANNED graphs loaded:           %d\n", len(scan.planned))
		plannedNodes := 0
		for _, lg := range scan.planned {
			plannedNodes += len(lg.g.Nodes)
		}
		fmt.Printf("  resolved nodes in them:          %d\n", plannedNodes)

		// Not part of the population — stated so a reader can see the edge of
		// what was measured rather than guess at it.
		fmt.Printf("  (outside the population) distinct .yaml paths hand-written runs came from: %d\n",
			len(scan.handwrittenSources))
		var srcs []string
		for s := range scan.handwrittenSources {
			srcs = append(srcs, s)
		}
		sort.Strings(srcs)
		for _, s := range srcs {
			fmt.Printf("      %4d runs  %s\n", scan.handwrittenSources[s], s)
		}
	}
	fmt.Printf("\nPOPULATION: %d graphs", len(population))
	sh, pl, nodes := 0, 0, 0
	for _, lg := range population {
		if lg.origin == "shipped" {
			sh++
		} else {
			pl++
		}
		nodes += len(lg.g.Nodes)
	}
	fmt.Printf(" (%d shipped + %d planned), %d resolved nodes\n", sh, pl, nodes)

	// ---- print B ---------------------------------------------------------
	fmt.Println("\n=== B. how many graphs can produce a hit at all ===")
	withAnyLane, withTwoLanes := 0, 0
	nodesInLanes := 0
	var multi []loadedGraph
	laneCount := map[string][]lane{}
	for _, lg := range population {
		ls := lanesOf(lg.g)
		laneCount[lg.label] = ls
		for _, n := range lg.g.Nodes {
			if strings.TrimSpace(n.Worktree) != "" {
				nodesInLanes++
			}
		}
		if len(ls) >= 1 {
			withAnyLane++
		}
		if len(ls) >= 2 {
			withTwoLanes++
			multi = append(multi, lg)
		}
	}
	fmt.Printf("graphs declaring at least one lane (a non-empty worktree:):  %d of %d\n", withAnyLane, len(population))
	fmt.Printf("graphs declaring TWO OR MORE distinct lanes:                 %d of %d\n", withTwoLanes, len(population))
	fmt.Printf("resolved nodes belonging to some lane:                       %d of %d\n", nodesInLanes, nodes)
	fmt.Println("the multi-lane graphs (the only ones the predicate can fire on):")
	if len(multi) == 0 {
		fmt.Println("    (none)")
	}
	for _, lg := range multi {
		var names []string
		for _, l := range laneCount[lg.label] {
			names = append(names, l.name)
		}
		fmt.Printf("    %-32s [%s] (%s)\n", lg.label, strings.Join(names, " "), lg.origin)
	}

	// ---- print C ---------------------------------------------------------
	fmt.Println("\n=== C. hits ===")
	var all []hit
	for _, lg := range population {
		all = append(all, hitsIn(lg.label, laneCount[lg.label])...)
	}
	if len(all) == 0 {
		fmt.Println("    (no hit)")
	}
	for i, h := range all {
		fmt.Printf("  hit %d\n", i+1)
		fmt.Printf("    graph:        %s\n", h.graph)
		fmt.Printf("    lane %-8s node %s\n", h.laneA+":", h.nodeA)
		fmt.Printf("    lane %-8s node %s\n", h.laneB+":", h.nodeB)
		fmt.Printf("    shared path:  %s\n", h.path)
		fmt.Printf("    carriers:     %d nodes across the two lanes\n", h.carriers)
	}

	// ---- print D ---------------------------------------------------------
	fmt.Println("\n=== D. totals ===")
	fmt.Printf("graphs in population:                     %d\n", len(population))
	fmt.Printf("graphs that could fire (>= 2 lanes):      %d\n", withTwoLanes)
	fmt.Printf("HITS:                                     %d\n", len(all))
	fmt.Println("noise rate: NOT COMPUTABLE HERE — every hit above must be hand-checked")
	fmt.Println("against the graph it names; see the .md beside this file.")
}
