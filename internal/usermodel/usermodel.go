// Package usermodel answers one question: which model did the OPERATOR choose?
//
// It reads exactly one key — `model` — out of the Claude CLI's own settings
// file ($CLAUDE_CONFIG_DIR/settings.json, else ~/.claude/settings.json) and
// returns it verbatim, so a planned node can be spawned with `--model <value>`
// instead of whatever the CLI defaults to when its settings are withheld.
//
// # Why this does not weaken the auto ceiling
//
// The auto ceiling (coordinator.toolPolicyFor) bounds CAPABILITY: layer 1
// decides which settings, hooks and CLAUDE.md load into the node, layer 2 which
// grants bind, layer 3 which tools exist at all, layer 4 MCP, layer 5 the
// residual denies. Every layer answers "what may this node DO".
//
// A model name answers "who does the thinking". It adds no tool, loads no file
// into the context, runs no hook, grants no path, and reaches argv rather than a
// prompt (so it is not fenced text, and internal/fence does not apply). A node
// holding `Read, Glob` holds exactly `Read, Glob` whichever model answers. The
// value also cannot come from untrusted output: there is no `model` key in the
// graph schema, so a planner cannot select it — it comes from a file only the
// operator writes.
//
// What this separates is two things that were only ever coupled by the
// bluntness of `--setting-sources ""`. That flag withholds the whole settings
// document because it is the only lever the CLI offers, and the operator's model
// preference happened to live in the document it withholds. Reading one key back
// out restores the preference without restoring any of the capability: the node's
// ceiling is unchanged, and exactly one preference crosses it, by name.
//
// Reading the WHOLE file would not be equivalent and is deliberately not done:
// the same document's `permissions.allow` holds the standing `Bash(*)`-class
// grants layer 1 exists to withhold, and its `env` block holds live credentials.
// Nothing here decodes either — the struct has one field — and the malformed-file
// warning names the path and the decode error, never any of the contents. A
// second key needs its own ADR.
//
// # We are parsing a file another product owns
//
// settings.json is the Claude CLI's schema, not oh-my-graph's, and it may change
// under us. That is accepted on narrow terms (ADR 0009's rule: never make
// CORRECTNESS depend on a weak parse, and degrade to the pre-parse behaviour
// when it fails): this is a named key in a documented JSON document rather than
// prose, and we are a courier, not an interpreter — the value's vocabulary
// belongs to the CLI that defined it, so `"opus[1m]"` is meaningless here and
// meaningful there. There is deliberately NO allowlist of model names: an
// allowlist goes stale with the CLI's release cadence and would then silently
// substitute a default for a name the operator really chose, which is the very
// defect this package repairs.
//
// So when the schema changes, this degrades rather than lies. If the key is
// renamed or moved, Read finds nothing, no flag is emitted, and a planned node
// runs exactly as it did before this package existed. If the key's TYPE changes
// (say to an object), the decode fails, Read returns the error its caller turns
// into one warning per run, and again no flag is emitted. If a value is passed
// through that this CLI build does not know, the CLI rejects it loudly and the
// node FAILS — a wrong model name must produce a dead node, never a different
// answer, and nothing here may catch that rejection and retry without the flag.
package usermodel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SettingsFileName is the settings document's name inside the CLI's config
// directory.
const SettingsFileName = "settings.json"

// DefaultPath is where the operator's Claude settings live:
// $CLAUDE_CONFIG_DIR/settings.json when that variable is set, else
// ~/.claude/settings.json — the same precedence the CLI itself documents.
//
// It returns "" when neither can be resolved (no $CLAUDE_CONFIG_DIR and no
// home: containers, launchd, some CI). Empty means "nowhere to read", which
// Read treats as "no choice expressed" rather than as an error — a machine with
// no settings file is a supported machine.
func DefaultPath() string {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return filepath.Join(dir, SettingsFileName)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", SettingsFileName)
}

// settings is the ONE key of the operator's settings document this program
// decodes. It has exactly one field on purpose: everything else in that file —
// permissions, hooks, env — is capability or credentials, and is never read,
// never logged, never rendered.
type settings struct {
	Model string `json:"model"`
}

// Read returns the model the operator chose in the settings file at path, or ""
// when they expressed no choice. The five cases, all of which are ordinary:
//
//	key present, non-empty  the value VERBATIM — no normalisation, no
//	                        case-folding, no stripping of a "[1m]"-style
//	                        variant suffix. The operator's string is the payload
//	                        and its vocabulary is the CLI's.
//	key present but blank   treated as absent: the CLI rejects an empty value
//	                        ("--model requires a non-empty value"), so emitting
//	                        it would turn a harmless config typo into a dead run.
//	key absent              "", no error — the CLI's own default stands, and the
//	                        argv is byte-identical to the pre-inheritance one.
//	file absent (or path    "", no error. Not a failure: a machine with no
//	empty)                  settings file is a supported machine.
//	unreadable / malformed  "", plus an error naming the path and the cause. The
//	                        caller must WARN ONCE per run and carry on: a broken
//	                        settings file belongs to the operator, and killing a
//	                        45-node run over it is a worse outcome than running
//	                        the default. Silence is not an option either — that
//	                        reproduces the defect with a new cause.
//
// A value this CLI build has never heard of is not a case: it is passed through
// verbatim, and the CLI's own "Unknown model" rejection fails the node with a
// message naming it.
func Read(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	var s settings
	if err := json.Unmarshal(data, &s); err != nil {
		// The path and the decode error only. encoding/json names the offending
		// token and its offset, never the document's contents, and nothing here
		// may add any: this file holds the operator's credentials.
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	if strings.TrimSpace(s.Model) == "" {
		return "", nil
	}
	return s.Model, nil
}
