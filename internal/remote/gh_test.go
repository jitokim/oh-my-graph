package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// stubGH writes script as an executable `gh` into a fresh directory and puts
// that directory first on PATH, so GHRunner spawns the stub and never a real
// gh. The script body follows a #!/bin/sh line.
func stubGH(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// prJSON renders n open PRs as gh would.
func prJSON(t *testing.T, n int) string {
	t.Helper()
	prs := make([]OpenPR, n)
	for i := range prs {
		prs[i] = OpenPR{Number: i + 1, HeadRefName: fmt.Sprintf("b%d", i+1), HeadRefOid: fmt.Sprintf("%040d", i+1)}
	}
	b, err := json.Marshal(prs)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func wantUnavailable(t *testing.T, got PRList, reason string) {
	t.Helper()
	if got.Status != runstate.RemotePartUnavailable || got.Reason != reason {
		t.Fatalf("got status %q reason %q (detail %q), want unavailable %q", got.Status, got.Reason, got.Detail, reason)
	}
	if got.PRs != nil {
		t.Errorf("an unavailable list carries PRs %v; it must never read as a list", got.PRs)
	}
}

// TestGHRunner_SpawnsTheFixedArgvInTheRepoWithAScrubbedEnv is the call-site
// half of the subscription-auth guarantee for the fifth spawner, observed
// from inside the child: the stub records its argv, working directory and
// environment.
func TestGHRunner_SpawnsTheFixedArgvInTheRepoWithAScrubbedEnv(t *testing.T) {
	out := t.TempDir()
	repo := t.TempDir()
	t.Setenv("GH_STUB_OUT", out)
	t.Setenv("ANTHROPIC_API_KEY", "sk-live-secret")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "tok-secret")
	t.Setenv("OPENAI_API_KEY", "sk-openai-secret")
	t.Setenv("CODEX_API_KEY", "sk-codex-secret")
	t.Setenv("GH_TOKEN", "gh-operator-token")
	stubGH(t, `for a in "$@"; do printf '%s\n' "$a"; done > "$GH_STUB_OUT/argv"
pwd -P > "$GH_STUB_OUT/pwd"
env > "$GH_STUB_OUT/env"
echo '[]'
`)

	got := NewGHRunner(repo).ListOpenPRs(context.Background())
	if !got.Observed() || got.PRs == nil || len(got.PRs) != 0 {
		t.Fatalf("got %+v, want an observed empty list", got)
	}

	argv := readFile(t, filepath.Join(out, "argv"))
	wantArgv := "pr\nlist\n--state\nopen\n--limit\n1000\n--json\nnumber,headRefName,headRefOid\n"
	if argv != wantArgv {
		t.Errorf("argv = %q, want %q", argv, wantArgv)
	}

	wantDir, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if dir := strings.TrimSpace(readFile(t, filepath.Join(out, "pwd"))); dir != wantDir {
		t.Errorf("gh ran in %q, want the invocation repository %q", dir, wantDir)
	}

	env := strings.Split(readFile(t, filepath.Join(out, "env")), "\n")
	has := func(kv string) bool {
		for _, e := range env {
			if e == kv {
				return true
			}
		}
		return false
	}
	for _, e := range env {
		for _, k := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "OPENAI_API_KEY", "CODEX_API_KEY"} {
			if strings.HasPrefix(e, k+"=") {
				t.Errorf("billing variable leaked into the gh child env: %s", e)
			}
		}
	}
	if !has("GH_PROMPT_DISABLED=1") {
		t.Error("the gh child env lacks GH_PROMPT_DISABLED=1")
	}
	if !has("GH_TOKEN=gh-operator-token") {
		t.Error("the scrub removed GH_TOKEN; gh must authenticate as the operator")
	}
}

// TestGHRunner_CmdShape pins the built *exec.Cmd itself, without spawning:
// the argv, the directory, the timeout default and the grandchild-pipe bound.
func TestGHRunner_CmdShape(t *testing.T) {
	r := NewGHRunner("/some/repo")
	if r.timeout != 30*time.Second {
		t.Errorf("default timeout = %s, want 30s", r.timeout)
	}
	cmd := r.ghCmd(context.Background())
	if got := strings.Join(cmd.Args[1:], " "); got != "pr list --state open --limit 1000 --json number,headRefName,headRefOid" {
		t.Errorf("argv = %q", got)
	}
	if cmd.Dir != "/some/repo" {
		t.Errorf("Dir = %q, want /some/repo", cmd.Dir)
	}
	if cmd.WaitDelay <= 0 {
		t.Error("WaitDelay is unset; a grandchild holding gh's pipes could outlive the timeout")
	}
}

