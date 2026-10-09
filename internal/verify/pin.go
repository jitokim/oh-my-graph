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

// PinCommand pins every word of command that names an existing, readable
// regular file once resolved against dir (relative words are joined to it,
// absolute ones are used as they are). A word that names nothing pinnable — a
// missing path, a directory, an unreadable file, a bare program name — is
// skipped, not an error: there is no PATH lookup, and a program found on PATH
// is the user's toolchain, not a file a node in this tree is expected to edit.
func PinCommand(command, dir string) []PinnedFile {
	var pins []PinnedFile
	seen := map[string]bool{}
	for _, word := range commandWords(command) {
		if seen[word] {
			continue
		}
		seen[word] = true
		path, digest, err := digestWord(word, dir)
		if err != nil {
			continue
		}
		pins = append(pins, PinnedFile{Word: word, Path: path, Digest: digest})
	}
	return pins
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

// commandWords splits a command line into shell words: whitespace separates,
// single and double quotes group (and are removed), a backslash outside single
// quotes escapes the next character, and a run of operator characters is a
// separator, not a word. It is a word splitter for pinning, not a shell — it
// expands nothing, so a `$VAR` word simply names no file.
//
// The word after an output redirection (`>`, `>>`, `2>&1`'s `>&`) is dropped:
// the command writes it, so pinning it would fault every run that redirects
// into a file the last run left behind.
func commandWords(command string) []string {
	var (
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
	endOperator := func() {
		if inOperator && strings.Contains(operator.String(), ">") {
			skipNext = true
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
	return words
}
