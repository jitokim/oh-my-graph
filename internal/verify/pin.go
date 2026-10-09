package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The --verify-cmd is the user's engine-run build evidence (ADR 0016/0030), and
// a command is only evidence while the files it names are the ones the user
// meant. A planned node holding the stock Edit grant can rewrite `./check.sh`,
// and the run would then record a verified PASS that the script it ran no
// longer earned (#363). So the files a command line names are pinned at launch
// — word, resolved path, content digest — and a verification whose pinned file
// changed is a fault that names the file, never a result.
//
// This file pins and checks; it spawns nothing and imports no os/exec
// (internal/invariants). There is no snapshot copy of a pinned file: the check
// can only refuse a changed file, never run the original in its place.

// PinnedFile is one file a pinned command line names, as it was at launch.
type PinnedFile struct {
	// Word is the shell word as written on the command line, quotes removed.
	Word string
	// Path is the absolute path Word resolved to, every symlink followed.
	Path string
	// Digest is the hex SHA-256 of the file's content.
	Digest string
}

// PinCommand pins, per command segment of command (split on && || ; | and the
// other control operators), the one file that segment executes, resolved
// against dir:
//
//   - Leading NAME=value words and the wrappers env, exec, command, nice and
//     time (by basename, so /usr/bin/env too) are skipped to the command word.
//   - If the command word's basename is an interpreter (sh, bash, python3.12,
//     node, ...), a segment holding -c pins nothing (an inline script);
//     otherwise its first following word that resolves to a file is pinned and
//     nothing after it is (those are the script's data files). The interpreter
//     binary itself is never pinned.
//   - Any other command word is pinned only if it contains a slash
//     (./verify.sh, /abs/x); a bare name found on PATH (go, make, tee) is the
//     user's toolchain, not a file a node in this tree is expected to edit.
//
// Every argument the command reads or writes (`| tee log.txt`, `--junitxml
// log.txt`, `grep -q done status.txt`) stays unpinned: the command may rewrite
// it itself (#367). Paths resolve against dir only — a `cd` inside the command
// is not tracked. A word that names no existing, readable regular file is
// skipped, not an error.
func PinCommand(command, dir string) []PinnedFile {
	var pins []PinnedFile
	seen := map[string]bool{}
	for _, segment := range commandSegments(command) {
		word, path, digest, ok := executedFile(segment, dir)
		if !ok || seen[word] {
			continue
		}
		seen[word] = true
		pins = append(pins, PinnedFile{Word: word, Path: path, Digest: digest})
	}
	return pins
}

// commandWrappers run the word after them as the command (#367).
var commandWrappers = map[string]bool{"env": true, "exec": true, "command": true, "nice": true, "time": true}

// interpreters run the script file named by their first file argument (#367).
var interpreters = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "python": true, "python3": true,
	"node": true, "ruby": true, "perl": true,
}

// executedFile is the file one command segment executes, if any: the script
// an interpreter is handed, or a command word written as a path.
func executedFile(segment []string, dir string) (word, path, digest string, ok bool) {
	i := 0
	for i < len(segment) && (isAssignment(segment[i]) || commandWrappers[filepath.Base(segment[i])]) {
		i++
	}
	if i == len(segment) {
		return "", "", "", false
	}
	command := segment[i]
	if base := filepath.Base(command); interpreters[base] || strings.HasPrefix(base, "python3.") {
		for _, arg := range segment[i+1:] {
			if arg == "-c" {
				return "", "", "", false
			}
		}
		for _, arg := range segment[i+1:] {
			if path, digest, err := digestWord(arg, dir); err == nil {
				return arg, path, digest, true
			}
		}
		return "", "", "", false
	}
	if !strings.Contains(command, "/") {
		return "", "", "", false
	}
	path, digest, err := digestWord(command, dir)
	return command, path, digest, err == nil
}

