package worktree

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/remote"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// #410: which repository gh will read is worked out before gh is asked, and
// when it cannot be told gh is not asked at all — with two GitHub remotes
// and no default, gh reads one of them without failing.
func TestTakeRemoteSnapshot_ExpectedGHRepo(t *testing.T) {
	const (
		originHTTPS = "https://github.com/me/cli.git"
		upstream    = "https://github.com/cli/cli.git"
	)
	prIn := func(url string) remote.OpenPR {
		return remote.OpenPR{Number: 3, HeadRefName: "fix", HeadRefOid: strings.Repeat("a", 40), URL: url}
	}
	pr := prIn("https://github.com/me/cli/pull/3")
	cases := []struct {
		name      string
		remotes   []Remote
		remoteErr error
		defaults  []string
		env       remote.RepoEnv
		prs       []remote.OpenPR
		// wantReason "" means gh is asked and the part is observed.
		wantReason string
		// wantAsked: gh is asked even though the part is unavailable — its
		// answer named another repository.
		wantAsked    bool
		wantExpected ExpectedGHRepo
	}{
		{
			name:       "two GitHub remotes and no gh default",
			remotes:    []Remote{{Name: "origin", FetchURL: originHTTPS}, {Name: "upstream", FetchURL: upstream}},
			wantReason: ReasonAmbiguousGHRepo,
		},
		{
			name:         "two GitHub remotes, origin is gh's default",
			remotes:      []Remote{{Name: "origin", FetchURL: originHTTPS}, {Name: "upstream", FetchURL: upstream}},
			defaults:     []string{"origin"},
			prs:          []remote.OpenPR{pr},
			wantExpected: ExpectedGHRepo{Host: "github.com", Repo: "me/cli", Source: ExpectedFromGHDefault},
		},
		{
			name:         "one GitHub remote and one that is not",
			remotes:      []Remote{{Name: "gitlab", FetchURL: "git@gitlab.com:me/cli.git"}, {Name: "origin", FetchURL: "git@github.com:me/cli.git"}},
			prs:          []remote.OpenPR{pr},
			wantExpected: ExpectedGHRepo{Host: "github.com", Repo: "me/cli", Source: ExpectedFromSingleRemote},
		},
		{
			name:         "GH_REPO over two GitHub remotes",
			remotes:      []Remote{{Name: "origin", FetchURL: originHTTPS}, {Name: "upstream", FetchURL: upstream}},
			env:          remote.RepoEnv{Repo: "cli/cli"},
			prs:          []remote.OpenPR{prIn("https://github.com/cli/cli/pull/3")},
			wantExpected: ExpectedGHRepo{Host: "github.com", Repo: "cli/cli", Source: ExpectedFromGHRepo},
		},
		{
			name: "one repository in https, ssh and scp forms counts once",
			remotes: []Remote{
				{Name: "a", FetchURL: "https://x-access-token:ghs_secret@github.com/Me/CLI.git"},
				{Name: "b", FetchURL: "ssh://git@github.com/me/cli"},
				{Name: "c", FetchURL: "git@GitHub.com:me/cli.git"},
			},
			wantExpected: ExpectedGHRepo{Host: "github.com", Repo: "Me/CLI", Source: ExpectedFromSingleRemote},
		},
		{
			name:       "the remotes part is unavailable",
			remoteErr:  errors.New("fatal: not a git repository"),
			wantReason: ReasonGHRepoRemotesUnreadable,
		},
		{
			name:         "no GitHub remote asks gh as before",
			remotes:      []Remote{{Name: "origin", FetchURL: "/srv/git/r.git"}},
			wantExpected: ExpectedGHRepo{},
		},
		{
			name:         "zero open pull requests are observed with the expected repo",
			remotes:      []Remote{{Name: "origin", FetchURL: originHTTPS}},
			wantExpected: ExpectedGHRepo{Host: "github.com", Repo: "me/cli", Source: ExpectedFromSingleRemote},
		},
		{
			name:         "GH_HOST is the host, and only its remotes are GitHub remotes",
			remotes:      []Remote{{Name: "corp", FetchURL: "https://GHE.Example.com/team/svc.git"}, {Name: "origin", FetchURL: originHTTPS}},
			env:          remote.RepoEnv{Host: "ghe.example.com"},
			wantExpected: ExpectedGHRepo{Host: "ghe.example.com", Repo: "team/svc", Source: ExpectedFromSingleRemote},
		},
		{
			name:         "GH_HOST with no remote on it",
			remotes:      []Remote{{Name: "origin", FetchURL: originHTTPS}},
			env:          remote.RepoEnv{Host: "ghe.example.com"},
			wantExpected: ExpectedGHRepo{Host: "ghe.example.com"},
		},
		{
			name:         "GH_HOST is the host of a GH_REPO naming none",
			remoteErr:    errors.New("fatal: not a git repository"),
			env:          remote.RepoEnv{Repo: "team/svc", Host: "ghe.example.com"},
			wantExpected: ExpectedGHRepo{Host: "ghe.example.com", Repo: "team/svc", Source: ExpectedFromGHRepo},
		},
		{
			name:         "a GH_REPO naming its host",
			env:          remote.RepoEnv{Repo: "GHE.example.com/team/svc", Host: "other.example.com"},
			wantExpected: ExpectedGHRepo{Host: "ghe.example.com", Repo: "team/svc", Source: ExpectedFromGHRepo},
		},
		{
			name:       "gh's default on two repositories",
			remotes:    []Remote{{Name: "origin", FetchURL: originHTTPS}, {Name: "upstream", FetchURL: upstream}},
			defaults:   []string{"origin", "upstream"},
			wantReason: ReasonAmbiguousGHDefault,
		},
		{
			name:       "gh's default on a remote that is not GitHub is passed over",
			remotes:    []Remote{{Name: "gitlab", FetchURL: "https://gitlab.com/me/cli.git"}, {Name: "origin", FetchURL: originHTTPS}, {Name: "upstream", FetchURL: upstream}},
			defaults:   []string{"gitlab"},
			wantReason: ReasonAmbiguousGHRepo,
		},
		{
			name:       "gh lists the PRs of a repository other than the expected origin",
			remotes:    []Remote{{Name: "origin", FetchURL: originHTTPS}},
			prs:        []remote.OpenPR{prIn("https://github.com/cli/cli/pull/3")},
			wantAsked:  true,
			wantReason: "gh read another repo: expected github.com/me/cli, got github.com/cli/cli",
		},
		{
			name:         "PR urls naming the expected repo in another case are observed",
			remotes:      []Remote{{Name: "origin", FetchURL: originHTTPS}},
			prs:          []remote.OpenPR{prIn("https://GitHub.com/Me/CLI/pull/3"), prIn("https://www.github.com/me/Cli/pull/4")},
			wantExpected: ExpectedGHRepo{Host: "github.com", Repo: "me/cli", Source: ExpectedFromSingleRemote},
		},
		{
			name:       "PRs that disagree on their repository",
			remotes:    []Remote{{Name: "origin", FetchURL: originHTTPS}},
			prs:        []remote.OpenPR{pr, prIn("https://github.com/cli/cli/pull/4")},
			wantAsked:  true,
			wantReason: "gh read another repo: expected github.com/me/cli, got github.com/me/cli and github.com/cli/cli",
		},
		{
			name:       "a PR url naming no repository",
			remotes:    []Remote{{Name: "origin", FetchURL: originHTTPS}},
			prs:        []remote.OpenPR{prIn("")},
			wantAsked:  true,
			wantReason: "gh read another repo: expected github.com/me/cli, got a pull request url naming no repo",
		},
		{
			name:         "GHE PRs match the expected GH_HOST repo",
			remotes:      []Remote{{Name: "corp", FetchURL: "https://ghe.example.com/team/svc.git"}},
			env:          remote.RepoEnv{Host: "ghe.example.com"},
			prs:          []remote.OpenPR{prIn("https://GHE.example.com/team/svc/pull/3")},
			wantExpected: ExpectedGHRepo{Host: "ghe.example.com", Repo: "team/svc", Source: ExpectedFromSingleRemote},
		},
		{
			name:         "no expected repo: PR urls are not compared",
			remotes:      []Remote{{Name: "origin", FetchURL: "/srv/git/r.git"}},
			prs:          []remote.OpenPR{prIn("https://github.com/cli/cli/pull/3")},
			wantExpected: ExpectedGHRepo{},
		},
		{
			name:       "a GH_REPO that is not OWNER/REPO",
			env:        remote.RepoEnv{Repo: "just-a-name"},
			wantReason: ReasonBadGHRepo,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obs := NewFakeRemoteObserver()
			obs.SetRemotes(tc.remotes, tc.remoteErr)
			obs.SetGHDefaultRemotes(tc.defaults, nil)
			prs := remote.NewFakePRLister()
			prs.SetRepoEnv(tc.env)
			prs.Set(remote.ObservedPRs(tc.prs))

			snap, expected := takeRemoteSnapshot(context.Background(), obs, prs)
			got := snap.PullRequests
			if tc.wantReason != "" {
				if wantCalls := map[bool]int{true: 1}[tc.wantAsked]; prs.Calls() != wantCalls {
					t.Errorf("gh asked %d times, want %d", prs.Calls(), wantCalls)
				}
				if got.Status != runstate.RemotePartUnavailable || got.Reason != tc.wantReason || got.Open != nil {
					t.Errorf("pull requests = %+v, want unavailable %q", got, tc.wantReason)
				}
				if tc.wantAsked {
					return
				}
				if expected != (ExpectedGHRepo{}) {
					t.Errorf("expected repo = %+v, want none when gh is not asked", expected)
				}
				if got.Repo != "" || got.RepoSource != "" {
					t.Errorf("recorded repo %q (%q), want none when gh is not asked", got.Repo, got.RepoSource)
				}
				return
			}
			if prs.Calls() != 1 {
				t.Errorf("gh asked %d times, want once", prs.Calls())
			}
			if got.Status != runstate.RemotePartObserved || len(got.Open) != len(tc.prs) {
				t.Errorf("pull requests = %+v, want observed with %d PRs", got, len(tc.prs))
			}
			if expected != tc.wantExpected {
				t.Errorf("expected repo = %+v, want %+v", expected, tc.wantExpected)
			}
			wantRepo, wantSource := "", ""
			if tc.wantExpected.Repo != "" {
				wantRepo, wantSource = tc.wantExpected.Host+"/"+tc.wantExpected.Repo, tc.wantExpected.Source
			}
			if got.Repo != wantRepo || got.RepoSource != wantSource {
				t.Errorf("recorded repo %q (%q), want %q (%q)", got.Repo, got.RepoSource, wantRepo, wantSource)
			}
			if strings.Contains(expected.Host+expected.Repo, "secret") {
				t.Errorf("expected repo %+v keeps a credential", expected)
			}
		})
	}
}

