package graph

import (
	"path/filepath"
	"testing"
)

// TestShippedGraphsLoad guards the example graphs that ship in graphs/: they
// must parse and pass validation, so a broken example is caught in CI rather
// than at a user's first run.
func TestShippedGraphsLoad(t *testing.T) {
	for _, name := range []string{"haiku-smoke.yaml", "dev-review-pr.yaml", "self-dev.yaml"} {
		path := filepath.Join("..", "..", "graphs", name)
		if _, err := Load(path); err != nil {
			t.Errorf("shipped graph %s failed to load: %v", name, err)
		}
	}
}
