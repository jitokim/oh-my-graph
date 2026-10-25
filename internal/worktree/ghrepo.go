package worktree

import (
	"context"
	"net/url"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/remote"
)

// The sources an ExpectedGHRepo is worked out from, in the order
// expectGHRepo tries them (#410).
const (
	// ExpectedFromGHRepo: GH_REPO in the env the gh child runs with.
	ExpectedFromGHRepo = "GH_REPO"
	// ExpectedFromGHDefault: the remote `gh repo set-default` marked with
	// remote.<name>.gh-resolved.
	ExpectedFromGHDefault = "gh default"
	// ExpectedFromSingleRemote: the only GitHub repository among the
	// configured remotes.
	ExpectedFromSingleRemote = "single GitHub remote"
)

// The reasons the pull-request part is unavailable before gh is asked,
// because which repository gh would read cannot be told. gh does not fail in
// these cases: with two GitHub remotes and no default it reads one of them,
// and the snapshot would record that repository's PRs as this one's.
const (
	ReasonAmbiguousGHRepo = "ambiguous gh repo: more than one GitHub remote and no gh default (run gh repo set-default)"
	// ReasonAmbiguousGHDefault: gh-resolved marks remotes of two different
	// GitHub repositories.
	ReasonAmbiguousGHDefault = "ambiguous gh repo: more than one remote is gh's default"
	// ReasonGHRepoRemotesUnreadable: the remotes part is unavailable, so the
	// remotes gh would resolve from are unknown.
	ReasonGHRepoRemotesUnreadable = "gh repo unknown: the remotes could not be read"
	// ReasonGHDefaultUnreadable: reading gh-resolved from git config failed.
	ReasonGHDefaultUnreadable = "gh repo unknown: gh's default repo could not be read from git config"
	// ReasonBadGHRepo: GH_REPO is set but is not [HOST/]OWNER/REPO.
	ReasonBadGHRepo = "gh repo unknown: GH_REPO is not [HOST/]OWNER/REPO"
)

// reasonGHReadAnotherRepo is the prefix of the reason the pull-request part
// is unavailable with when gh answered, but its PRs' URLs name another
// repository than the expected one; checkGHRepoRead writes the rest.
const reasonGHReadAnotherRepo = "gh read another repo: "

// noRepoInURL stands for the actual repository when a PR's URL names none.
const noRepoInURL = "a pull request url naming no repo"

// defaultGHHost is the host gh reads when neither GH_REPO nor GH_HOST names
// one.
const defaultGHHost = "github.com"

// ExpectedGHRepo is the GitHub repository the snapshot expects `gh pr list`
// to read, worked out before gh is asked (#410). Host is the GitHub host,
// lowercased; Repo is "owner/name" as the source wrote it; Source is one of
// the Expected* constants. A zero Repo with an empty Source means no rule
// applied — no GH_REPO, no gh default and no GitHub remote — and gh is asked
// as it always was; Host then holds GH_HOST, if set.
//
// It never holds a credential: parseGitHubURL keeps only host and path.
type ExpectedGHRepo struct {
	Host   string
	Repo   string
	Source string
}

// ghRepoRef is one GitHub repository a URL or GH_REPO names.
type ghRepoRef struct {
	host, repo string
}

// key is the repository's identity: GitHub compares host and owner/name
// case-insensitively, so two remotes spelling one repo differently are one.
func (r ghRepoRef) key() string { return strings.ToLower(r.host + "/" + r.repo) }

// ghRepoUnknown is why the pull-request part is recorded unavailable without
// asking gh; Detail adds what the engine saw, redacted by the caller.
type ghRepoUnknown struct {
	Reason string
	Detail string
}