// The remotes-unavailable reason says so, and the git failure rides in the
// detail; the gh default is not read once there is no remote to read it on.
func TestTakeRemoteSnapshot_UnreadableRemotesSkipTheGHDefault(t *testing.T) {
	obs := NewFakeRemoteObserver()
	obs.SetRemotes(nil, errors.New("fatal: not a git repository"))
	prs := remote.NewFakePRLister()
	got := TakeRemoteSnapshot(context.Background(), obs, prs).PullRequests
	if !strings.Contains(got.Reason, "remotes could not be read") || !strings.Contains(got.Detail, "not a git repository") {
		t.Errorf("pull requests = %+v, want the remotes named as unreadable", got)
	}
	if obs.GHDefaultCalls() != 0 || prs.Calls() != 0 {
		t.Errorf("gh default read %d times, gh asked %d times; want neither", obs.GHDefaultCalls(), prs.Calls())
	}
}

// A failed gh-resolved read is not taken as "no default": with two GitHub
// remotes that would be a guess.
func TestTakeRemoteSnapshot_UnreadableGHDefaultIsNotGuessed(t *testing.T) {
	obs := NewFakeRemoteObserver()
	obs.SetRemotes([]Remote{{Name: "origin", FetchURL: "https://github.com/me/cli"}}, nil)
	obs.SetGHDefaultRemotes(nil, errors.New("git config: timed out"))
	prs := remote.NewFakePRLister()
	got := TakeRemoteSnapshot(context.Background(), obs, prs).PullRequests
	if got.Status != runstate.RemotePartUnavailable || got.Reason != ReasonGHDefaultUnreadable || prs.Calls() != 0 {
		t.Errorf("pull requests = %+v (gh asked %d times), want unavailable %q and gh not asked", got, prs.Calls(), ReasonGHDefaultUnreadable)
	}
}

