package coordinator

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestMeasureReuseMenuPromptSize is the reproducible method behind
// docs/measurements/0038-reuse-menu-prompt-size.md (#338). It renders the
// planner prompt on THIS checkout's graphs/fragments/ through the real Plan
// path, with reuse on and with WithoutReuse, for the first call, the repair
// call and a goal-loop continuation, and prints each size in BYTES (no
// tokenizer runs here). Run it with:
//
//	go test ./internal/coordinator -run TestMeasureReuseMenuPromptSize -v -count=1
//
// It asserts only what holds whatever the corpus holds: off carries no menu,
// and on is never smaller than off.
func TestMeasureReuseMenuPromptSize(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := scanReuseCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(catalog.Offered))
	for i, entry := range catalog.Offered {
		ids[i] = entry.ID
	}
	var reasons []string
	for reason, n := range catalog.SkippedByReason() {
		reasons = append(reasons, fmt.Sprintf("%s: %d", reason, n))
	}
	sort.Strings(reasons)
	t.Logf("catalog %s: %d offered %v, %d skipped (%s)", catalog.Dir, len(ids), ids, len(catalog.Skipped), strings.Join(reasons, ", "))
	for _, skip := range catalog.Skipped {
		t.Logf("  skipped %s: %s", filepath.Base(skip.Source), skip.Reason)
	}

	onFirst, onRepair, onCont := plannerPrompts(t, WithInvocationDir(root))
	offFirst, offRepair, offCont := plannerPrompts(t, WithInvocationDir(root), WithoutReuse())
	menu, err := reuseMenuBlock(catalog.Offered)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("menu block alone: %d bytes", len(menu))
	for _, row := range []struct {
		call    string
		on, off string
	}{
		{"first", onFirst.Prompt, offFirst.Prompt},
		{"repair", onRepair.Prompt, offRepair.Prompt},
		{"continuation", onCont.Prompt, offCont.Prompt},
	} {
		t.Logf("%-12s on %6d bytes  off %6d bytes  delta %+6d bytes", row.call, len(row.on), len(row.off), len(row.on)-len(row.off))
		if strings.Contains(row.off, "Reusable shapes the operator already keeps.") {
			t.Errorf("%s: the menu rendered with WithoutReuse", row.call)
		}
		if len(row.on) < len(row.off) {
			t.Errorf("%s: menu-on prompt (%d bytes) is smaller than menu-off (%d bytes)", row.call, len(row.on), len(row.off))
		}
	}
}
