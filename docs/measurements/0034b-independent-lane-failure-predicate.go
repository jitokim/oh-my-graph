//go:build ignore

// Measure the candidate "rule 5" predicate from the batch-lane survey memo:
// "independent lanes fail independently" (graphs/backlog-batch.yaml:71-74).
//
// This is a MEASUREMENT, not a lint. It ships no behaviour and nothing in the
// engine calls it. The `ignore` build tag above keeps it out of
// `go build ./...` and `go test ./...`; run it explicitly:
//
//	go run docs/measurements/0034b-independent-lane-failure-predicate.go
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
// The corpus assembly here is 0034-lane-file-ownership-predicate.go's, reused
// unchanged, because the survey memo's section B named that reuse as the
// condition for promoting this predicate.
//
// The four questions, in the order they are printed:
//
//	A. the corpus and its size — every graphs/*.yaml this repo ships, plus
//	   every planned graph found in the operator corpus under
//	   $OMG_HOME/runs (default ~/.oh-my-graph/runs);
//	B. how many of those graphs even have more than one component, since a
//	   single-component graph can produce no hit by construction;
//	C. the hits, each with graph label, its on_fail value, and the full
//	   component decomposition a reader needs to hand-check it;
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
	"sort"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// ---------------------------------------------------------------------------
// THE PREDICATE — fixed, quoted from the survey memo section B
// ---------------------------------------------------------------------------
//
// "the resolved graph's `depends_on` undirected closure yields two or more
//  weakly connected components, and the graph does not declare
//  `on_fail: continue` — one warning on the GRAPH, not on a node."
//
// For each RESOLVED graph:
//
//	P1. take every node as a vertex and every `depends_on` entry as an edge,
//	    read UNDIRECTED (a lane is connected whichever way its arrows point);
//	P2. compute the weakly connected components of that closure;
//	P3. HIT when len(components) >= 2 AND !g.ContinuesOnFail().
//
// Fields read: Node.DependsOn (internal/graph/graph.go:252) and Graph.OnFail
// via Graph.ContinuesOnFail (internal/graph/graph.go:373, :467). ZERO bytes of
// prompt prose are read — which is the whole reason this candidate is worth
// measuring where #213's was not.
//
// One hit is one GRAPH, not one node and not one component: the warning the
// predicate would emit is a single line about the graph's failure policy.
//
// EXEMPTION, and it is the only one: a graph whose closure has exactly one
// component cannot hit. Rule 5 says nothing about such a graph — it has no
// independent lanes to fail independently — so the premise is absent rather
// than satisfied.
//
// A LIMIT that is NOT an exemption and cannot be removed by any static
// checker: `--continue-on-fail` at the command line ORs with the graph's own
// value (internal/schedule/scheduler.go:1665, and Graph.ContinuesOnFail's own
// doc comment says so). A load-time warning can therefore be false about the
// run that actually happens. It is reported, not silently dropped.

// component is one weakly connected component of a resolved graph.
type component struct {
	nodes []string // sorted node ids
}

// componentsOf applies P1 and P2 to one resolved graph via union-find.
func componentsOf(g *graph.Graph) []component {
	parent := map[string]string{}
	var find func(string) string
	find = func(x string) string {
		p, ok := parent[x]
		if !ok || p == x {
			return x
		}
		r := find(p)
		parent[x] = r
		return r
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}
	for _, n := range g.Nodes {
		if _, ok := parent[n.ID]; !ok {
			parent[n.ID] = n.ID
		}
	}
	for _, n := range g.Nodes {
		for _, dep := range n.DependsOn {
			if _, ok := parent[dep]; !ok {
				// A validated graph cannot reach here (an edge to an unknown
				// node is a load error), but the probe must not invent a
				// vertex silently if it ever does.
				parent[dep] = dep
			}
			union(n.ID, dep)
		}
	}
	byRoot := map[string][]string{}
	for _, n := range g.Nodes {
		r := find(n.ID)
		byRoot[r] = append(byRoot[r], n.ID)
	}
	var out []component
	for _, ids := range byRoot {
		sort.Strings(ids)
		out = append(out, component{nodes: ids})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].nodes[0] < out[j].nodes[0] })
	return out
}