// isAssignment reports whether word is a NAME=value environment assignment.
func isAssignment(word string) bool {
	name, _, found := strings.Cut(word, "=")
	if !found || name == "" || (name[0] >= '0' && name[0] <= '9') {
		return false
	}
	for _, r := range name {
		if r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// PinChangedError is a verification refused because a file its command line
// names is no longer the one pinned at launch. It replaces any Result, so the
// command's own exit status can never turn a changed script into a PASS.
type PinChangedError struct {
	Command string
	// Path is the pinned file's absolute, symlink-resolved path.
	Path string
	// Reason says what changed: a re-pointed path, a missing or unreadable
	// file, or different content.
	Reason string
}

// Error names the file LAST: a node's recorded detail keeps only the tail of
// a long fault (the scheduler's capDetail), and the file is the one part a
// reader cannot recover from anything else on the record.
func (e *PinChangedError) Error() string {
	return fmt.Sprintf("verify command %q cannot be evidence: %s; pinned file %s changed since launch",
		e.Command, e.Reason, e.Path)
}

// PinningVerifier wraps a Verifier and checks a pinned command's files
// immediately before and immediately after every inner verification. It
// guards every Request rather than only one whose Command equals the pinned
// string: the scheduler interpolates {{ }} tokens before the verifier sees the
// command, so an equality test would let `./check.sh {{ self.timeout }}` run
// unchecked. It is only ever wrapped around a verifier that runs nothing but
// the pinned command — the auto baseline, a planned graph's sinks (no planned
// node may carry a verify of its own), and their resume.
type PinningVerifier struct {
	inner   Verifier
	command string
	pins    []PinnedFile
}

// NewPinningVerifier returns a Verifier that guards command's pins around
// inner. Each pinned word is re-resolved against the directory the
// verification runs in: Request.Cwd, or the process's own when that is empty.
func NewPinningVerifier(inner Verifier, command string, pins []PinnedFile) *PinningVerifier {
	copied := make([]PinnedFile, len(pins))
	copy(copied, pins)
	return &PinningVerifier{inner: inner, command: command, pins: copied}
}

// Verify checks the pins, delegates, and checks them again. A pre-check fault
// runs nothing; a post-check fault discards whatever the inner verifier
// returned, exit 0 included.
func (p *PinningVerifier) Verify(ctx context.Context, req Request) (Result, error) {
	if len(p.pins) == 0 {
		return p.inner.Verify(ctx, req)
	}
	dir, err := verifyDir(req.Cwd)
	if err != nil {
		return Result{}, fmt.Errorf("verify command %q: cannot resolve its pinned files: %w", req.Command, err)
	}
	if err := p.check(dir); err != nil {
		return Result{}, err
	}
	result, verifyErr := p.inner.Verify(ctx, req)
	if err := p.check(dir); err != nil {
		return Result{}, err
	}
	return result, verifyErr
}

// check re-resolves every pin against dir, returning the first that changed.
func (p *PinningVerifier) check(dir string) error {
	for _, pin := range p.pins {
		path, digest, err := digestWord(pin.Word, dir)
		switch {
		case err != nil:
			return &PinChangedError{Command: p.command, Path: pin.Path, Reason: fmt.Sprintf("it is gone or unreadable (%v)", err)}
		case path != pin.Path:
			return &PinChangedError{Command: p.command, Path: pin.Path, Reason: fmt.Sprintf("%q now resolves to %s", pin.Word, path)}
		case digest != pin.Digest:
			return &PinChangedError{Command: p.command, Path: pin.Path, Reason: fmt.Sprintf("its content changed (sha256 %s at launch, %s now)", pin.Digest, digest)}
		}
	}
	return nil
}

// verifyDir is the directory a Request's relative words resolve against.
func verifyDir(cwd string) (string, error) {
	if cwd == "" {
		return os.Getwd()
	}
	return filepath.Abs(cwd)
}

// digestWord resolves word against dir, follows every symlink, and hashes the
// regular file it names. The Stat comes before the Open so a FIFO is refused
// rather than blocked on.
func digestWord(word, dir string) (path, digest string, err error) {
	abs := word
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(dir, word)
	}
	// Absolute BEFORE resolving: EvalSymlinks leaves a relative path relative,
	// and Abs then prefixes the cwd as $PWD spells it, symlinks and all (on
	// macOS /tmp, not /private/tmp).
	if abs, err = filepath.Abs(abs); err != nil {
		return "", "", err
	}
	if path, err = filepath.EvalSymlinks(abs); err != nil {
		return "", "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", "", err
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("%s is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", "", err
	}
	return path, hex.EncodeToString(h.Sum(nil)), nil
}

// shellOperatorChars separate words and are never part of one: the control
// operators (&& || ; | &), grouping, and redirection.
const shellOperatorChars = "&|;<>()\n"

// commandSegments splits a command line into its commands' shell words:
// whitespace separates, single and double quotes group (and are removed), a
// backslash outside single quotes escapes the next character, and a run of
// operator characters is a separator, not a word. A control operator (&& || ;
// | & and grouping) also ends the segment; one inside quotes is just text. It
// is a word splitter for pinning, not a shell — it expands nothing, so a
// `$VAR` word simply names no file.
//
// The word after a redirection (`>`, `>>`, `<`, `2>&1`'s `>&`) is dropped: it
// is not an argument, and pinning a target the command writes would fault
// every run that redirects into a file the last run left behind.
func commandSegments(command string) [][]string {
	var (
		segments   [][]string
		words      []string
		cur        strings.Builder
		inWord     bool
		skipNext   bool
		quote      rune
		escaped    bool
		dqEscaped  bool
		inOperator bool
		operator   strings.Builder
	)
	endWord := func() {
		if inWord {
			if !skipNext {
				words = append(words, cur.String())
			}
			skipNext = false
		}
		cur.Reset()
		inWord = false
	}
	endSegment := func() {
		if len(words) > 0 {
			segments = append(segments, words)
		}
		words = nil
		skipNext = false
	}
	endOperator := func() {
		switch op := operator.String(); {
		case !inOperator:
		case strings.ContainsAny(op, "<>"):
			skipNext = true
		default:
			endSegment()
		}
		operator.Reset()
		inOperator = false
	}
	for _, r := range command {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case dqEscaped:
			// Inside double quotes a backslash escapes only these; before
			// anything else it is a literal backslash.
			if !strings.ContainsRune("$`\"\\\n", r) {
				cur.WriteRune('\\')
			}
			cur.WriteRune(r)
			dqEscaped = false
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case quote == '"':
			switch r {
			case '"':
				quote = 0
			case '\\':
				dqEscaped = true
			default:
				cur.WriteRune(r)
			}
		case strings.ContainsRune(shellOperatorChars, r):
			endWord()
			inOperator = true
			operator.WriteRune(r)
		case r == ' ' || r == '\t' || r == '\r':
			endWord()
			endOperator()
		default:
			endOperator()
			inWord = true
			switch r {
			case '\'', '"':
				quote = r
			case '\\':
				escaped = true
			default:
				cur.WriteRune(r)
			}
		}
	}
	endWord()
	endOperator()
	endSegment()
	return segments
}
