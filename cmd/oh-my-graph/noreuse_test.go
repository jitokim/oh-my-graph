package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// reuseMenuMarker is a line only the planner prompt's reuse menu carries.
const reuseMenuMarker = "Reusable shapes the operator already keeps."

// #338: --no-reuse is registered on `auto` and on `chat`, off by default, and
// set by the flag on both.
func TestNoReuseFlag_ParsesOnAutoAndChat(t *testing.T) {
	auto := newAutoFlags()
	if err := auto.parse([]string{"audit the readme"}); err != nil {
		t.Fatal(err)
	}
	if auto.noReuse {
		t.Error("auto: noReuse set without the flag; reuse must be on by default")
	}
	auto = newAutoFlags()
	if err := auto.parse([]string{"audit the readme", "--no-reuse"}); err != nil {
		t.Fatal(err)
	}
	if !auto.noReuse {
		t.Error("auto --no-reuse: noReuse not set")
	}

	chat := newChatFlags()
	if err := chat.parse(nil); err != nil {
		t.Fatal(err)
	}
	if chat.noReuse {
		t.Error("chat: noReuse set without the flag; reuse must be on by default")
	}
	chat = newChatFlags()
	if err := chat.parse([]string{"--no-reuse"}); err != nil {
		t.Fatal(err)
	}
	if !chat.noReuse {
		t.Error("chat --no-reuse: noReuse not set")
	}
}

// #338: both subcommands' help names the flag with the same description, and
// both synopsis lines advertise it.
func TestNoReuseFlag_HelpText(t *testing.T) {
	var autoHelp strings.Builder
	var usage *usageRequest
	if err := newAutoFlags().parse([]string{"--help"}); !errors.As(err, &usage) {
		t.Fatalf("auto --help = %v, want a *usageRequest", err)
	}
	usage.print(&autoHelp)

	var chatHelp strings.Builder
	chat := newChatFlags()
	chat.set.SetOutput(&chatHelp)
	chat.set.PrintDefaults()

	for name, help := range map[string]string{"auto": autoHelp.String(), "chat": chatHelp.String()} {
		if !strings.Contains(help, "-no-reuse") {
			t.Errorf("%s help does not list -no-reuse:\n%s", name, help)
		}
		for _, phrase := range []string{"By default each planning call scans", "graphs/fragments/", "reuse-catalog.json"} {
			if !strings.Contains(strings.Join(strings.Fields(help), " "), phrase) {
				t.Errorf("%s help for -no-reuse does not say %q:\n%s", name, phrase, help)
			}
		}
		line, ok := usageSynopsisFor(name)
		if !ok || !strings.Contains(line, "[--no-reuse]") {
			t.Errorf("%s synopsis = %q, want it to carry [--no-reuse]", name, line)
		}
	}
}

// #338: reuseOptions is the flag's whole mapping. Off, the coordinator reads
// no catalog, renders no menu and records no reuse; on (the default), the
// planted shape is offered and the plan carries its record.
func TestReuseOptions_MapsTheFlagOntoTheCoordinator(t *testing.T) {
	dir, _ := plantReuseCatalog(t)
	plain := `{"name":"planned","nodes":[{"id":"scan","prompt":"scan","allowed_tools":["Read"]}]}`
	for _, tc := range []struct {
		noReuse  bool
		wantMenu bool
	}{{noReuse: false, wantMenu: true}, {noReuse: true, wantMenu: false}} {
		fake := newCycleFake(map[string]runner.NodeOutcome{"plan-1": {Result: plain}})
		opts := append(reuseOptions(tc.noReuse), coordinator.WithInvocationDir(dir))
		plan, err := coordinator.New(fake, opts...).Plan(context.Background(), "audit the readme", nil)
		if err != nil {
			t.Fatalf("noReuse=%v: Plan: %v", tc.noReuse, err)
		}
		prompt := fake.Invocations()[0].Prompt
		if got := strings.Contains(prompt, reuseMenuMarker); got != tc.wantMenu {
			t.Errorf("noReuse=%v: menu in planner prompt = %v, want %v", tc.noReuse, got, tc.wantMenu)
		}
		if got := plan.Reuse != nil; got != tc.wantMenu {
			t.Errorf("noReuse=%v: plan.Reuse recorded = %v, want %v", tc.noReuse, got, tc.wantMenu)
		}
	}
}
