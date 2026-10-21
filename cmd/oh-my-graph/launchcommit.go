package main

import (
	"os"
	"path/filepath"
	"strings"
)

// launchCommit returns the full HEAD sha of the git repository containing
// dir, or "" — silently — when dir is in no repository or anything about it
// cannot be read. `auto` and `design` call it once at launch and hand the
// result to coordinator.WithStartCommit, so every cycle of one goal measures
// scope from the same commit (#414).
//
// It reads the git directory itself instead of spawning `git rev-parse HEAD`
// because of the exec-seam invariant: exactly four objects may spawn a
// process (ADR 0002, 0005, 0006, held by internal/invariants), and the CLI is
// not one of them. Only what a launch sha needs is handled: a `.git`
// directory or `gitdir:` file, a detached or symbolic HEAD, loose refs in the
// gitdir and the commondir (a worktree), and packed-refs.
func launchCommit(dir string) string {
	gitDir := findGitDir(dir)
	if gitDir == "" {
		return ""
	}
	commonDir := gitDir
	if data, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		commonDir = resolveFrom(gitDir, strings.TrimSpace(string(data)))
	}
	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	value := strings.TrimSpace(string(head))
	// A loose ref may itself be symbolic; a few hops is more than git writes.
	for range 5 {
		ref, ok := strings.CutPrefix(value, "ref:")
		if !ok {
			if isFullSHA(value) {
				return value
			}
			return ""
		}
		value = lookupRef(gitDir, commonDir, strings.TrimSpace(ref))
		if value == "" {
			return ""
		}
	}
	return ""
}

// findGitDir walks up from dir to the first `.git`, following a `gitdir:`
// file to the directory it names.
func findGitDir(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(abs, ".git")
		if info, err := os.Stat(candidate); err == nil {
			if info.IsDir() {
				return candidate
			}
			data, err := os.ReadFile(candidate)
			if err != nil {
				return ""
			}
			path, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
			if !ok {
				return ""
			}
			return resolveFrom(abs, strings.TrimSpace(path))
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return ""
		}
		abs = parent
	}
}

// lookupRef reads ref as a loose ref in the gitdir, then in the commondir,
// then in the commondir's packed-refs.
func lookupRef(gitDir, commonDir, ref string) string {
	if ref == "" {
		return ""
	}
	for _, base := range []string{gitDir, commonDir} {
		if data, err := os.ReadFile(filepath.Join(base, filepath.FromSlash(ref))); err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	data, err := os.ReadFile(filepath.Join(commonDir, "packed-refs"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "^") {
			continue
		}
		sha, name, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok && name == ref {
			return sha
		}
	}
	return ""
}

func resolveFrom(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(base, path)
}

// isFullSHA accepts a SHA-1 (40) or SHA-256 (64) object name in hex.
func isFullSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// launchCommitOfWorkingDir is launchCommit on the invocation directory, ""
// when even that cannot be found.
func launchCommitOfWorkingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return launchCommit(wd)
}
