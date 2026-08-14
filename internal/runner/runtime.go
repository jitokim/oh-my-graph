package runner

import "fmt"

// Runtime identifies the one CLI protocol used by a whole run.
type Runtime string

const (
	RuntimeClaude Runtime = "claude"
	RuntimeCodex  Runtime = "codex"
)

// ParseRuntime parses the closed runtime vocabulary. An empty value is the
// backwards-compatible default.
func ParseRuntime(value string) (Runtime, error) {
	switch Runtime(value) {
	case "", RuntimeClaude:
		return RuntimeClaude, nil
	case RuntimeCodex:
		return RuntimeCodex, nil
	default:
		return "", fmt.Errorf("unknown runtime %q (want claude or codex)", value)
	}
}
