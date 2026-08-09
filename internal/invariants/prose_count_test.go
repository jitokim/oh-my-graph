// This file mechanizes the PROSE half of the exec-seam invariant that
// exec_seam_test.go already enforces mechanically.
//
// The allowlist in exec_seam_test.go is checked by a test, so it cannot drift.
// The sentences that RESTATE its result — "N objects in this codebase may spawn
// a process", "the N exec seams", "all N spawners" — were checked by nobody, and
// they duly drifted: an audit found copies still saying "two" and "three" long
// after the allowlist had grown, in package docs a reader is far likelier to
// meet than the test. A declaration nobody checks is not a mechanism, so these
// tests read the number back out of the prose and compare it to the number
// derived from the allowlist.
//
// Where the truth comes from
//
//	The count is DERIVED — from the distinct packages named in
//	allowedExecImporters, exec_seam_test.go's source of truth. There is
//	deliberately no second copy of the number anywhere in this file: a guard
//	that carried its own constant would be the very bug it is here to catch.
//	Distinct packages (not distinct files) is the right derivation because a
//	seam IS a package — the platform-specific procgroup files sit inside their
//	seam's package and add no spawner.
//
// What is deliberately NOT scanned
//
//	docs/adr/** and CHANGELOG.md are HISTORY. ADR 0002 says "exactly two
//	objects", ADR 0005 says "three", and a CHANGELOG entry records what a
//	release claimed at the time; every one of those sentences was true when it
//	was written and all of them are false today. They are excluded on purpose,
//	and the correct response to one of them tripping this test would be to fix
//	the exclusion, never to rewrite the record. A superseded ADR is amended
//	only in its Status line and its forward pointers.
//
//	This file is excluded too, for the same reason one step removed: it QUOTES
//	those historical counts to explain why they are excluded. Scanning itself
//	would make the guard demand that its own explanation of the drift be
//	falsified — the one repair the paragraph above forbids. Nothing is lost by
//	the exclusion: this file states no count of its own, it derives one.
//
//	README.ko.md is excluded because its claims are written in Korean
//	("정확히 네 개") and the number grammar below only reads English cardinals.
//	FOLLOW-UP: teach numberFromWord a Korean cardinal table, or drop the
//	exclusion once the translation states the count in digits.
//
//	Ordinals ("the fourth exec seam", "the second of the ... seams") are not
//	matched at all. They index a seam rather than counting the set, so they stay
//	true when a seam is added — browser.ExecOpener is the fourth seam whether or
//	not a fifth exists.
package invariants

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// englishCardinals is the number grammar shared by every claim pattern below.
// It is interpolated into the patterns rather than written inline so that the
// number words live in exactly one place — and, incidentally, so that this
// file's own source never puts a cardinal next to a spawner noun and matches
// itself.
const englishCardinals = `one|two|three|four|five|six|seven|eight|nine|ten|[0-9]+`

// spawnerCountClaims are the shapes an English sentence in this repo uses to
// state HOW MANY objects may spawn a process. Each pattern captures the count
// in the group named "n".
//
// The patterns are deliberately narrow — they anchor on a spawner noun rather
// than on mere proximity to the word "spawn". Proximity alone matches plenty of
// prose that is not a count claim at all ("the one command that spawns a real
// claude subprocess", "exactly one home per spawner", "every lifecycle emission
// behind one seam"), and a guard that cries wolf gets deleted.
var spawnerCountClaims = []*regexp.Regexp{
	// "... spawners" — "all N spawners", "the N OTHER spawners".
	claimPattern(`\b%s\s+(?:other\s+)?spawners\b`),
	// "... seams" — "the N exec seams", "the N exec-seam files", "the N seams".
	// Singular bare "seam" is left out: it is almost always a different seam
	// (the NodeRunner seam, the event-emission seam), not the exec-seam set.
	claimPattern(`\b%s\s+(?:exec[ -]seams?|seams)\b`),
	// "... process-spawning objects", "... spawn-site files".
	claimPattern(`\b%s\s+process-spawning\b`),
	claimPattern(`\b%s\s+spawn-site\b`),
	// "N objects ... spawn a process", "N objects ... touch os/exec". Bounded
	// by [^.] so the match cannot run past the end of the sentence.
	claimPattern(`\b%s\s+objects\b[^.]{0,80}?(?:spawn|exec)`),
	// The elliptical form the seam types use about themselves: "the only object
	// in this package that spawns a process, and one of exactly N in the
	// project".
	claimPattern(`\bone of exactly\s+%s\s+in the\b`),
}

