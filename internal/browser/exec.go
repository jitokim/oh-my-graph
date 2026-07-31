package browser

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jitokim/oh-my-graph/internal/childenv"
)

// openTimeout bounds the launcher command. `open`/`xdg-open`/`cmd /c start`
// hand the URL to the desktop and return almost immediately; the bound keeps
// a wedged launcher (a hung display session, a broken handler registration)
// from stalling the caller indefinitely.
const openTimeout = 10 * time.Second

// ExecOpener is the production Opener: it hands the URL to the operating
// system's default-browser launcher (see the build-tagged openArgv files for
// the per-platform command). It is the only object in this package that
// spawns a process, and one of exactly four in the project (the others:
// runner.ClaudeCLIRunner, verify.ShellVerifier and worktree.GitManager) —
// see docs/adr/0006.
//
// Construct it in cmd/oh-my-graph and inject it; nothing in internal/serve
// builds one, so no test picks up a real spawn by accident.
type ExecOpener struct {
	// environ supplies the parent environment to scrub. A field (defaulting
	// to os.Environ) so a test can inject a parent env that DOES carry the
	// API keys and then assert they are absent from the built child env.
	environ func() []string
}

// NewExecOpener builds the production Opener.
func NewExecOpener() *ExecOpener {
	return &ExecOpener{environ: os.Environ}
}

// Open launches the platform's default-browser handler on url under the
// launcher timeout and waits for the launcher (not the browser) to exit. A
// failure carries the launcher's combined output in the error, because
// `xdg-open` explains itself on stderr and an exit status alone ("exit
// status 3") diagnoses nothing.
func (o *ExecOpener) Open(ctx context.Context, url string) error {
	cmdCtx, cancel := context.WithTimeout(ctx, openTimeout)
	defer cancel()

	out, err := o.openCmd(cmdCtx, url).CombinedOutput()
	if err != nil {
		if output := strings.TrimSpace(string(out)); output != "" {
			return fmt.Errorf("open %s in browser: %w: %s", url, err, output)
		}
		return fmt.Errorf("open %s in browser: %w", url, err)
	}
	return nil
}

// openCmd assembles the exact *exec.Cmd for one launch: the platform argv and
// the scrubbed child environment. It is the unit under test — exec_test.go
// calls it directly to assert that ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN
// are absent from cmd.Env. Nothing here spawns; Open wires it to the OS.
//
// The env scrub is not an optional nicety for the launcher either: the URL
// handler it dispatches to is arbitrary user-configured code (a .desktop
// entry, a registry association) that inherits this environment and may
// legitimately invoke claude — an unscrubbed child would run it on metered
// API billing.
func (o *ExecOpener) openCmd(ctx context.Context, url string) *exec.Cmd {
	argv := openArgv(url)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = childenv.Scrub(o.environ())
	return cmd
}
