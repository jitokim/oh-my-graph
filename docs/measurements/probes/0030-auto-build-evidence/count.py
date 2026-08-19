#!/usr/bin/env python3
"""Count how many of this machine's `auto` runs carried engine-run build evidence.

Reads every ~/.oh-my-graph/runs/*/state.json with the json module — never grep,
which would count the word `verify:` in a prompt string as a verification.

Definitions, both taken from the snapshot and from nothing else:

  auto run          `tool_policies` is non-empty. That is the same
                    auto/hand-written discriminator `resume` uses
                    (internal/coordinator/verifycmd.go, ReattachVerifyCommand's
                    docstring) — a planned graph carries a per-node ceiling and
                    a hand-written one never does.
  build evidence    some node of the snapshot's graph carries
                    success_check.verify. For a planned graph that field can
                    only have come from --verify-cmd: validatePlannedNodeVerify
                    refuses a planner-authored one, and trusted code attaches
                    the user's (ADR 0016 §2).

Zero spawns, zero cost.

It REPORTS by default and asserts only under --check. That order is deliberate
and was the other way round until 2026-08-20: the assertion is against one
machine's corpus at one hour, so a default that asserted made the command ADR
0030 cites as "re-derivable" exit 1 for every reader but this tree, and after
literally the next `auto` run even here. --check is what the frozen numbers in
docs/measurements/0030-auto-runs-carry-no-build-evidence.md are pinned by; it is
for whoever re-runs this on THAT corpus, not for a reader wanting the figure.

What this probe measures is ADR 0016's definition of build evidence — a
`success_check.verify` in the snapshot's graph — which is the question §1 of ADR
0030 asked. It CANNOT answer §8(a), whose four strata are read from the
`build_evidence` block ADR 0030 added: no run in this corpus has one, and the
strata are not derivable from a graph. That measurement needs its own probe, and
it is owed before ADR 0030 leaves Proposed.

Usage:  python3 count.py [--check] [--runs-dir DIR]
"""

import argparse
import collections
import json
import os
import sys

# The flag and its notice both shipped on 2026-08-06 (ADR 0016, implementation
# commit). A run started before that date could not have carried build evidence
# and never saw the notice, so it is reported in its own stratum rather than
# folded into a denominator it cannot answer for.
FLAG_EXISTS_FROM = "20260806"

# What the measurement doc quotes, frozen on 2026-08-20 against this machine's
# 294-run corpus. Checked only under --check (see the docstring). Update both
# together or not at all.
EXPECTED = {"auto": 8, "with_evidence": 1, "post_flag": 2, "post_flag_with_evidence": 1}


def load_graph(raw):
    """state.json's `graph` is opaque JSON bytes; some readers hand back a dict."""
    if isinstance(raw, (str, bytes)):
        return json.loads(raw)
    return raw or {}


def scan(runs_dir):
    rows = []
    for run_id in sorted(os.listdir(runs_dir)):
        path = os.path.join(runs_dir, run_id, "state.json")
        if not os.path.exists(path):
            continue
        with open(path) as fh:
            snap = json.load(fh)
        graph = load_graph(snap.get("graph"))
        verified = [
            node.get("id")
            for node in (graph.get("nodes") or [])
            if (node.get("success_check") or {}).get("verify")
        ]
        rows.append(
            {
                "run_id": run_id,
                "auto": bool(snap.get("tool_policies")),
                "nodes": len(graph.get("nodes") or []),
                "verified_nodes": verified,
                "post_flag": run_id[:8] >= FLAG_EXISTS_FROM,
            }
        )
    return rows


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--runs-dir", default=os.path.expanduser("~/.oh-my-graph/runs"))
    ap.add_argument(
        "--check",
        action="store_true",
        help="assert the frozen numbers in the measurement doc; exit 1 if the corpus has moved",
    )
    args = ap.parse_args()

    rows = scan(args.runs_dir)
    auto = [r for r in rows if r["auto"]]
    with_evidence = [r for r in auto if r["verified_nodes"]]
    post = [r for r in auto if r["post_flag"]]
    post_with = [r for r in post if r["verified_nodes"]]

    print(f"runs scanned:            {len(rows)}")
    print(f"auto runs:               {len(auto)}")
    print(f"  with build evidence:   {len(with_evidence)}")
    print(f"  started on/after {FLAG_EXISTS_FROM} (the flag existed): {len(post)}")
    print(f"    with build evidence: {len(post_with)}")
    print("auto runs by date:       "
          + ", ".join(f"{d}:{n}" for d, n in sorted(collections.Counter(r["run_id"][:8] for r in auto).items())))
    print("per auto run:")
    for r in auto:
        mark = ",".join(r["verified_nodes"]) if r["verified_nodes"] else "-"
        print(f"  {r['run_id']}  nodes={r['nodes']:<2} verify={mark}")

    if not args.check:
        return 0
    actual = {
        "auto": len(auto),
        "with_evidence": len(with_evidence),
        "post_flag": len(post),
        "post_flag_with_evidence": len(post_with),
    }
    if actual != EXPECTED:
        print(f"\nCORPUS MOVED: expected {EXPECTED}, got {actual}", file=sys.stderr)
        return 1
    print("\nmatches the measurement doc.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
