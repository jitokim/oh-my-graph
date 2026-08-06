// The skill SCAN: after a plan has been validated — and after subagent
// auto-mapping (agentmap.go) has run and rebuilt the graph — the coordinator,
// trusted Go code, never the planner LLM, reads the user's own Claude Code
// skill definitions (~/.claude/skills/*/SKILL.md; v1 deliberately never scans
// a project directory — ADR 0012, Alternatives). What it does with them is
// skillstage.go's: the corpus is staged into a plugin directory the run owns
// and the node's own model activates by description at run time (ADR 0017).
//
// This file used to also MAP one skill onto one node by name and inline its
// body into that node's prompt (ADR 0012). That mechanism is gone, and with it
// the name-token matcher, the 16 KiB cap, the '{{' neutralization, the nonce
// fence around inlined bodies and SkillMapping itself: it recovered 7% of the
// node ids it was measured over, one of the five mappings it made was
// semantically wrong, and it was never established that an inlined body helps
// a node at all (ADR 0017 §8). The two mechanisms must never coexist in a
// shipped build — a node holding both would receive the same skill twice, pay
// for it twice, and become unattributable — so the deletion rides with the
// replacement rather than being kept as a fallback.
//
// What survives is exactly what activation needs: the scan itself, its
// silence, and its disclosure. Filesystem scan failures — missing directories,
// unreadable files, broken frontmatter, a blank name, an empty body — are
// silent no-activation, never an error: zero-config stays zero-config. The
// directories scanned are the caller's to choose (WithSkillDirs; the CLI
// passes DefaultSkillDirs, tests pass temp dirs), so a Coordinator built
// without them never touches the filesystem at all. Opt out entirely with
// `--no-skill-activation` (WithoutSkillActivation).
// See docs/adr/0017-planned-nodes-get-skill-activation-not-inlined-skill-text.md.

package coordinator

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultSkillDirs is where the CLI has the coordinator scan for skill
// definitions: the user's ~/.claude/skills only. Two other places a skill can
// live are deliberately absent, both cut in ADR 0012's Alternatives and both
// disclosed in the plan printout (see SkillScan) rather than left as silence:
//
//   - <cwd>/.claude/skills — 100% of the genuinely new injection surface (a
//     cloned repository shipping instructions into unattended dontAsk nodes)
//     for 0% measured yield;
//   - ~/.claude/plugins/... — plugin-provided skills, deferred: measured on
//     the 2026-08-04 corpus (20 live plugin skills against the 32 node ids in
//     the shipped graphs/) they add ZERO mappings, and the same ADR's standard
//     for cutting the project scan was "new surface for 0% measured yield".
//     Scanning them is a decision to make with a number, once the yield is
//     observable; the conditions it would have to meet are recorded in the
//     ADR.
//
// Both cuts survive ADR 0017 unchanged and for the same reasons: staging a
// project checkout's skills would put a cloned repository's instructions in
// front of every unattended node, which is a larger hole under activation than
// it was under a 7% matcher, not a smaller one.
//
// A home that cannot be resolved just drops out (the scan is silent about
// missing directories anyway).
func DefaultSkillDirs() []string {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".claude", "skills"))
	}
	return dirs
}

// maxSkillFileBytes bounds any single file read out of a skill directory —
// the scan's and the stager's only otherwise-unbounded per-file cost. It is
// deliberately far above any plausible SKILL.md (the largest in the corpus
// ADR 0012 measured is 86.6 KiB, and under activation that one is reachable
// rather than capped), so only a pathological file is refused here, and
// silently, like any other file the scan cannot use.
const maxSkillFileBytes = 1 << 20

// SkillScan records that a skill scan HAPPENED and over what — the datum the
// plan printout needs to tell "your skills were read and none matched" apart
// from "your skills were never looked at", which are indistinguishable when
// the only output is a list of decisions and the list is empty. Silence is the
// common case, so silence must not also be the failure display.
//
// Dirs is what was scanned, in scan order — LATER WINS in scanSkillDirs, so a
// directory further down the list shadows an earlier one holding the same
// skill name, matching how DefaultAgentDirs describes its own order. Found is
// how many usable skill definitions came back. Found 0 with a non-empty Dirs
// is the diagnosable case — the directory is named, so a missing tree, an
// empty one, or a corpus that lives somewhere the scan does not go (a plugin,
// a project checkout) is one printed line away instead of a guess.
//
// Found is also activation's whole predicate (ADR 0017 §1): a user with no
// ~/.claude/skills pays nothing for a capability there is nothing to exercise.
// That is a per-RUN predicate over a filesystem fact, deliberately not a
// per-node guess about relevance — any rule good enough to decide "this node
// needs this skill" in advance is a skill selector, and the only selector this
// project has measured ran at 7%.
//
// Shadowed is the path of every definition that lost a name collision, in the
// order the scan met them. It exists because Found is the size of the map
// AFTER dedup: two skill directories declaring `name: babysit` are one
// definition here, so without this the count silently disagrees with the
// number of directories on disk and nothing anywhere says which file was
// dropped. Empty is the normal case.
type SkillScan struct {
	Dirs     []string
	Found    int
	Shadowed []string
}