// expectGHRepo works out which repository gh will read, by the first rule
// that applies:
//
//  1. GH_REPO in env, the env the gh child runs with: gh reads exactly that,
//     whatever the remotes say, so the rest is skipped.
//  2. A remote marked gh's default (remote.<name>.gh-resolved = base),
//     read through obs.
//  3. Exactly one distinct GitHub repository among the remotes' fetch URLs.
//     More than one is ambiguous and gh is not asked.
//
// No GitHub remote at all leaves gh to its own resolution, as before. A
// remote is a GitHub remote when its host is GH_HOST — gh considers only
// that host when it is set — or, with GH_HOST unset, github.com or a
// *.ghe.com host. remotesErr is the remotes part's own failure: with it, and
// no GH_REPO, rules 2 and 3 have nothing to read and nothing is guessed.
//
// A non-nil ghRepoUnknown means gh must not be asked.
func expectGHRepo(ctx context.Context, obs RemoteObserver, env remote.RepoEnv, remotes []Remote, remotesErr error) (ExpectedGHRepo, *ghRepoUnknown) {
	ghHost := strings.ToLower(env.Host)
	if env.Repo != "" {
		ref, ok := parseGHRepoEnv(env.Repo)
		if !ok {
			return ExpectedGHRepo{}, &ghRepoUnknown{Reason: ReasonBadGHRepo, Detail: env.Repo}
		}
		if ref.host == "" {
			ref.host = ghHost
		}
		if ref.host == "" {
			ref.host = defaultGHHost
		}
		return ExpectedGHRepo{Host: ref.host, Repo: ref.repo, Source: ExpectedFromGHRepo}, nil
	}
	if remotesErr != nil {
		return ExpectedGHRepo{}, &ghRepoUnknown{Reason: ReasonGHRepoRemotesUnreadable, Detail: remotesErr.Error()}
	}

	byName := map[string]ghRepoRef{}
	var distinct []ghRepoRef
	seen := map[string]bool{}
	for _, r := range remotes {
		ref, ok := parseGitHubURL(r.FetchURL)
		if !ok || !isGitHubHost(ref.host, ghHost) {
			continue
		}
		byName[r.Name] = ref
		if !seen[ref.key()] {
			seen[ref.key()] = true
			distinct = append(distinct, ref)
		}
	}
	if len(distinct) == 0 {
		return ExpectedGHRepo{Host: ghHost}, nil
	}

	defaults, err := obs.GHDefaultRemotes(ctx)
	if err != nil {
		return ExpectedGHRepo{}, &ghRepoUnknown{Reason: ReasonGHDefaultUnreadable, Detail: err.Error()}
	}
	// gh only honours gh-resolved on a remote it recognises as GitHub, so a
	// mark on any other remote is passed over, as gh passes it over.
	var marked []ghRepoRef
	markedSeen := map[string]bool{}
	for _, name := range defaults {
		ref, ok := byName[name]
		if ok && !markedSeen[ref.key()] {
			markedSeen[ref.key()] = true
			marked = append(marked, ref)
		}
	}
	switch {
	case len(marked) == 1:
		return ExpectedGHRepo{Host: marked[0].host, Repo: marked[0].repo, Source: ExpectedFromGHDefault}, nil
	case len(marked) > 1:
		return ExpectedGHRepo{}, &ghRepoUnknown{Reason: ReasonAmbiguousGHDefault}
	case len(distinct) == 1:
		return ExpectedGHRepo{Host: distinct[0].host, Repo: distinct[0].repo, Source: ExpectedFromSingleRemote}, nil
	default:
		return ExpectedGHRepo{}, &ghRepoUnknown{Reason: ReasonAmbiguousGHRepo}
	}
}

// checkGHRepoRead reports whether the PRs gh listed are the expected
// repository's, read from their URLs. With no expected repository, or no PRs,
// there is nothing to compare and the list stands. Otherwise every PR's URL
// must name the expected host and owner/name, compared case-insensitively;
// PRs naming more than one repository, or a URL naming none, are a mismatch
// too. reason is 'gh read another repo: expected <expected>, got <actual>'
// in host/owner/name forms.
func checkGHRepoRead(expected ExpectedGHRepo, prs []remote.OpenPR) (reason string, ok bool) {
	if expected.Repo == "" || len(prs) == 0 {
		return "", true
	}
	want := ghRepoRef{host: expected.Host, repo: expected.Repo}
	var got []string
	seen := map[string]bool{}
	mismatch := false
	for _, pr := range prs {
		ref, ok := parsePRURL(pr.URL)
		name, key := noRepoInURL, ""
		if ok {
			name, key = ref.host+"/"+ref.repo, ref.key()
		}
		if key != want.key() {
			mismatch = true
		}
		if !seen[strings.ToLower(name)] {
			seen[strings.ToLower(name)] = true
			got = append(got, name)
		}
	}
	if !mismatch {
		return "", true
	}
	return reasonGHReadAnotherRepo + "expected " + want.host + "/" + want.repo + ", got " + strings.Join(got, " and "), false
}

