// Package conventions carries an operator's coding conventions into an
// auto-planned node as TEXT (ADR 0041).
//
// A planned node runs under ceiling layer 1, `--setting-sources ""`, and so
// never loads the operator's CLAUDE.md: the CLI cannot load a settings source
// without also loading the standing permission grants that live beside it.
// `auto --conventions <path>` is the narrow door beside ADR 0032's wide one —
// the operator names files, the files' bytes are validated at launch, staged
// into the run directory, and prefixed to every spawn that resumes nothing.
// Nothing else comes with them: no setting, no grant, no hook, no MCP server.
//
// The package does three things and spawns nothing:
//
//   - Load reads and validates the operator's list, whole or not at all
//     (ADR 0041 §2.5). Every refusal names a path and a reason, never content.
//   - Set.Stage writes the rendered prefix into a run directory as
//     conventions.md; the staged bytes are exactly what every node receives.
//   - LoadStaged re-reads a staged copy for `resume` and refuses one whose
//     SHA-256 no longer matches the one the first leg recorded (§2.6).
package conventions

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// MaxTotalBytes is the cap on the TOTAL size of the named files, sized from
// #282's measured corpus: five docs of about 17k tokens, roughly 68 KiB, and
// 128 KiB is about 1.9× that (ADR 0041 §2.5). It bounds a bill every fresh
// spawn pays. A move goes through ADR 0041 §7(4), not a quiet constant change.
const MaxTotalBytes = 128 * 1024

// StagedFileName is the staged copy's name inside a run directory.
const StagedFileName = "conventions.md"

// header opens the prefix. It names the text as the operator's conventions,
// not as quoted data: these are instructions the node is meant to follow, so
// they are deliberately NOT fenced (ADR 0041 §2.3).
const header = "The person who launched this run gave these conventions for every node. Follow them.\n\n"

// separator ends the prefix; the node's own prompt follows it unchanged.
const separator = "---\n\n"

// Source is one operator-named file as the launch validated it. Path is the
// absolute path, for the operator's screen and state.json only — the node's
// prompt carries the basename alone (ADR 0041 §2.4).
type Source struct {
	Path        string
	Bytes       int
	SHA256      string
	ImportLines int
}

// Set is a validated conventions list and the prefix rendered from it. Staged
// is the exact byte string every fresh spawn is prefixed with, and the exact
// content of the staged conventions.md.
type Set struct {
	Sources []Source
	Staged  []byte
}

// SHA256 is the hex SHA-256 of the staged prefix — the hash `resume` checks.
func (s *Set) SHA256() string {
	return sha256Hex(s.Staged)
}

// TotalBytes is the summed size of the source files, the figure the cap is
// on and the one the plan screen prints.
func (s *Set) TotalBytes() int {
	total := 0
	for _, src := range s.Sources {
		total += src.Bytes
	}
	return total
}

// RefusalError is a launch-time refusal of the --conventions list. It names
// the path and the reason and never quotes a file's content.
type RefusalError struct {
	Path   string
	Reason string
}

func (e *RefusalError) Error() string {
	if e.Path == "" {
		return "--conventions: " + e.Reason
	}
	return fmt.Sprintf("--conventions %s: %s", e.Path, e.Reason)
}

// StagedMismatchError is `resume`'s refusal of a staged copy that is missing
// or no longer hashes to what the first leg recorded. Continuing would tell
// this leg's nodes something different from their siblings (ADR 0041 §2.6).
type StagedMismatchError struct {
	Path  string
	Want  string
	Found string
}

func (e *StagedMismatchError) Error() string {
	found := e.Found
	if found == "" {
		found = "missing"
	}
	return fmt.Sprintf("staged conventions %s: recorded sha256 %s, found %s; a resumed leg's nodes would be told "+
		"something different from the nodes that already ran, so this run cannot be resumed", e.Path, e.Want, found)
}

type readFile struct {
	source   Source
	resolved string
	content  []byte
}

// Load reads every named file in order, validates each and the list as a
// whole, and renders the prefix. An empty list is a caller bug, not a flag
// the operator typed, and is refused so it can never render a header with no
// conventions under it.
func Load(paths []string) (*Set, error) {
	if len(paths) == 0 {
		return nil, &RefusalError{Reason: "no file named"}
	}
	files := make([]readFile, 0, len(paths))
	seen := map[string]string{}
	for _, path := range paths {
		file, err := readOne(path)
		if err != nil {
			return nil, err
		}
		if first, dup := seen[file.resolved]; dup {
			return nil, &RefusalError{Path: path, Reason: fmt.Sprintf("names the same file as %s; each file is listed once", first)}
		}
		seen[file.resolved] = path
		files = append(files, file)
	}
	total := 0
	for _, f := range files {
		total += f.source.Bytes
	}
	if total > MaxTotalBytes {
		sizes := make([]string, len(files))
		for i, f := range files {
			sizes[i] = fmt.Sprintf("%s %d bytes", f.source.Path, f.source.Bytes)
		}
		return nil, &RefusalError{Reason: fmt.Sprintf(
			"the named files total %d bytes, over the %d-byte cap (%s); nothing is truncated, so shorten the set on purpose",
			total, MaxTotalBytes, strings.Join(sizes, ", "))}
	}
	set := &Set{Sources: make([]Source, len(files))}
	for i, f := range files {
		set.Sources[i] = f.source
	}
	set.Staged = render(files)
	return set, nil
}