// claimPattern compiles one claim shape, interpolating the number grammar and
// making the whole pattern case-insensitive (the repo shouts "FOUR" in a couple
// of places).
func claimPattern(format string) *regexp.Regexp {
	return regexp.MustCompile("(?i)" + fmt.Sprintf(format, `(?P<n>`+englishCardinals+`)`))
}

// otherLookback is how far before a match to look for the word "other", which
// turns a claim about the whole set into a claim about the set minus the
// speaker — a seam's own doc calling its siblings the OTHER spawners is right
// at one less than the total. Short on purpose: "other" modifies the number, so
// it sits immediately beside it. (No count is written out in this comment, so
// the comment is not itself a claim the test would then have to police.)
const otherLookback = 24

// historyExcluded are the repo's historical records, matched as path prefixes.
// They state what was true when they were written and must never be rewritten
// to match today's code — see this file's doc comment.
var historyExcluded = []string{
	"docs/adr/",
	"CHANGELOG.md",
}

// scanExcluded are paths skipped for reasons other than history: build output,
// git internals, the Korean README whose cardinals this file cannot read, and
// this file itself — see the doc comment's "What is deliberately NOT scanned".
var scanExcluded = []string{
	".git/",
	"bin/",
	"README.ko.md",
	selfRelPath(),
}

// selfRelPath is this file's own repo-relative path, taken from the compiler's
// record of it rather than transcribed, so renaming the file cannot silently
// turn the self-exclusion above into a dead string that excludes nothing. The
// two-level climb mirrors the repoRoot the tests walk from.
func selfRelPath() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	dir := filepath.Dir(file)
	return path.Join(filepath.Base(filepath.Dir(dir)), filepath.Base(dir), filepath.Base(file))
}

// TestProseSpawnerCountsMatchTheExecSeamAllowlist walks every Go, Markdown and
// YAML file in the repo (minus the exclusions above), finds each sentence that
// states how many objects may spawn a process, and fails when one of them
// disagrees with the count derived from allowedExecImporters.
//
// It also fails when a seam's own package carries no such sentence at all — a
// fifth seam that lands with a silent package doc is drift the count check
// alone would never see, because there is no wrong number to catch.
func TestProseSpawnerCountsMatchTheExecSeamAllowlist(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	seamPackages := execSeamPackages()
	want := len(seamPackages)

	// The two lists in exec_seam_test.go must agree before either can be a
	// source of truth for prose: execSeamCallSites is one file per seam, so a
	// seam added to the allowlist and forgotten there would quietly shrink the
	// derived count and let stale prose pass. The comparison is set against set,
	// not length against length — a duplicated entry keeps the tally right while
	// some seam's call site goes unlisted, and the scrub test in
	// exec_seam_test.go would then walk right past that seam.
	callSitesByPackage := map[string]int{}
	for _, file := range execSeamCallSites {
		callSitesByPackage[path.Dir(file)]++
	}
	for _, pkg := range seamPackages {
		if n := callSitesByPackage[pkg]; n != 1 {
			t.Fatalf("execSeamCallSites names %d spawn-site file(s) under %s, want exactly one per seam; "+
				"allowedExecImporters spans %d exec seam(s) %v. The seam count cannot be derived while the "+
				"two lists disagree — add that seam's spawn-site file to execSeamCallSites (or drop the "+
				"duplicate entry) before trusting any prose check.", n, pkg, want, seamPackages)
		}
		delete(callSitesByPackage, pkg)
	}
	unlisted := make([]string, 0, len(callSitesByPackage))
	for pkg := range callSitesByPackage {
		unlisted = append(unlisted, pkg)
	}
	sort.Strings(unlisted)
	if len(unlisted) > 0 {
		t.Fatalf("execSeamCallSites names spawn sites under %v, which allowedExecImporters does not cover; "+
			"it spans %d exec seam(s) %v. The seam count cannot be derived while the two lists disagree — "+
			"drop the stale entry, or allowlist the new seam (with its ADR) first.",
			unlisted, want, seamPackages)
	}

	claimsByPackage := map[string]int{}
	for _, file := range proseFiles(t, repoRoot) {
		for _, claim := range findSpawnerCountClaims(file) {
			claimsByPackage[path.Dir(file.rel)]++
			expected := claim.want(want)
			if expected == claim.got {
				continue
			}
			// The number the sentence must carry, not the seam total: an
			// "other" claim is right at one BELOW the total, and printing the
			// total there would read as "4, but 4" and prove nothing.
			form := ""
			if claim.excludesSelf {
				form = ", minus the speaker the word \"other\" excludes"
			}
			t.Errorf("%s:%d says %q — a spawner count of %d, but it must say %d: allowedExecImporters spans "+
				"%d exec seam(s) %v%s. The allowlist is the enforced truth; this sentence is a copy of it that "+
				"drifted. Update the sentence (and its sibling copies) to match, or, if a seam really was added "+
				"or removed, update the allowlist and its ADR first.",
				file.rel, file.lineAt(claim.start), claim.text, claim.got, expected, want, seamPackages, form)
		}
	}

	for _, pkg := range seamPackages {
		if claimsByPackage[pkg] == 0 {
			t.Errorf("package %s owns an exec seam but no file in it states how many objects may spawn a "+
				"process. Every seam's package doc says so, and that sentence is what a reader of the seam "+
				"meets instead of internal/invariants. Add it — see the sibling seam packages for the wording.", pkg)
		}
	}
}

