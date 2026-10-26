package coordinator

import (
	"fmt"

	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// The two attribution labels of a remote change entry (ADR 0046 §2.2), word
// for word. Every entry is "observed": the snapshot sees state, not who
// changed it. They live here, beside the assessor block that renders them, and
// the CLI's printed record reads the same constants, so the operator and the
// assessor are never told two different things about one entry.
const (
	RemoteLabelRunCommit       = "observed during the run; points at a commit this run made"
	RemoteLabelNotAttributable = "observed, not attributable"
)

// RemoteAttributionLabel is the label of an entry's attribution: anything but
// run_commit reads "observed, not attributable".
func RemoteAttributionLabel(a runstate.RemoteAttribution) string {
	if a == runstate.AttributionRunCommit {
		return RemoteLabelRunCommit
	}
	return RemoteLabelNotAttributable
}

// DescribeRemoteEntry is one entry's kind and what it changed. It is raw:
// remote and branch names, URLs and head ref names are repository text, so
// each caller makes the line safe for where it goes — the CLI for a terminal,
// the assessor for a prompt.
func DescribeRemoteEntry(e runstate.RemoteChangeEntry) string {
	ref := e.Remote + "/" + e.Branch
	switch e.Kind {
	case runstate.RemoteAdded:
		return fmt.Sprintf("%s %s %s", e.Kind, e.Remote, describeURLs(e.NewFetchURL, e.NewPushURL))
	case runstate.RemoteRemoved:
		return fmt.Sprintf("%s %s (was %s)", e.Kind, e.Remote, describeURLs(e.OldFetchURL, e.OldPushURL))
	case runstate.RemoteURLChanged:
		return fmt.Sprintf("%s %s %s -> %s", e.Kind, e.Remote, describeURLs(e.OldFetchURL, e.OldPushURL), describeURLs(e.NewFetchURL, e.NewPushURL))
	case runstate.BranchCreated:
		return fmt.Sprintf("%s %s at %s", e.Kind, ref, shortSHA(e.NewSHA))
	case runstate.BranchMoved:
		return fmt.Sprintf("%s %s %s -> %s", e.Kind, ref, shortSHA(e.OldSHA), shortSHA(e.NewSHA))
	case runstate.BranchDeleted:
		return fmt.Sprintf("%s %s (was %s)", e.Kind, ref, shortSHA(e.OldSHA))
	case runstate.PROpened:
		return fmt.Sprintf("%s #%d %s at %s", e.Kind, e.PRNumber, e.HeadRefName, shortSHA(e.NewSHA))
	case runstate.PRUpdated:
		return fmt.Sprintf("%s #%d %s %s -> %s", e.Kind, e.PRNumber, e.HeadRefName, shortSHA(e.OldSHA), shortSHA(e.NewSHA))
	case runstate.PRNoLongerOpen:
		return fmt.Sprintf("%s #%d %s (was %s)", e.Kind, e.PRNumber, e.HeadRefName, shortSHA(e.OldSHA))
	default:
		return fmt.Sprintf("%s %s", e.Kind, e.Remote)
	}
}

// describeURLs names a remote's fetch URL, and its push URL only when that
// differs.
func describeURLs(fetch, push string) string {
	if push == "" || push == fetch {
		return fetch
	}
	return fetch + " (push " + push + ")"
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
