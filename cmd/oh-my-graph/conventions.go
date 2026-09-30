package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/conventions"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// conventionsFlag is `auto --conventions <path>`, repeatable, in command-line
// order — which is the order the files appear in every node's prompt
// (ADR 0041 §2.1). Registered on `auto` alone: `run` nodes load the operator's
// CLAUDE.md natively, `chat` must not change an unattended run behind a [y/N],
// and `resume` inherits the staged copy rather than re-pointing it (§2.6).
type conventionsFlag []string

func (f *conventionsFlag) String() string { return strings.Join(*f, ",") }

func (f *conventionsFlag) Set(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("invalid --conventions %q (want a file path)", path)
	}
	*f = append(*f, path)
	return nil
}

// conventionsDisclosure is what the plan screen says about the conventions: the
// validated set, and whether the saved graph is being previewed rather than
// run. The zero value prints nothing, which is every run without the flag.
type conventionsDisclosure struct {
	set *conventions.Set
	// notCarried is `--plan-only`: the preview's natural next step is
	// `run <graph.json>`, which does not prefix them (ADR 0041 §2.4).
	notCarried bool
}

// noteConventions prints ADR 0041 §2.4's operator-screen lines: the full
// paths and hashes the node's own prompt deliberately does not carry. One
// writer for `auto`, every goal-loop cycle, `--plan-only` and a resumed leg.
func noteConventions(w io.Writer, d conventionsDisclosure) {
	set := d.set
	if set == nil {
		return
	}
	files := fmt.Sprintf("%d file", len(set.Sources))
	if len(set.Sources) != 1 {
		files += "s"
	}
	if d.notCarried {
		fmt.Fprintf(w, "  Your conventions (%s, %s bytes) are NOT in the saved graph: `run <graph.json>` does not prefix them. Launch with `auto` to apply them.\n",
			files, groupThousands(set.TotalBytes()))
	} else {
		fmt.Fprintf(w, "  Planned nodes are prefixed with your conventions (%s, %s bytes, sha256 %s) — text only: no settings, grants, hooks or MCP servers come with it.\n",
			files, groupThousands(set.TotalBytes()), shortHash(set.SHA256()))
	}
	for i, src := range set.Sources {
		line := fmt.Sprintf("    %d/%d  %s  %s bytes  sha256 %s", i+1, len(set.Sources), src.Path, groupThousands(src.Bytes), shortHash(src.SHA256))
		switch src.ImportLines {
		case 0:
		case 1:
			line += "  (1 @-import line not followed)"
		default:
			line += fmt.Sprintf("  (%d @-import lines not followed)", src.ImportLines)
		}
		fmt.Fprintln(w, line)
	}
}

func shortHash(hex string) string {
	if len(hex) > 12 {
		return hex[:12]
	}
	return hex
}

// groupThousands renders n with comma thousands separators, as the plan
// screen prints byte counts. n is a size, never negative.
func groupThousands(n int) string {
	digits := strconv.Itoa(n)
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// conventionsRecord is the state.json half of a set: the staged hash `resume`
// checks and the sources the screen reprints. nil for no set, which keeps the
// snapshot at schema 3 (runstate.SchemaWithConventions).
func conventionsRecord(set *conventions.Set) *runstate.Conventions {
	if set == nil {
		return nil
	}
	record := &runstate.Conventions{StagedSHA256: set.SHA256(), Sources: make([]runstate.ConventionsSource, len(set.Sources))}
	for i, src := range set.Sources {
		record.Sources[i] = runstate.ConventionsSource{Path: src.Path, Bytes: src.Bytes, SHA256: src.SHA256, ImportLines: src.ImportLines}
	}
	return record
}

// resumedConventions re-reads a run's staged conventions for a resumed leg,
// refusing a missing or altered copy (ADR 0041 §2.6). nil, nil for a run that
// had none.
func resumedConventions(runDir string, record *runstate.Conventions) (*conventions.Set, error) {
	if record == nil {
		return nil, nil
	}
	sources := make([]conventions.Source, len(record.Sources))
	for i, src := range record.Sources {
		sources[i] = conventions.Source{Path: src.Path, Bytes: src.Bytes, SHA256: src.SHA256, ImportLines: src.ImportLines}
	}
	return conventions.LoadStaged(runDir, sources, record.StagedSHA256)
}

// conventionsPrefix is the scheduler's Options.Conventions for a set: its
// staged bytes, or "" for none.
func conventionsPrefix(set *conventions.Set) string {
	if set == nil {
		return ""
	}
	return string(set.Staged)
}