// parsePRURL reads the repository out of a PR's web URL,
// https://<host>/<owner>/<repo>/pull/<n>. Only the host and owner/repo are
// kept; anything else is not a match.
func parsePRURL(raw string) (ghRepoRef, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ghRepoRef{}, false
	}
	switch strings.ToLower(u.Scheme) {
	case "https", "http":
	default:
		return ghRepoRef{}, false
	}
	host := normalizeGHHost(u.Hostname())
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if host == "" || len(parts) != 4 || parts[0] == "" || parts[1] == "" || parts[2] != "pull" || !isDigits(parts[3]) {
		return ghRepoRef{}, false
	}
	return ghRepoRef{host: host, repo: parts[0] + "/" + parts[1]}, true
}

// isDigits reports whether s is a non-empty run of ASCII digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// isGitHubHost reports whether host is one gh would read: GH_HOST when it is
// set, else github.com or a GitHub Enterprise Cloud *.ghe.com host.
func isGitHubHost(host, ghHost string) bool {
	if ghHost != "" {
		return host == ghHost
	}
	return host == defaultGHHost || strings.HasSuffix(host, ".ghe.com")
}

// parseGHRepoEnv reads GH_REPO's [HOST/]OWNER/REPO, or a URL naming one.
// host is "" when GH_REPO names none.
func parseGHRepoEnv(v string) (ghRepoRef, bool) {
	if strings.Contains(v, "://") || strings.HasPrefix(v, "git@") {
		return parseGitHubURL(v)
	}
	parts := strings.Split(v, "/")
	switch {
	case len(parts) == 2 && parts[0] != "" && parts[1] != "":
		return ghRepoRef{repo: parts[0] + "/" + parts[1]}, true
	case len(parts) == 3 && parts[0] != "" && parts[1] != "" && parts[2] != "":
		return ghRepoRef{host: normalizeGHHost(parts[0]), repo: parts[1] + "/" + parts[2]}, true
	}
	return ghRepoRef{}, false
}

// parseGitHubURL reads a remote URL naming owner/repo on some host, in the
// forms git accepts for one: https://[cred@]host/owner/repo,
// ssh://[user@]host[:port]/owner/repo and the scp-like [user@]host:owner/repo,
// each with or without a .git suffix. Only the host and the path are kept:
// any credential in the URL is dropped here, never returned. Anything else —
// a local path, a path that is not exactly owner/repo — is not a match.
func parseGitHubURL(raw string) (ghRepoRef, bool) {
	raw = strings.TrimSpace(raw)
	var host, path string
	if i := strings.Index(raw, "://"); i >= 0 {
		u, err := url.Parse(raw)
		if err != nil {
			return ghRepoRef{}, false
		}
		switch strings.ToLower(u.Scheme) {
		case "https", "http", "ssh", "git", "git+ssh", "ssh+git":
		default:
			return ghRepoRef{}, false
		}
		host, path = u.Hostname(), u.Path
	} else {
		// scp-like: a colon before any slash, and something before it.
		colon := strings.Index(raw, ":")
		if colon <= 0 || strings.Contains(raw[:colon], "/") {
			return ghRepoRef{}, false
		}
		host, path = raw[:colon], raw[colon+1:]
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
	}
	host = normalizeGHHost(host)
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	parts := strings.Split(path, "/")
	if host == "" || len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ghRepoRef{}, false
	}
	return ghRepoRef{host: host, repo: parts[0] + "/" + parts[1]}, true
}

// normalizeGHHost lowercases host and folds the aliases gh itself folds:
// www.github.com, and ssh.github.com (ssh over port 443), are github.com.
func normalizeGHHost(host string) string {
	host = strings.ToLower(host)
	switch host {
	case "www." + defaultGHHost, "ssh." + defaultGHHost:
		return defaultGHHost
	}
	return host
}
