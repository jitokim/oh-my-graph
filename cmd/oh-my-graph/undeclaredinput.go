package main

import (
	"fmt"
	"io"
	"slices"
	"sort"

	"github.com/jitokim/oh-my-graph/internal/fence"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/handoff"
)

// undeclaredInputWarnings is #356's check: one line for each bound key the
// graph does not declare in its `inputs:` list, sorted by key. It warns and
// never refuses — the key stays bound, the run and its exit status are exactly
// what they would have been without it.
//
// It takes the declared names and each bound key's WINNING source (the one
// commonRunFlags.inputSources records: "--input", or the --input-file path
// that bound it last) and nothing else. The values are not a parameter, so no
// line can print one by construction: an input may carry a token. The key is
// quoted with %q, as the precedence line quotes it, so a file key carrying an
// ESC sequence prints as \x1b and never raw; the source path goes through
// fence.SanitizeTerminalLine for the same reason.
//
// A line reads
//
//	input "reop" (from in.yaml) is not declared in the graph's inputs list; it is bound anyway — did you mean "repo"?
//
// and the "— did you mean" tail appears only for a near miss: the declared
// name at the smallest Levenshtein distance (counted in runes) from the key,
// within 2 when the key is 4 runes or longer, within 1 when it is 3 runes, and
// never for a key of 2 runes or fewer, which is too short for a guess to mean
// anything. Several declared names at the same smallest distance: the
// alphabetically first wins.
//
// A graph with NO `inputs:` declaration declares nothing, so every bound key
// is undeclared and warns. That is the reading `lint` already gives a missing
// list (handoff.judgeToken against placeholderFindings' declared set, built
// from Graph.Inputs alone): every {{ inputs.<name> }} in such a graph is
// reported as an input the graph does not declare. Skipping the check there
// would make `run` silent about a key `lint` would call undeclared the moment
// a prompt referenced it.
//
// This is `run`'s and `run --dry-run`'s rule only. `auto`'s plan screen uses
// nearMissInputWarnings instead: there the planner saw every bound input and
// may simply not need one, so a key the planned graph leaves undeclared is not
// a typo signal — only a key one edit or two away from a name the plan uses is.
func undeclaredInputWarnings(declared []string, sources map[string]string) []string {
	keys := undeclaredInputKeys(declared, sources)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		near, ok := nearestDeclaredInput(key, declared)
		lines = append(lines, undeclaredInputLine(key, sources[key], near, ok))
	}
	return lines
}

// undeclaredInputLine is the one wording both rules print, with the
// "— did you mean" tail when hasNear.
func undeclaredInputLine(key, source, near string, hasNear bool) string {
	line := fmt.Sprintf("input %q (from %s) is not declared in the graph's inputs list; it is bound anyway",
		key, fence.SanitizeTerminalLine(source))
	if hasNear {
		line += fmt.Sprintf(" — did you mean %q?", near)
	}
	return line
}

// plannedInputNames is what `auto` judges a bound key against (#356): the
// union of the names a planned graph declares in `inputs:` and the names it
// references as a well-formed {{ inputs.<name> }} anywhere the engine
// interpolates (handoff.InputReferences). A planner often references an input
// without declaring it, and a typo of a referenced name is as much a typo as
// one of a declared name. A reuse citation's bind: values need no extra
// look: the coordinator splices them into the nodes' fields before the plan
// is returned, so they are already in what InputReferences scans.
func plannedInputNames(g *graph.Graph) []string {
	return slices.Compact(slices.Sorted(slices.Values(append(slices.Clone(g.Inputs), handoff.InputReferences(g)...))))
}

// nearMissInputWarnings is #356's check on `auto`'s plan screen: one line,
// in undeclaredInputWarnings' wording, for each bound key that is not itself
// in known but is a near miss of a name that is (nearestDeclaredInput's rule),
// sorted by key. known is plannedInputNames. A key that matches a known name,
// or is near none — including every key when the plan declares and references
// nothing — prints nothing: the planner saw every bound input and may simply
// not need it, so on `auto` undeclared alone is not a typo signal.
func nearMissInputWarnings(known []string, sources map[string]string) []string {
	var lines []string
	for _, key := range nearMissInputKeys(known, sources) {
		near, _ := nearestDeclaredInput(key, known)
		lines = append(lines, undeclaredInputLine(key, sources[key], near, true))
	}
	return lines
}

// nearMissInputKeys is the bound keys nearMissInputWarnings prints a line
// for, sorted.
func nearMissInputKeys(known []string, sources map[string]string) []string {
	var keys []string
	for _, key := range undeclaredInputKeys(known, sources) {
		if _, ok := nearestDeclaredInput(key, known); ok {
			keys = append(keys, key)
		}
	}
	return keys
}

// undeclaredInputKeys is the bound keys of sources that declared does not
// name, sorted: the set undeclaredInputWarnings prints a line for.
func undeclaredInputKeys(declared []string, sources map[string]string) []string {
	isDeclared := make(map[string]bool, len(declared))
	for _, name := range declared {
		isDeclared[name] = true
	}
	keys := make([]string, 0, len(sources))
	for key := range sources {
		if !isDeclared[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// unwarnedInputSources is a goal loop's per-cycle view of sources (#356):
// every bound key not yet in warned, for that cycle's plan screen to judge.
// known is the cycle's plannedInputNames. The keys that plan screen prints a
// near-miss line for (nearMissInputKeys) are added to warned on the way out,
// and only those: a key that is merely undeclared prints nothing on `auto`,
// so it is not spent, and a later cycle whose plan uses a name it nearly
// matches still warns. Across the loop each near-miss key warns once.
// warned is the loop's own state, owned by its caller.
func unwarnedInputSources(known []string, sources map[string]string, warned map[string]bool) map[string]string {
	pending := make(map[string]string, len(sources))
	for key, source := range sources {
		if !warned[key] {
			pending[key] = source
		}
	}
	for _, key := range nearMissInputKeys(known, pending) {
		warned[key] = true
	}
	return pending
}

// warnUndeclaredInputs prints undeclaredInputWarnings on warnW through
// warnLine, so each carries the "warning: <graph path>: " prefix the other
// load-time warnings do. `run` and `run --dry-run` call it.
func warnUndeclaredInputs(warnW io.Writer, graphPath string, declared []string, sources map[string]string) {
	for _, line := range undeclaredInputWarnings(declared, sources) {
		warnLine(warnW, graphPath, line)
	}
}

// warnNearMissInputs prints nearMissInputWarnings against g's
// plannedInputNames on w through warnLine — the bare "warning: " form on a
// plan screen whose spec has no path yet. `auto`'s plan screen calls it.
func warnNearMissInputs(w io.Writer, specPath string, g *graph.Graph, sources map[string]string) {
	for _, line := range nearMissInputWarnings(plannedInputNames(g), sources) {
		warnLine(w, specPath, line)
	}
}

// nearestDeclaredInput is the near-miss rule undeclaredInputWarnings documents.
func nearestDeclaredInput(key string, declared []string) (string, bool) {
	var limit int
	switch n := len([]rune(key)); {
	case n >= 4:
		limit = 2
	case n == 3:
		limit = 1
	default:
		return "", false
	}
	best, bestDist := "", limit+1
	for _, name := range declared {
		d := levenshtein(key, name)
		if d < bestDist || (d == bestDist && name < best) {
			best, bestDist = name, d
		}
	}
	return best, bestDist <= limit
}

// levenshtein is the edit distance between a and b, counted in runes:
// insertions, deletions and substitutions each cost 1.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
