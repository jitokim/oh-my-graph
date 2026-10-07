package main

import (
	"fmt"
	"io"
	"sort"

	"github.com/jitokim/oh-my-graph/internal/fence"
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
func undeclaredInputWarnings(declared []string, sources map[string]string) []string {
	keys := undeclaredInputKeys(declared, sources)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		line := fmt.Sprintf("input %q (from %s) is not declared in the graph's inputs list; it is bound anyway",
			key, fence.SanitizeTerminalLine(sources[key]))
		if near, ok := nearestDeclaredInput(key, declared); ok {
			line += fmt.Sprintf(" — did you mean %q?", near)
		}
		lines = append(lines, line)
	}
	return lines
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
// The ones the cycle's planned graph does not declare are added to warned on
// the way out, so across the loop each undeclared key warns once — on the
// first cycle whose plan leaves it out — and a key a later plan declares
// costs nothing. warned is the loop's own state, owned by its caller.
func unwarnedInputSources(declared []string, sources map[string]string, warned map[string]bool) map[string]string {
	pending := make(map[string]string, len(sources))
	for key, source := range sources {
		if !warned[key] {
			pending[key] = source
		}
	}
	for _, key := range undeclaredInputKeys(declared, pending) {
		warned[key] = true
	}
	return pending
}

// warnUndeclaredInputs prints undeclaredInputWarnings on warnW through
// warnLine, so each carries the "warning: <graph path>: " prefix the other
// load-time warnings do — and the bare "warning: " form on a plan screen whose
// spec has no path yet.
func warnUndeclaredInputs(warnW io.Writer, graphPath string, declared []string, sources map[string]string) {
	for _, line := range undeclaredInputWarnings(declared, sources) {
		warnLine(warnW, graphPath, line)
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