// ---------------------------------------------------------------------------
// A. the corpus — assembly reused verbatim from
//    docs/measurements/0034-lane-file-ownership-predicate.go
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
// C. hits
// ---------------------------------------------------------------------------

type hit struct {
	graph      string
	origin     string
	onFail     string // the raw declared value, "" when undeclared
	nodes      int
	components []component
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
		fmt.Printf("    %-32s %2d resolved nodes   on_fail=%q\n",
			lg.label, len(lg.g.Nodes), lg.g.OnFail)
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
		declaring := 0
		for _, lg := range scan.planned {
			plannedNodes += len(lg.g.Nodes)
			if lg.g.ContinuesOnFail() {
				declaring++
			}
		}
		fmt.Printf("  resolved nodes in them:          %d\n", plannedNodes)
		fmt.Printf("  planned graphs declaring on_fail: continue: %d\n", declaring)
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
	comps := map[string][]component{}
	multi := 0
	byCount := map[int]int{}
	var multiGraphs []loadedGraph
	for _, lg := range population {
		cs := componentsOf(lg.g)
		comps[lg.label] = cs
		byCount[len(cs)]++
		if len(cs) >= 2 {
			multi++
			multiGraphs = append(multiGraphs, lg)
		}
	}
	fmt.Printf("graphs whose depends_on closure has TWO OR MORE components: %d of %d\n", multi, len(population))
	fmt.Println("component-count distribution over the whole population:")
	var counts []int
	for c := range byCount {
		counts = append(counts, c)
	}
	sort.Ints(counts)
	for _, c := range counts {
		fmt.Printf("    %2d component(s): %3d graphs\n", c, byCount[c])
	}
	fmt.Println("the multi-component graphs (the only ones the predicate can fire on):")
	if len(multiGraphs) == 0 {
		fmt.Println("    (none)")
	}
	for _, lg := range multiGraphs {
		fmt.Printf("    %-46s %d components, %2d nodes, on_fail=%q (%s)\n",
			lg.label, len(comps[lg.label]), len(lg.g.Nodes), lg.g.OnFail, lg.origin)
	}

	// ---- print C ---------------------------------------------------------
	fmt.Println("\n=== C. hits (>= 2 components AND no on_fail: continue) ===")
	var all []hit
	for _, lg := range population {
		cs := comps[lg.label]
		if len(cs) < 2 || lg.g.ContinuesOnFail() {
			continue
		}
		all = append(all, hit{
			graph: lg.label, origin: lg.origin, onFail: lg.g.OnFail,
			nodes: len(lg.g.Nodes), components: cs,
		})
	}
	if len(all) == 0 {
		fmt.Println("    (no hit)")
	}
	for i, h := range all {
		fmt.Printf("  hit %d\n", i+1)
		fmt.Printf("    graph:      %s (%s)\n", h.graph, h.origin)
		fmt.Printf("    on_fail:    %q  (%d nodes, %d components)\n", h.onFail, h.nodes, len(h.components))
		for j, c := range h.components {
			fmt.Printf("    component %d (%d nodes): %s\n", j+1, len(c.nodes), strings.Join(c.nodes, ", "))
		}
	}

	// ---- print D ---------------------------------------------------------
	fmt.Println("\n=== D. totals ===")
	fmt.Printf("graphs in population:                       %d\n", len(population))
	fmt.Printf("graphs that could fire (>= 2 components):   %d\n", multi)
	fmt.Printf("HITS:                                       %d\n", len(all))
	shHits, plHits := 0, 0
	for _, h := range all {
		if h.origin == "shipped" {
			shHits++
		} else {
			plHits++
		}
	}
	fmt.Printf("    of which shipped:                       %d\n", shHits)
	fmt.Printf("    of which planned:                       %d\n", plHits)
	fmt.Println("noise rate: NOT COMPUTABLE HERE — every hit above must be hand-checked")
	fmt.Println("against the graph it names; see the .md beside this file.")
}