func readOne(path string) (readFile, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return readFile{}, &RefusalError{Path: path, Reason: fmt.Sprintf("cannot resolve the path: %v", err)}
	}
	info, err := os.Stat(abs)
	if err != nil {
		return readFile{}, &RefusalError{Path: path, Reason: "cannot be read: " + describeStatError(err)}
	}
	if info.IsDir() {
		return readFile{}, &RefusalError{Path: path, Reason: "is a directory; name each file"}
	}
	if !info.Mode().IsRegular() {
		return readFile{}, &RefusalError{Path: path, Reason: "is not a regular file"}
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return readFile{}, &RefusalError{Path: path, Reason: "cannot be read: " + describeStatError(err)}
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return readFile{}, &RefusalError{Path: path, Reason: "cannot be read: " + describeStatError(err)}
	}
	if !utf8.Valid(content) {
		return readFile{}, &RefusalError{Path: path, Reason: "is not valid UTF-8"}
	}
	if strings.TrimSpace(string(content)) == "" {
		return readFile{}, &RefusalError{Path: path, Reason: "is blank; a flag that delivers nothing is a typo"}
	}
	targets, onlyImports := importLines(content)
	if onlyImports {
		return readFile{}, &RefusalError{Path: path, Reason: fmt.Sprintf(
			"holds nothing but @-import lines, which are not followed; pass the imported files instead: --conventions %s",
			strings.Join(targets, " --conventions "))}
	}
	return readFile{
		source:   Source{Path: abs, Bytes: len(content), SHA256: sha256Hex(content), ImportLines: len(targets)},
		resolved: resolved,
		content:  content,
	}, nil
}

// describeStatError drops the path os already prefixed to err, since the
// RefusalError names it once.
func describeStatError(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err.Error()
	}
	return err.Error()
}

// importLines returns the targets of every CLAUDE.md-style `@path` import line
// in content, and whether every non-blank line is one. An import line is a
// trimmed line that starts with `@` and holds a single whitespace-free token.
func importLines(content []byte) (targets []string, onlyImports bool) {
	onlyImports = true
	for _, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if len(trimmed) > 1 && trimmed[0] == '@' && !strings.ContainsAny(trimmed, " \t") {
			targets = append(targets, trimmed[1:])
			continue
		}
		onlyImports = false
	}
	return targets, onlyImports
}

// render builds the prefix of ADR 0041 §2.4: the header, each file under its
// ordinal and basename, and the separator. No path and no hash reach it.
func render(files []readFile) []byte {
	var b bytes.Buffer
	b.WriteString(header)
	for i, f := range files {
		fmt.Fprintf(&b, "## Conventions %d/%d: %s\n", i+1, len(files), filepath.Base(f.source.Path))
		b.Write(f.content)
		if !bytes.HasSuffix(f.content, []byte("\n")) {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	b.WriteString(separator)
	return b.Bytes()
}

// Stage writes the prefix into runDir as conventions.md, owner-only like
// every other run artifact that can hold the operator's private text.
func (s *Set) Stage(runDir string) error {
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return fmt.Errorf("stage conventions: %w", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, StagedFileName), s.Staged, 0o600); err != nil {
		return fmt.Errorf("stage conventions: %w", err)
	}
	return nil
}

// LoadStaged re-reads runDir's staged copy for a resumed leg and returns it as
// a Set carrying the recorded sources. A missing file, or one whose SHA-256 is
// not wantSHA, is a *StagedMismatchError.
func LoadStaged(runDir string, sources []Source, wantSHA string) (*Set, error) {
	path := filepath.Join(runDir, StagedFileName)
	staged, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, &StagedMismatchError{Path: path, Want: wantSHA}
		}
		return nil, fmt.Errorf("read staged conventions: %w", err)
	}
	if got := sha256Hex(staged); got != wantSHA {
		return nil, &StagedMismatchError{Path: path, Want: wantSHA, Found: got}
	}
	return &Set{Sources: sources, Staged: staged}, nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
