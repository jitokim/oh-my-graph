package schedule

import (
	"context"
	"sync"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/verify"
)

// The graph both arms below run: two independent sinks, each carrying the same
// kind of evidence command auto mode attaches to a sink (ADR 0016 §2). With a
// ready set of 2 they settle together, so their verifications are genuinely
// contemporaneous — which is what makes "they overlapped" and "they did not"
// distinguishable rather than accidental.
const twoSinkVerifyGraph = `
name: two-sinks
nodes:
  - { id: left,  prompt: left,  success_check: { verify: { command: "build left" } } }
  - { id: right, prompt: right, success_check: { verify: { command: "build right" } } }
`

// gatedVerifier is the choreography double for the serialization tests. Each
// Verify call announces its arrival on arrivals and then blocks on a gate the
// TEST opens — never on a wall clock, and never on anything the scheduler
// controls — so the test drives the interleaving instead of observing whatever
// the machine happened to do.
//
// maxActive is the property under test: the greatest number of verifications
// in flight at one moment.
type gatedVerifier struct {
	arrivals chan string
	gates    map[string]chan struct{}

	mu        sync.Mutex
	active    int
	maxActive int
	order     []string
}

func newGatedVerifier(commands ...string) *gatedVerifier {
	gates := make(map[string]chan struct{}, len(commands))
	for _, command := range commands {
		gates[command] = make(chan struct{})
	}
	return &gatedVerifier{arrivals: make(chan string, len(commands)), gates: gates}
}

func (v *gatedVerifier) Verify(ctx context.Context, req verify.Request) (verify.Result, error) {
	v.mu.Lock()
	v.active++
	if v.active > v.maxActive {
		v.maxActive = v.active
	}
	v.order = append(v.order, req.Command)
	v.mu.Unlock()

	v.arrivals <- req.Command
	select {
	case <-v.gates[req.Command]:
	case <-ctx.Done():
		v.leave()
		return verify.Result{}, ctx.Err()
	}

	v.leave()
	return verify.Result{ExitCode: 0, Output: "ok\n"}, nil
}

func (v *gatedVerifier) leave() {
	v.mu.Lock()
	v.active--
	v.mu.Unlock()
}

func (v *gatedVerifier) peak() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.maxActive
}

// open releases one command's verification.
func (v *gatedVerifier) open(command string) { close(v.gates[command]) }

// TestScheduler_UnserializedVerificationsOverlap is the NEGATIVE CONTROL, and
// it is what stops the test below from measuring nothing. It asserts the
// scheduler's default: both verifications reach the seam and are in flight at
// the same moment, with neither released. If this ever stopped holding — a
// narrower ready set, a lock added somewhere else — the serialization test
// would still pass while proving nothing about serialization.
func TestScheduler_UnserializedVerificationsOverlap(t *testing.T) {
	g := mustGraph(t, twoSinkVerifyGraph)
	r := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"left": pass("s-left", 0.01), "right": pass("s-right", 0.01),
	})
	v := newGatedVerifier("build left", "build right")
	s, h, led := newVerifyHarness(t, r, v, Options{Concurrency: 2})

	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background(), g, h, led) }()

	// Both arrive before either is released. The second receive is the whole
	// assertion: it can only complete while the first verification is still
	// inside the seam.
	first, second := <-v.arrivals, <-v.arrivals
	if first == second {
		t.Fatalf("both arrivals were %q — the two sinks did not both verify", first)
	}
	v.open(first)
	v.open(second)

	if err := <-done; err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if peak := v.peak(); peak != 2 {
		t.Errorf("peak concurrent verifications = %d, want 2 — without this the serialization test proves nothing", peak)
	}
}

// TestScheduler_SerializedVerificationsRunOneAtATime is the property ADR 0016
// §2 calls load-bearing. It is asserted positively: the second verification's
// arrival is RECEIVED, after the first has been released, so this cannot pass
// because a check never ran.
//
// Two things rest on it. Flake — two concurrent `./gradlew build` invocations
// in one project directory contend for the daemon's locks. And soundness —
// verifyEvidence runs at the START of a node's settlement, before
// PersistOutput and recordPass, so a check does not observe the final tree by
// finishing last; what carries "a passing run means the final tree passed the
// command" is that the last-EXECUTED check runs after every other node's
// subprocess has ended. Deleting the lock breaks the second reason silently,
// which is why this test exists at all.
func TestScheduler_SerializedVerificationsRunOneAtATime(t *testing.T) {
	g := mustGraph(t, twoSinkVerifyGraph)
	r := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"left": pass("s-left", 0.01), "right": pass("s-right", 0.01),
	})
	v := newGatedVerifier("build left", "build right")
	s, h, led := newVerifyHarness(t, r, v, Options{
		Concurrency:           2,
		SerializedVerifyNodes: map[string]bool{"left": true, "right": true},
	})

	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background(), g, h, led) }()

	first := <-v.arrivals
	v.open(first)
	// The second arrival is only reachable once the first has left the seam.
	// Under the control above, this receive would already have completed
	// before the release.
	second := <-v.arrivals
	if second == first {
		t.Fatalf("the same command %q verified twice; the second sink never ran", first)
	}
	v.open(second)

	if err := <-done; err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if peak := v.peak(); peak != 1 {
		t.Errorf("peak concurrent verifications = %d, want 1 — two builds ran at once in the same directory", peak)
	}
}

// TestScheduler_SerializationIsScopedToItsNodes proves the mutual exclusion is
// a per-node opt-in, not a run-wide behaviour change: a graph whose sinks are
// not in the set keeps today's concurrency. Hand-written graphs run without a
// coordinator, so a serialization that silently applied to everybody would
// slow every existing multi-verify graph down for a feature they never asked
// for.
func TestScheduler_SerializationIsScopedToItsNodes(t *testing.T) {
	g := mustGraph(t, twoSinkVerifyGraph)
	r := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"left": pass("s-left", 0.01), "right": pass("s-right", 0.01),
	})
	v := newGatedVerifier("build left", "build right")
	// Only ONE of the two sinks is serialized, so the pair can still overlap.
	s, h, led := newVerifyHarness(t, r, v, Options{
		Concurrency:           2,
		SerializedVerifyNodes: map[string]bool{"left": true},
	})

	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background(), g, h, led) }()

	first, second := <-v.arrivals, <-v.arrivals
	v.open(first)
	v.open(second)

	if err := <-done; err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if peak := v.peak(); peak != 2 {
		t.Errorf("peak concurrent verifications = %d, want 2: a node outside the set must keep today's behaviour", peak)
	}
}
