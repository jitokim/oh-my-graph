package schedule

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/handoff"
)

// TestMergeShepherdMergeVerify runs the SHIPPED merge-shepherd merge node's
// success_check.verify against a fake `gh` (#334). That issue is a merge the
// permission mode DENIED: merge answered WITHHELD, the pattern accepted it, and
// the run exited 0 over an OPEN PR. The verify is the engine-run half that
// catches it, and it is inline POSIX sh in the YAML, so the only way to test
// what ships is to take the command out of the loaded graph, resolve it
// exactly as the scheduler does (resolveVerification), and run it.
//
// The fake gh prints the one line the real `gh --jq` projection prints —
// `<state> <head> <on|off>` — and drops a marker only when its argv carries
// `pr view <pr>`, so a FAIL case cannot pass for the wrong reason (a fake that
// refused the args). Pass/fail is judged by exit status alone, as
// judgeVerification judges it.
func TestMergeShepherdMergeVerify(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on PATH; the verify command is POSIX sh")
	}

	loaded, err := graph.LoadFile(filepath.Join("..", "..", "graphs", "merge-shepherd.yaml"))
	if err != nil {
		t.Fatalf("load merge-shepherd: %v", err)
	}
	var merge *graph.Node
	for i, n := range loaded.Graph.Nodes {
		if n.ID == "merge" {
			merge = &loaded.Graph.Nodes[i]
		}
	}
	if merge == nil {
		t.Fatal("merge-shepherd has no merge node")
	}
	if merge.SuccessCheck.Verify == nil {
		t.Fatal("merge-shepherd's merge declares no verify — a denied merge answered WITHHELD passes again (#334)")
	}

	const (
		pr     = "4242"
		shaA   = "83edfad11db1ba0281cab9b615c4acadded38512"
		shaB   = "0123456789abcdef0123456789abcdef01234567"
		shaAUp = "83EDFAD11DB1BA0281CAB9B615C4ACADDED38512"
	)
	cases := []struct {
		name    string
		verdict string // recheck's recorded reply
		gh      string // what the fake gh prints, or "" to exit non-zero
		pass    bool
	}{
		{"denied merge leaves the PR open", "RECHECKED " + shaA + " mergeable: CLEAN review_decision: APPROVED", "OPEN " + shaA + " off", false},
		{"merged at the judged head", "**RECHECKED** " + shaAUp + " mergeable: CLEAN", "MERGED " + shaA + " off", true},
		{"unsettled and left open", "UNSETTLED " + shaA + " — test still IN_PROGRESS", "OPEN " + shaA + " off", true},
		{"auto-merge queued instead of merged", "RECHECKED " + shaA, "OPEN " + shaA + " on", false},
		{"merged at a head pushed after recheck", "RECHECKED " + shaA, "MERGED " + shaB + " off", false},
		{"merged against an unsettled recheck", "UNSETTLED " + shaA, "MERGED " + shaA + " off", false},
		{"auto-merge queued against an unsettled recheck", "UNSETTLED " + shaA, "OPEN " + shaA + " on", false},
		{"recheck named a 7-hex sha", "RECHECKED 83edfad", "MERGED " + shaA + " off", false},
		{"gh fails", "RECHECKED " + shaA, "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeDir := t.TempDir()
			marker := filepath.Join(fakeDir, "args-ok")
			body := "exit 1\n"
			if tc.gh != "" {
				body = "printf '%s\\n' '" + tc.gh + "'\n"
			}
			script := "#!/bin/sh\n" +
				"case \" $* \" in *\" pr view " + pr + " \"*) : > '" + marker + "' ;; *) exit 97 ;; esac\n" +
				body
			if err := os.WriteFile(filepath.Join(fakeDir, "gh"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))

			artifact := filepath.Join(t.TempDir(), "recheck.out")
			if err := os.WriteFile(artifact, []byte(tc.verdict+"\nmore of recheck's reply\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			h := handoff.New(t.TempDir(), map[string]string{"repo": t.TempDir(), "pr": pr})
			h.Seed("recheck", artifact, "")

			cwd := t.TempDir()
			req, err := resolveVerification(*merge, *merge.SuccessCheck.Verify, h, cwd)
			if err != nil {
				t.Fatalf("resolveVerification: %v", err)
			}

			cmd := exec.Command(sh, "-c", req.Command)
			cmd.Dir = req.Cwd
			out, runErr := cmd.CombinedOutput()
			var exitErr *exec.ExitError
			if runErr != nil && !errors.As(runErr, &exitErr) {
				t.Fatalf("could not run the verify: %v", runErr)
			}
			t.Logf("verify said: %s", strings.TrimSpace(string(out)))

			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("the fake gh was not called with `pr view %s` — the verify reads some other PR", pr)
			}
			if passed := runErr == nil; passed != tc.pass {
				t.Errorf("verify passed=%v, want %v; output: %s", passed, tc.pass, out)
			}
		})
	}
}
