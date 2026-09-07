# ADR 0040 — A recorded policy says whether it was a ceiling

**Status:** Accepted. `runstate.NodeToolPolicy.MarshalJSON` stamps one derived
field, `allowed_tools_is_a_ceiling`, on every policy it writes. `setting_sources`
is unchanged: still `omitempty`, still recording layer 1 OFF as an absent key.
This amends ADR 0032 §2.7, which said "no new snapshot field", on the one point
that record did not consider — not whether the answer is *derivable*, but
whether a reader derives it.

**Where the addresses point.** Every `file:line` below was re-opened in the
working tree of `lane-snapshot` as this record landed, which is where the code
it describes now sits. The one exception is marked: the defect address in §1 is
given at `841cf83` (`git log -1 --format=%H`, the `main` this branch left),
because this change moved it.

**Date:** 2026-09-08

## 1. What went wrong

`internal/runstate/runstate.go:134` at `841cf83` — `:141` after this change —
records ceiling layer 1 as a `*string` with
`json:"setting_sources,omitempty"`. Isolated — layer 1 ON, restrictive — writes
`"setting_sources": ""`. Unisolated — layer 1 OFF, permissive, the node running
under the operator's standing `Bash(*)` grant — writes nothing, so the key is
absent. The dangerous state is the invisible one and the safe state is a value
that reads as empty at a glance.

On run `20260907-044244.824270000-1` the operator opened node `reply-drafts`'
recorded policy, read

```json
"allowed_tools": ["Read","Grep","Glob","Write","Edit","Bash(git *)"]
```

as the node's authority, concluded it could not have posted GitHub comments, and
posted them by hand. Three issue threads belonging to an outside contributor got
duplicate comments, deleted afterwards through the API. The node had posted them:
that run carried `--accept-loaded-user-config`, so
`internal/coordinator/coordinator.go:868-869` set `policy.SettingSources = nil`
and the six-entry list was a declaration, not a limit — exactly as
`coordinator.go:844-847` and `:858-859` say it is, and as the ceiling table at
`DESIGN.md:2509-2516` describes layers 1 and 2.

The behaviour is intended and is not touched here. What is fixed is the record.

## 2. The corpus

Two independent programs agree, one written for the recount at
`~/.oh-my-graph/runs/20260907-231020.580274000-1/recount.out` and this one,
re-run for this record. It reads key PRESENCE, because a decode into the pointer
field would collapse "absent" and `""` into the same nil — which is the very
distinction being measured:

```python
import json, glob, os
root = os.path.expanduser("~/.oh-my-graph/runs")
absent = empty = nonempty = withmap = 0
for f in sorted(glob.glob(os.path.join(root, "*", "state.json"))):
    if "20260907-231020.580274000-1" in f:  # the in-flight run that asked this
        continue
    try:
        snap = json.load(open(f))
    except Exception:
        continue
    pols = snap.get("tool_policies") or {}
    if not pols:
        continue
    withmap += 1
    for p in pols.values():
        if "setting_sources" not in p: absent += 1
        elif p["setting_sources"] == "": empty += 1
        else: nonempty += 1
print(withmap, absent + empty + nonempty, absent, empty, nonempty)
```

`74 365 251 114 0` — of 385 `state.json` files on this machine, 74 carry a
`tool_policies` map at all; those 74 hold 365 node policies, of which **251 are
unisolated (absent), 114 isolated (`""`), and 0 name a real source list**. The
majority state is the silent one. These figures are a corpus on one maintainer's
machine and reproduce nowhere else; the command is quoted so the number can be
re-derived where the corpus is.

## 3. The verdict

Record the CONSEQUENCE, leave the mechanism alone.

`NodeToolPolicy.MarshalJSON` writes `allowed_tools_is_a_ceiling`, always
present, `p.SettingSources != nil`. It is derived at the encoding boundary — on
the type, the way `Snapshot.MarshalJSON` canonicalizes `runtime`
(`internal/runstate/runstate.go:548-556`), so no writer can persist a policy
without it — and it is read back by nothing: `NodeToolPolicy` gains no Go field,
decoding ignores the key, and the in-memory truth stays `SettingSources` alone.

That is what answers ADR 0032 §2.7's objection to a second field, *"could only
ever disagree with the argv"*: there is no stored copy to drift. A snapshot
whose key contradicts its own `setting_sources` decodes to the pointer and
re-encodes to the recomputed answer
(`TestMarshal_TheDerivedKeyIsNotStored`).

The field claims exactly one thing — that layer 2 did or did not bind. Layers 3
(`--tools`) and 5 (`--disallowedTools`) bind under the opt-in too, and the field
says nothing about them.

Schema stays 3. The key is additive and optional in both directions: `Load`
(`internal/runstate/runstate.go:690-703`) sets no `DisallowUnknownFields` — there
is none in the repository — so an older binary ignores it, and a snapshot written
before this change has nothing to populate wrongly
(`TestLoad_SnapshotWrittenBeforeTheCeilingRecord`, over
`internal/runstate/testdata/pre-ceiling-record-state.json`).

## 4. Rejected

**Drop `omitempty`, so the unisolated state serialises as `"setting_sources":
null`.** The obvious fix, and it changes the bytes without changing what a
reader concludes: `null` beside a six-entry `allowed_tools` list still does not
say that the list did not bind, which is the sentence the operator needed. It
also breaks the two byte-level assertions that hold ADR 0032 §2.7 —
`cmd/oh-my-graph/loadeduserconfig_cli_test.go:142` and `:157` — so the price of
a cosmetic change would be rewriting the tests that guard a resumed leg against
silently re-isolating a run.

**A stored field rather than a derived one.** Any `bool` persisted on
`NodeToolPolicy` decodes to `false` on the 251 existing unisolated policies. For
`allowed_tools_is_a_ceiling` that zero value reads "the list bound" — the exact
misreading, now machine-baked into every old snapshot. A derived field has no
zero value to be wrong about. (It would also fail
`TestNodeToolPolicyMirrorsRunnerToolPolicy`, correctly: it is not a ceiling
layer.)

**A sentence in the snapshot.** A snapshot is a machine record. Prose in one
ages against the code it describes and cannot be branched on.

**Fixing only the screen.** The run-time screen is not silent: the disclosure at
`cmd/oh-my-graph/main.go:1397-1405` prints on `auto`, on `--plan-only`, on every
later goal cycle and on `resume`
(`cmd/oh-my-graph/resume.go:547-552`), off the same predicate
(`cmd/oh-my-graph/main.go:1364-1371`) that reads the same pointer. The operator
who misread this run was not at that screen; they were reading the record
afterwards, which is the surface that had no answer. The after-the-fact CLI
surfaces (`runs show`, `serve`) remain silent and are not addressed here — see
§5.

## 5. Not in scope

`--accept-loaded-user-config` and every ceiling layer are untouched; the four
exec seams stay four; `internal/childenv` keeps ONE list with no runtime branch.
`runfeed` never carried this field and does not start — `events.jsonl` has no
policy field at all (`internal/runfeed/runfeed.go:151-218`). `runs show` and
`serve` still read no `tool_policies`
(`cmd/oh-my-graph/show.go:98`, `internal/serve/card.go:160`); putting the
answer on those surfaces is a separate change, now unblocked because the datum
is on the page.