func TestParseGitHubURL(t *testing.T) {
	cases := []struct {
		url  string
		want ghRepoRef
		ok   bool
	}{
		{"https://github.com/o/r.git", ghRepoRef{"github.com", "o/r"}, true},
		{"https://user:ghp_secret@github.com/o/r/", ghRepoRef{"github.com", "o/r"}, true},
		{"ssh://git@github.com:22/o/r.git", ghRepoRef{"github.com", "o/r"}, true},
		{"ssh://git@ssh.github.com:443/o/r.git", ghRepoRef{"github.com", "o/r"}, true},
		{"git@github.com:o/r.git", ghRepoRef{"github.com", "o/r"}, true},
		{"github.com:o/r", ghRepoRef{"github.com", "o/r"}, true},
		{"https://WWW.GitHub.com/O/R", ghRepoRef{"github.com", "O/R"}, true},
		{"/srv/git/r.git", ghRepoRef{}, false},
		{"./r", ghRepoRef{}, false},
		{"file:///srv/o/r.git", ghRepoRef{}, false},
		{"https://github.com/o", ghRepoRef{}, false},
		{"https://github.com/o/r/tree/main", ghRepoRef{}, false},
	}
	for _, tc := range cases {
		got, ok := parseGitHubURL(tc.url)
		if ok != tc.ok || got != tc.want {
			t.Errorf("parseGitHubURL(%q) = %+v, %v; want %+v, %v", tc.url, got, ok, tc.want, tc.ok)
		}
	}
}

func TestParseGHResolved(t *testing.T) {
	out := "remote.origin.gh-resolved base\n" +
		"remote.my.fork.gh-resolved base\n" +
		"remote.upstream.gh-resolved other\n" +
		"warning: something\n"
	got := parseGHResolved(out)
	if want := []string{"my.fork", "origin"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("parseGHResolved = %v, want %v", got, want)
	}
}