// execSeamPackages derives the exec seams from allowedExecImporters by
// collapsing its files to their packages, sorted. A seam is a package: each of
// the seam packages holds a single spawn-site file plus, for some, build-tagged
// procgroup helpers that mutate an already-built *exec.Cmd without spawning
// anything of their own. Counting files would count those helpers as spawners;
// counting packages does not.
func execSeamPackages() []string {
	seen := map[string]bool{}
	for file := range allowedExecImporters {
		seen[path.Dir(file)] = true
	}
	pkgs := make([]string, 0, len(seen))
	for pkg := range seen {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)
	return pkgs
}

// countClaim is one prose statement of a spawner count: the number it states,
// the text it states it in, and whether it is a claim about the whole set or
// about the set minus the speaker.
type countClaim struct {
	got          int
	text         string
	start        int
	excludesSelf bool
}

// want returns the number this claim should carry given a seam total.
func (c countClaim) want(total int) int {
	if c.excludesSelf {
		return total - 1
	}
	return total
}

// findSpawnerCountClaims returns every spawner-count claim in a flattened file,
// deduplicated by offset so a sentence matched by two patterns is reported once.
func findSpawnerCountClaims(file flatFile) []countClaim {
	byStart := map[int]countClaim{}
	for _, re := range spawnerCountClaims {
		numGroup := re.SubexpIndex("n")
		for _, loc := range re.FindAllStringSubmatchIndex(file.text, -1) {
			n, ok := numberFromWord(file.text[loc[2*numGroup]:loc[2*numGroup+1]])
			if !ok {
				continue
			}
			start := loc[0]
			if _, dup := byStart[start]; dup {
				continue
			}
			lookback := start - otherLookback
			if lookback < 0 {
				lookback = 0
			}
			byStart[start] = countClaim{
				got:          n,
				text:         file.text[start:loc[1]],
				start:        start,
				excludesSelf: strings.Contains(strings.ToLower(file.text[lookback:loc[1]]), "other"),
			}
		}
	}
	claims := make([]countClaim, 0, len(byStart))
	for _, c := range byStart {
		claims = append(claims, c)
	}
	sort.Slice(claims, func(i, j int) bool { return claims[i].start < claims[j].start })
	return claims
}

// numberFromWord reads an English cardinal, or a bare decimal, as an int.
func numberFromWord(word string) (int, bool) {
	words := map[string]int{
		"one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
		"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
	}
	if n, ok := words[strings.ToLower(word)]; ok {
		return n, true
	}
	n, err := strconv.Atoi(word)
	return n, err == nil
}

// flatFile is one file's prose collapsed onto a single line, with a map back to
// the source line each byte came from. Flattening is what lets a claim that
// wraps across source lines — and, in Go, across `//` comment leaders — match
// as the one sentence it is.
type flatFile struct {
	rel   string
	text  string
	lines []int
}

// lineAt reports the 1-based source line the flattened byte at offset came from.
func (f flatFile) lineAt(offset int) int {
	if offset < 0 || offset >= len(f.lines) {
		return 0
	}
	return f.lines[offset]
}

// flattenProse joins a file's non-blank lines with single spaces, stripping the
// leading markup that would otherwise land in the middle of a wrapped sentence:
// Go's `//`, Markdown's `>` and `#`.
func flattenProse(rel string, src []byte) flatFile {
	var b strings.Builder
	var lines []int
	for i, raw := range strings.Split(string(src), "\n") {
		line := i + 1
		s := strings.TrimSpace(raw)
		for _, leader := range []string{"//", "#", ">"} {
			s = strings.TrimPrefix(s, leader)
		}
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
			lines = append(lines, line)
		}
		b.WriteString(s)
		for j := 0; j < len(s); j++ {
			lines = append(lines, line)
		}
	}
	return flatFile{rel: rel, text: b.String(), lines: lines}
}

