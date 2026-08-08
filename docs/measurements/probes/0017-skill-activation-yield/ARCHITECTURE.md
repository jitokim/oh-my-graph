# Job runner scheduling

## Context

A small local job runner executes short-lived jobs submitted from a CLI. Jobs
are IO-bound, arrive in bursts, and must not outlive the process that started
them.

## Options

### A single-process scheduler

One goroutine drains a queue and runs each job to completion. Simple, no
cross-job state, trivially debuggable; throughput is capped by one job at a time.

### A worker pool

N workers drain a shared channel. Throughput scales with N; costs a bounded
concurrency knob, a shutdown protocol, and per-worker error attribution.

## Decision

Decision: a worker pool, bounded to NCPU-2 workers.