func TestGHRunner_ParsesANormalList(t *testing.T) {
	stubGH(t, `echo '[{"number":7,"headRefName":"feat/x","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},{"number":9,"headRefName":"fix/y","headRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]'
`)
	got := NewGHRunner(t.TempDir()).ListOpenPRs(context.Background())
	if !got.Observed() || got.Reason != "" {
		t.Fatalf("got %+v, want observed", got)
	}
	want := []OpenPR{
		{Number: 7, HeadRefName: "feat/x", HeadRefOid: strings.Repeat("a", 40)},
		{Number: 9, HeadRefName: "fix/y", HeadRefOid: strings.Repeat("b", 40)},
	}
	if fmt.Sprint(got.PRs) != fmt.Sprint(want) {
		t.Errorf("PRs = %v, want %v", got.PRs, want)
	}
}

func TestGHRunner_NonZeroExitIsUnavailable(t *testing.T) {
	stubGH(t, `echo 'no git remotes found for https://user:hunter2@github.com/o/r' >&2
exit 1
`)
	got := NewGHRunner(t.TempDir()).ListOpenPRs(context.Background())
	wantUnavailable(t, got, ReasonNonZeroExit)
	if !strings.Contains(got.Detail, "no git remotes found") {
		t.Errorf("Detail = %q, want gh's diagnosis", got.Detail)
	}
	if strings.Contains(got.Detail, "hunter2") || strings.Contains(got.Detail, "user:") {
		t.Errorf("Detail = %q leaks a URL credential", got.Detail)
	}
}

func TestGHRunner_NotAuthenticated(t *testing.T) {
	for name, script := range map[string]string{
		"exit status 4": "echo 'authentication required' >&2\nexit 4\n",
		"login advice":  "echo 'To get started with GitHub CLI, please run:  gh auth login' >&2\nexit 1\n",
	} {
		t.Run(name, func(t *testing.T) {
			stubGH(t, script)
			wantUnavailable(t, NewGHRunner(t.TempDir()).ListOpenPRs(context.Background()), ReasonNotAuthenticated)
		})
	}
}

func TestGHRunner_GHMissingFromPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	got := NewGHRunner(t.TempDir()).ListOpenPRs(context.Background())
	wantUnavailable(t, got, ReasonGHNotFound)
}

func TestGHRunner_Timeout(t *testing.T) {
	stubGH(t, "exec sleep 10\n")
	r := NewGHRunner(t.TempDir())
	r.timeout = 100 * time.Millisecond
	start := time.Now()
	got := r.ListOpenPRs(context.Background())
	wantUnavailable(t, got, ReasonTimeout)
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("a 100ms timeout took %s to return", took)
	}
}

func TestGHRunner_CanceledContext(t *testing.T) {
	stubGH(t, "exec sleep 10\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	wantUnavailable(t, NewGHRunner(t.TempDir()).ListOpenPRs(ctx), ReasonCanceled)
}

func TestGHRunner_UnparsableOutput(t *testing.T) {
	for name, out := range map[string]string{
		"not json":   "Showing 3 of 3 open pull requests",
		"empty":      "",
		"an object":  `{"number":1}`,
		"null":       "null",
		"no head":    `[{"number":1,"headRefName":"x"}]`,
		"zero":       `[{"number":0,"headRefName":"x","headRefOid":"abc"}]`,
		"wrong type": `[{"number":"1","headRefName":"x","headRefOid":"abc"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			stubGH(t, "cat <<'EOF'\n"+out+"\nEOF\n")
			wantUnavailable(t, NewGHRunner(t.TempDir()).ListOpenPRs(context.Background()), ReasonUnparsable)
		})
	}
}

func TestGHRunner_ExactlyTheLimitIsTruncated(t *testing.T) {
	file := filepath.Join(t.TempDir(), "prs.json")
	if err := os.WriteFile(file, []byte(prJSON(t, prListLimit)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GH_STUB_PRS", file)
	stubGH(t, `cat "$GH_STUB_PRS"`+"\n")
	wantUnavailable(t, NewGHRunner(t.TempDir()).ListOpenPRs(context.Background()), ReasonTruncated)

	// One fewer is a whole list.
	if err := os.WriteFile(file, []byte(prJSON(t, prListLimit-1)), 0o600); err != nil {
		t.Fatal(err)
	}
	got := NewGHRunner(t.TempDir()).ListOpenPRs(context.Background())
	if !got.Observed() || len(got.PRs) != prListLimit-1 {
		t.Fatalf("got status %q reason %q with %d PRs, want observed %d", got.Status, got.Reason, len(got.PRs), prListLimit-1)
	}
}

func TestFakePRLister(t *testing.T) {
	f := NewFakePRLister()
	if got := f.ListOpenPRs(context.Background()); !got.Observed() || got.PRs == nil || len(got.PRs) != 0 {
		t.Fatalf("default = %+v, want an observed empty list", got)
	}
	f.Set(UnavailablePRs(ReasonNotAuthenticated, "https://x:tok@github.com"))
	got := f.ListOpenPRs(context.Background())
	wantUnavailable(t, got, ReasonNotAuthenticated)
	if strings.Contains(got.Detail, "tok") {
		t.Errorf("Detail = %q leaks a credential", got.Detail)
	}
	if f.Calls() != 2 {
		t.Errorf("Calls() = %d, want 2", f.Calls())
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