// proseFiles walks the repo and returns every Go, Markdown and YAML file that
// prose claims are policed in — everything except the historical records and
// the non-prose paths named above. Test files are INCLUDED: a comment in
// claude_test.go restates the count just as a package doc does, and drifts the
// same way.
func proseFiles(t *testing.T, repoRoot string) []flatFile {
	t.Helper()

	var files []flatFile
	err := filepath.WalkDir(repoRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(repoRoot, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if excludedFromProseScan(rel, d.IsDir()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		switch filepath.Ext(p) {
		case ".go", ".md", ".yaml", ".yml":
		default:
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files = append(files, flattenProse(rel, src))
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repo for prose: %v", err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	return files
}

// excludedFromProseScan reports whether a repo-relative path is off limits,
// matching the exclusions as path prefixes so a directory entry and everything
// under it fall out together.
func excludedFromProseScan(rel string, isDir bool) bool {
	if rel == "." {
		return false
	}
	probe := rel
	if isDir {
		probe += "/"
	}
	for _, prefix := range append(append([]string{}, historyExcluded...), scanExcluded...) {
		if probe == prefix || strings.HasPrefix(probe, prefix) || probe == strings.TrimSuffix(prefix, "/") {
			return true
		}
	}
	return false
}

// fenceCallSiteClaim matches internal/fence/fence.go's statement of how many
// callers share the nonce fence: "Three call sites share Nonce: ...".
var fenceCallSiteClaim = claimPattern(`\b%s\s+call[ -]sites\b`)

// TestFenceCallSiteCountMatchesTheCode is the same idea as the spawner-count
// check, much smaller in scope. fence.go's package doc counts the callers of
// fence.Nonce, and that sentence had drifted too — it said "Two" until a human
// happened to notice a third. So count the real calls and hold the sentence to
// them.
//
// The count is taken REPO-WIDE, not over one package. It used to be scoped to
// internal/coordinator, which was right while the fence lived there and every
// caller was a sibling; internal/schedule quotes a rejected attempt back to the
// node that produced it (ADR 0020), so a package-scoped count would now miss a
// caller and let the sentence quietly go stale by one.
//
// Callers are matched syntactically as `fence.Nonce(...)`, which is what every
// caller in this repo writes. An import alias would slip past — noted rather
// than solved, because resolving imports for the whole repo to catch a spelling
// nobody uses is a lot of machinery for a guard whose whole value is that it is
// cheap enough to keep.
//
// The claim is read from fence.go alone: "call sites" is ordinary English that
// other files use about entirely different sets, and the claim being guarded is
// the one fence.go makes about its own helper.
func TestFenceCallSiteCountMatchesTheCode(t *testing.T) {
	const (
		pkgDir  = "internal/fence"
		helper  = "fence.Nonce"
		docFile = "fence.go"
	)
	repoRoot := filepath.Join("..", "..")

	callers := 0
	fset := token.NewFileSet()
	err := filepath.WalkDir(repoRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(repoRoot, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if excludedFromProseScan(rel, d.IsDir()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		// Test files are excluded on purpose: a fixture minting its own nonce
		// is not a place the engine fences untrusted text, and the doc comment
		// names production call sites.
		if d.IsDir() || filepath.Ext(p) != ".go" || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, p, nil, 0)
		if parseErr != nil {
			return fmt.Errorf("parsing %s: %w", rel, parseErr)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if ok && pkg.Name+"."+sel.Sel.Name == helper {
				callers++
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repo for %s callers: %v", helper, err)
	}

	src, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(pkgDir), docFile))
	if err != nil {
		t.Fatalf("reading %s/%s: %v", pkgDir, docFile, err)
	}
	flat := flattenProse(pkgDir+"/"+docFile, src)

	numGroup := fenceCallSiteClaim.SubexpIndex("n")
	locs := fenceCallSiteClaim.FindAllStringSubmatchIndex(flat.text, -1)
	if len(locs) != 1 {
		t.Fatalf("%s states its %s call-site count %d time(s), want once. The doc comment above the fence "+
			"is where that count lives; this test cannot hold a claim it cannot find.", flat.rel, helper, len(locs))
	}
	loc := locs[0]
	got, ok := numberFromWord(flat.text[loc[2*numGroup]:loc[2*numGroup+1]])
	if !ok {
		t.Fatalf("%s: cannot read %q as a number.", flat.rel, flat.text[loc[2*numGroup]:loc[2*numGroup+1]])
	}
	if got != callers {
		t.Errorf("%s:%d says %q, but %s is called from %d place(s) in the repo. Every caller has to mint its "+
			"own nonce for the fence to be unforgeable, so the doc comment naming them is the map a reader "+
			"uses to check that; update it.", flat.rel, flat.lineAt(loc[0]), flat.text[loc[0]:loc[1]], helper, callers)
	}
}
