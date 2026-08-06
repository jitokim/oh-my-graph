package main

import "github.com/jitokim/oh-my-graph/internal/runstate"

// acquireRunLock is how a leg — a first `run`/`auto` leg or a `resume` — takes
// its run's resume.lock, and the one place the ordering invariant that ADR
// 0015 §2 derives ABANDONED from is stated rather than merely arranged:
//
//	A leg must hold the lock BEFORE it writes its first event, and must still
//	hold it AFTER it writes its last.
//
// Otherwise a starting run has an open leg and no lock — which is precisely
// the pattern "abandoned" is derived from — for the milliseconds in between,
// and a finishing one has the same gap at the other end. Both call sites
// satisfy it by acquiring before runfeed.NewStreamWriter and releasing through
// a `defer` registered first, so LIFO puts the release after the feed's close;
// move the acquire below the feed writer and every run in the world would read
// abandoned for its first instants, with nothing failing.
//
// The indirection exists so that invariant can be pinned by a test that
// observes the leg's stream at the exact moments the lock is taken and given
// back (TestRunLeg_LockBracketsTheEventStream). Production code never
// reassigns it.
var acquireRunLock = runstate.AcquireLock
