package worktree

import (
	"context"
	"regexp"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// maxReasonBytes bounds an unavailable part's reason. git's diagnosis is one
// or two lines; the bound only keeps a chatty failure from bloating a
// snapshot that is rewritten after every node.
const maxReasonBytes = 500

// TakeRemoteSnapshot reads the invocation repository's remote state through
// obs (ADR 0046 §2.1): the configured remotes, then `ls-remote --heads` for
// each. It never fails: a read that errors — not a repository, a remote
// that cannot be reached, a timeout — makes its part unavailable with the
// reason, and a run carries on exactly as it would have. The branches part
// is unavailable when the remotes part is, and when any one remote's refs
// could not be read, since a list missing one remote would compare as that
// remote's branches all gone.
//
// URLs, and the reasons that may quote them, have any credential in them
// replaced before they are recorded.
func TakeRemoteSnapshot(ctx context.Context, obs RemoteObserver) runstate.RemoteSnapshot {
	var snap runstate.RemoteSnapshot

	remotes, err := obs.Remotes(ctx)
	if err != nil {
		reason := summarizeReason(err.Error())
		snap.Remotes = runstate.RemotesSnapshot{Status: runstate.RemotePartUnavailable, Reason: reason}
		snap.Branches = runstate.BranchesSnapshot{Status: runstate.RemotePartUnavailable,
			Reason: "the remotes part is unavailable: " + reason}
		return snap
	}
	snap.Remotes.Status = runstate.RemotePartObserved
	for _, r := range remotes {
		snap.Remotes.Configured = append(snap.Remotes.Configured, runstate.RemoteConfig{
			Name: r.Name, FetchURL: redactURLCredentials(r.FetchURL), PushURL: redactURLCredentials(r.PushURL),
		})
	}

	var failures []string
	var refs []runstate.RemoteBranchRef
	for _, r := range remotes {
		heads, err := obs.LsRemoteHeads(ctx, r.Name)
		if err != nil {
			failures = append(failures, "ls-remote "+r.Name+": "+err.Error())
			continue
		}
		for _, h := range heads {
			refs = append(refs, runstate.RemoteBranchRef{Remote: r.Name, Branch: h.Branch, SHA: h.SHA})
		}
	}
	if len(failures) > 0 {
		snap.Branches = runstate.BranchesSnapshot{Status: runstate.RemotePartUnavailable,
			Reason: summarizeReason(strings.Join(failures, "; "))}
		return snap
	}
	snap.Branches = runstate.BranchesSnapshot{Status: runstate.RemotePartObserved, Refs: refs}
	return snap
}

// RunCommitCheck adapts obs's §5.1 check to the shape
// runstate.RemoteChanges.EndLeg takes, against the run's recorded base.
func RunCommitCheck(ctx context.Context, obs RemoteObserver, baseSHA string) func(sha string) bool {
	return func(sha string) bool { return obs.CommitAttributable(ctx, sha, baseSHA) }
}

var (
	// httpUserinfo is the whole userinfo of an http(s) URL: there the user
	// name alone is often a token (https://<token>@github.com/...).
	httpUserinfo = regexp.MustCompile(`(?i)\b(https?://)[^/\s@]+@`)
	// urlPassword is the password of any other scheme's userinfo; the user
	// name (git@ in ssh://git@host) is kept.
	urlPassword = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://[^/\s:@]*):[^/\s@]+@`)
)

// redactURLCredentials replaces the credentials a URL may carry, wherever
// one appears in s.
func redactURLCredentials(s string) string {
	s = httpUserinfo.ReplaceAllString(s, "${1}***@")
	return urlPassword.ReplaceAllString(s, "${1}:***@")
}

// summarizeReason makes git's diagnosis one bounded, credential-free line.
func summarizeReason(s string) string {
	s = redactURLCredentials(strings.Join(strings.Fields(s), " "))
	if len(s) <= maxReasonBytes {
		return s
	}
	cut := maxReasonBytes
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + " …"
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