// skillDef is one parsed SKILL.md: the frontmatter fields the printout reads
// plus where the file came from. The body is parsed only far enough to prove
// the file is a usable skill — under activation nothing inlines it, so it is
// not retained.
type skillDef struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`

	// path is the SKILL.md the definition was read from. Its DIRECTORY is the
	// unit the stager copies, since a skill's bundled files (references/ and
	// anything else beside it) are reachable through the CLI's own progressive
	// disclosure and are part of the corpus.
	path string
}

// dir is the skill's own directory — what gets staged, whole.
func (d skillDef) dir() string { return filepath.Dir(d.path) }

// scanSkillDirs reads every <dir>/*/SKILL.md under dirs, in order, later
// definitions overwriting earlier ones on a name collision (mirroring
// scanAgentDirs; DefaultSkillDirs passes one directory, but the precedence
// shape is kept so tests and a future measured project scan need no new
// mechanism). Every failure — a missing directory, an unreadable file,
// frontmatter that does not parse, a blank or whitespace-carrying name, an
// empty body — skips just that much and stays silent: a broken skill file
// must not break `auto`.
//
// A collision is NOT one of those silences: the losing file's path is returned
// alongside the map, because losing is invisible from the map itself and it
// moves the count the printout shows. Two same-named skills inside a single
// directory tree collide too — `name:` need not equal the directory name — and
// there the winner is just whichever os.ReadDir returned later.
//
// Directory-ness is decided by os.Stat, not entry.IsDir(): os.ReadDir does not
// follow symlinks, so a `~/.claude/skills/<name>` symlinked out to a dotfiles
// checkout — how skills are commonly kept under version control — would
// otherwise be invisible here while the equivalent symlinked agent .md already
// scans fine (scanAgentDirs filters on suffix, not on IsDir).
func scanSkillDirs(dirs []string) (map[string]skillDef, []string) {
	skills := make(map[string]skillDef)
	var shadowed []string
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			info, err := os.Stat(filepath.Join(dir, entry.Name()))
			if err != nil || !info.IsDir() {
				continue
			}
			def, ok := parseSkillFile(filepath.Join(dir, entry.Name(), "SKILL.md"))
			if !ok {
				continue
			}
			if prev, clash := skills[def.Name]; clash {
				shadowed = append(shadowed, prev.path)
			}
			skills[def.Name] = def
		}
	}
	return skills, shadowed
}

// parseSkillFile extracts the YAML frontmatter of one SKILL.md and proves the
// file has a body. The format is Claude Code's own: a leading `---` line, YAML
// fields, a closing `---` line, then the skill's instructions. ok is false for
// anything that is not that shape, including an empty body (the CLI would have
// nothing to activate) and a file over maxSkillFileBytes.
func parseSkillFile(path string) (skillDef, bool) {
	f, err := os.Open(path)
	if err != nil {
		return skillDef{}, false
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxSkillFileBytes+1))
	if err != nil || len(raw) > maxSkillFileBytes {
		return skillDef{}, false
	}
	content := strings.TrimPrefix(string(raw), "\ufeff")
	rest, found := strings.CutPrefix(content, "---\n")
	if !found {
		if rest, found = strings.CutPrefix(content, "---\r\n"); !found {
			return skillDef{}, false
		}
	}
	front, body, found := strings.Cut(rest, "\n---")
	if !found {
		return skillDef{}, false
	}
	var def skillDef
	if err := yaml.Unmarshal([]byte(front), &def); err != nil {
		return skillDef{}, false
	}
	if def.Name == "" || strings.ContainsAny(def.Name, " \t\n") {
		return skillDef{}, false
	}
	if strings.TrimSpace(body) == "" {
		return skillDef{}, false
	}
	def.path = path
	return def, true
}
