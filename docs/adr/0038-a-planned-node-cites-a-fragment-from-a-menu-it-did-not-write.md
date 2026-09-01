# ADR 0038 — A planned node cites a fragment from a menu it did not write

**Status:** Proposed. Decision record only — no code, no schema change, no
graph shipped with this record. Nothing in `internal/` or `graphs/` moves
until this is Accepted.

**Where the addresses point.** Read on branch `lane-reuse` at `e767bd9`, the
`main` this branch left. Every `file:line` below was re-opened against that
tree rather than copied from the survey brief that preceded this record; where
a line and a symbol could disagree, the symbol is the address that keeps.

**Where the evidence comes from.** Two independent readings, agreeing: the
repo's own loader (`./bin/oh-my-graph lint graphs/*.yaml`, whose fragment
disclosure lines name each splice) and a full read of all fourteen YAML files
under `graphs/`. No number in this record came from `grep`.

**Date:** 2026-09-02

**Number.** `git ls-tree --name-only main docs/adr/` and `ls docs/adr` both
have maximum `0037`. `0035` is a deliberate gap from the 2026-08-29
renumbering (`docs/adr/0037-a-planned-node-answers-with-the-model-the-operator-chose.md:12`)
and is not reused. `0038` is the next number.

---

## 1. Context

### 1.1 The goal, and the refusal that is not in its way

The maintainer's ask: *`auto` has to get more useful — make the node when
there isn't one, and after that reuse the usable nodes when composing a graph;
this has to happen inside `auto`.*

Today `auto` cannot reuse anything. A planner reply carrying `use:`/`with:` is
refused at `internal/coordinator/coordinator.go:639`:

> `planned node %q references a fragment (use:/with:); planned nodes may not
> reference fragments — trusted code resolves local files, the planner never
> names them`

The refusal fires at the `graph.Parse` boundary
(`coordinator.go:624`, recognised at `:636-641`), *before* the per-node checks
in `validatePlannedNodes` (`coordinator.go:1014`) ever run — the comment at
`coordinator.go:626-630` says so, and records that a `validatePlannedNodes`
case would be unreachable. Its structural backstop is
`(*Graph).validateFragmentsResolved` (`internal/graph/validate.go:621`),
reached from `Issues` at `validate.go:147`, whose distinct
`UnresolvedFragmentError` type exists precisely so the coordinator can
recognise it (`validate.go:30-38`). The refusal is `repairable: true`
(`coordinator.go:641`) — one paid re-plan.

**This record keeps that refusal, and does not weaken it.** The reason is
stated in the code itself (`coordinator.go:632-635`): a planner-emitted `use:`
would let unreviewed plan output pick which local file's prompt text, tool
grant and verify command run. It is the same hole `validatePlannedNodeAgent`
(`coordinator.go:1462`) closes for `agent:`, and the reason ADR 0022 exists.
`internal/coordinator/`'s line in `CLAUDE.md:108` states the invariant in one
clause: *agent mapping — scanned from `~/.claude/agents` only, with the matched
definition staged.* Trusted code resolves; the plan names nothing.

Worth stating plainly, because it changes what "add reuse" means: the planner
is not *told no* about fragments today — it is never told they exist. The
planner prompt (`plannerPromptTemplate`, `coordinator.go:1698`) does not
mention fragments, and the prohibition list at `coordinator.go:1741-1742`
names `permission_mode, budget_usd, type, cwd, agent, worktree,
success_check.verify` — `use:`/`with:` are absent. The refusal is purely
structural. So the change this record proposes is not the removal of a "no";
it is the first "yes" the planner has ever been offered.

### 1.2 The shape this would be the third application of

| | ADR 0017 (skills) | ADR 0022 (agents) |
|---|---|---|
| what trusted code stages | the whole scanned corpus, no selector (`0017:463-476`) | the one matched definition, with source path + SHA-256 (`0022:126-128`) |
| where | `<run-dir>/skills-plugin/` (`0017:504-510`) | `<run-dir>/agents-plugin/` (`0022:129-132`) |
| re-checked | — | `GuardAgentStaging` before **every** spawn, deleting unmanifested paths and restoring changed bytes (`0022:133-136`, `internal/coordinator/agentstage.go:340`) |
| what the planner may emit | **nothing**; `plannedToolAllowlist` is not extended (`0017:410-418`) | **nothing**; `agent:` stays refused (`coordinator.go:1462`) |
| scan scope | user scope | `~/.claude/agents` only, narrowed 2026-08-12 (`0022:142-149`) |

ADR 0017 states the property this record must preserve
(`0017:415-418`): *"a planner that can name `Skill` in `allowed_tools` is a
planner that can select which of the user's local files gets loaded into a
node it authored. That is the hole `validatePlannedNodeAgent` closes for
agents, and it stays closed."*

### 1.3 What is actually in the library, measured

`ls graphs/fragments` → six files. Read in full; the fields a catalog would be
built from are all present in all six:

| fragment | `fragment:` | `substitutions:` | form | `exit:` |
|---|---|---|---|---|
| `e2e-verify.yaml` | `:20` | `:22` — checks, verify_command | `node:` (1) | — |
| `gated-lane.yaml` | `:42` | `:44` — repo, task, tools, checks, verify_command, focus, publish | `nodes:` (4) | `pr` (`:45`) |
| `pr-publish.yaml` | `:26` | `:28` — publish | `node:` (1) | — |
| `repair-round.yaml` | `:29` | `:31` — review_focus, review_agent, review_timeout, apply_scope, verify_command | `nodes:` (2) | `apply` (`:32`) |
| `review-security.yaml` | `:9` | `:11` — diff, evidence | `node:` (1) | — |
| `review-style.yaml` | `:8` | `:10` — diff, focus | `node:` (1) | — |

Each also carries a one-line `description:` (`e2e-verify.yaml:21`,
`gated-lane.yaml:43`, `pr-publish.yaml:27`, `repair-round.yaml:30`,
`review-security.yaml:10`, `review-style.yaml:9`).

**Where each substitution point lands** — the measurement this whole record
turns on. Read from the six files:

| fragment | slot | lands in |
|---|---|---|
| `pr-publish` | publish | `prompt:` (`:32`) |
| `review-security` | diff, evidence | `prompt:` (`:15`, `:16`) |
| `review-style` | diff, focus | `prompt:` (`:14`, `:15`) |
| `e2e-verify` | checks | `prompt:` (`:27`) |
| `e2e-verify` | **verify_command** | **`success_check.verify.command`** (`:65`) |
| `gated-lane` | repo, task, focus, publish | `prompt:` (`:50`, `:56`, `:124`, `:147`) |
| `gated-lane` | **tools** | **`allowed_tools`** (`:86`) |
| `gated-lane` | checks, verify_command | a nested `use: e2e-verify`'s `with:` (`:102`, `:103`) |
| `repair-round` | review_focus, apply_scope | `prompt:` |
| `repair-round` | **review_agent** | **`agent:`** (`:41`) |
| `repair-round` | **review_timeout** | **`timeout:`** (`:62`) |
| `repair-round` | **verify_command** | **`success_check.verify.command`** (`:101`) |

**Three of six** have every slot landing in prompt text. **Three of six** have
at least one slot landing in a field a planned node is forbidden to write.

Shipped citations, for scale: fourteen `use:` lines are written across four of
the nine graphs (`adr-driven-dev.yaml:272,:291`; `backlog-batch.yaml:173,:241,:253,:271`;
`dev-review-pr.yaml:63,:96,:104,:117`; `self-dev.yaml:67,:95,:103,:111`), and
the loader performs seventeen splices, because `gated-lane` itself cites three
fragments (`gated-lane.yaml:99,:119,:143` — ADR 0029). Fourteen is the written
count; seventeen is the resolved count. This record uses **written** unless it
says otherwise.

### 1.4 The one load-bearing gap in the pipeline

Fragment resolution is `resolveFragments(doc, entryPath)`
(`internal/graph/fragment.go:608`), and the file lookup is
`filepath.Join(filepath.Dir(entryPath), "fragments", name+".yaml")`
(`fragment.go:1319`, inside `loadFragmentCached`). **An `auto` plan has no
entry path** — it arrives as `graph.Parse([]byte(spec))` (`coordinator.go:624`)
and `Parse` has no file context by construction (`validate.go:31-33`).

Everything downstream of the lookup is indifferent to who chose the name:
`loadFragmentFile` (`fragment.go:1330`) and its judges, `bindingsFor`,
`namespaceNode` (`fragment.go:1259`, which reads only `usingID` and the
fragment's own declarations), `splicedID` (`fragment.go:396`) and
`Graph.Validate` (`validate.go:80`) are all facts about files and ids. That is
the asset here: the resolution machinery is reusable unchanged. The gap is
narrow — *an identifier and a base directory* — and that is exactly the shape
a staged catalog fills.

---

## 2. Decision

### 2.1 (A) The decision in one sentence

**Trusted code scans the invocation repository's `graphs/fragments/` sibling,
admits only fragments whose every declared substitution point lands in prompt
text, offers the planner that admitted set as a menu of identifiers, and — after
`validatePlannedNodes` has run — performs every path resolution and splice
itself; the planner may name an identifier from the menu and bind that entry's
listed slots, and may never name a path, a file, an unlisted slot, or an
identifier that is not on the menu.**

### 2.2 (B) What the planner is shown, verbatim

The block is appended to `plannerPromptTemplate` (`coordinator.go:1698`) and is
**omitted entirely** when the admitted set is empty (§2.5). Literal text, with
two entries from today's admitted set:

```
Reusable shapes the operator already keeps. Each is a node (or a group of
nodes) that has been written, reviewed and run before. Prefer one of these
over writing an equivalent node yourself.

- id: review-style
  contributes: 1 node, merged into the node that names it
  binds: [diff, focus]
  summary: read-only style/naming/simplicity review of a diff, with the
    test-double discipline check

- id: pr-publish
  contributes: 1 node, merged into the node that names it
  binds: [publish]
  summary: publish the branch as a pull request — the URL is the payload
    that makes the verdict an assertion

To use one, set "reuse" on a node to an id from the list above and give
"bind" a value for every slot in that entry's "binds". Example:

  {"id": "style-check", "type": "claude-run", "depends_on": ["impl"],
   "reuse": "review-style",
   "bind": {"diff": "the diff impl produced", "focus": ""}}

A node that sets "reuse" writes no prompt and no allowed_tools of its own:
the shape supplies both. It still writes its own id and depends_on.

You may write an id that appears above and nothing else. Never a file path,
never a file name, never a slot that is not in that entry's "binds", never an
id that is not on this list. Any of those is rejected outright.
```

**Which fields trusted code derives, and from what.** All four, by parsing the
fragment file with the loader that already parses it (`loadFragmentFile`,
`fragment.go:1330`) — never by reading text out of it:

| field | derived from |
|---|---|
| `id` | the file's `fragment:` key (e.g. `review-style.yaml:8`) |
| `contributes` | the parsed form: `node:` → *"1 node, merged into the node that names it"* (the single-node body is spliced onto the citing node and declares no id — `fragment.go:984-986`); `nodes:` → the declared ids, rendered as `<your-node-id>/<internal-id>` |
| `binds` | the file's `substitutions:` key, filtered to the slots the body actually references (`fragmentFile.referenced`, `fragment.go:454`) |
| `summary` | the file's `description:` key, verbatim, one line |

**What is not shown.** No path. No file name. No directory. No fragment body,
no `prompt:` text, no `allowed_tools`, no `success_check`, no `verify`
command, no line of the file beyond the four derived fields above. A planner
that sees this menu cannot tell where the files are, how many other files sit
beside them, or what any of them contains.

**What the planner may emit.** Exactly two new keys on a node: `reuse:`, whose
value must match an id in the menu it was shown, and `bind:`, a flat map whose
keys must be exactly that entry's `binds` and whose values are strings.
Nothing else.

**What the planner may still not emit.** A path or file name in any position.
`use:` or `with:` — those spellings stay refused at `coordinator.go:639` and
`validate.go:621`, unchanged, because that backstop also guards snapshots and
replayed specs, not only plans. An id absent from the menu. A slot absent from
the entry. And every field `validatePlannedNodes` refuses today —
`cwd` (`coordinator.go:1333`), `success_check.verify` (`:1353`),
`agent` (`:1462`), a `/` in an id (`:1484`), `worktree` (`:1503`) — remains
refused on a `reuse:` node exactly as on any other.

**Why `bind:` is allowed at all, when `with:` is not.** This is the one place
this record grants the planner something new, so the reason has to be exact. A
slot is **inert** when every `{{ with.X }}` occurrence of it in the fragment
body lies inside a `prompt:` scalar. Only fragments whose slots are *all*
inert are admitted to the catalog (§3). A binding for an inert slot therefore
lands where planner-authored `prompt:` text already lands, and grants the
planner nothing it does not already hold — the planner writes whole prompts
today. Inertness is computed by trusted code from the parsed fragment file at
catalog time and **re-computed at splice time from the file actually read**; it
is never taken from the plan, and never from the catalog's own record.

Admission is all-or-nothing. A fragment with one non-inert slot is not
partially admitted with that slot left unbound: blanking
`repair-round.yaml:101`'s `verify_command` would produce a node whose engine
evidence command is the empty string — ADR 0030's hazard, manufactured by the
mechanism meant to help.

### 2.3 (C) The three failure cases

The plan is untrusted output, so each case is answered at a named point in the
pipeline. The pipeline order this record fixes:

```
planner reply
  → graph.Parse                       (coordinator.go:624)
  → validatePlannedNodes              (coordinator.go:1014)   ← C.1 answered here
  → catalog splice                    NEW, first of the post-validation
                                      mutations at coordinator.go:691-729
                                      ← C.2 and C.3 answered here
  → Graph.Validate on the spliced graph (validate.go:80)
  → applyAgentMapping                 (coordinator.go:699)
  → attachVerifyCommand               (coordinator.go:712)
  → applySkillActivation              (coordinator.go:727)
  → save graph.json                   (cmd/oh-my-graph/main.go:1135, :733)
```

Splicing lands **first** among the post-validation mutations so that agent
mapping and verify attachment see the spliced nodes, and **before** the spec is
saved, so `graph.json` holds the *resolved* graph. `reuse:`/`bind:` therefore
never appear in a persisted artifact, `run graph.json` keeps working with no
file context, and the `validateFragmentsResolved` backstop keeps its present
meaning. This is ADR 0022 §3's ordering and ADR 0017 §2's — trusted code
mutates strictly after the plan has been judged.

#### C.1 — the plan cites an id that is not in the catalog

**The engine rejects the plan.** A new `validatePlannedNodeReuse` case in
`validatePlannedNodes` (`coordinator.go:1014`) refuses any `reuse:` whose value
is not in *the menu this plan was shown* — the offered set, held from the same
call that rendered the prompt, not a re-scan of the disk. Same treatment for a
`bind:` whose key set is not that entry's, and for a `reuse:` node that also
writes its own `prompt:`.

**Where:** with the other per-node refusals, before any file is opened.
**Repairable:** yes — one paid re-plan, like the existing refusals, and the
refusal text names the id and lists the menu, so the repair prompt carries
enough to converge.
**What the operator sees:** the ordinary plan-refusal line, e.g.
`planned node "style-check" names the reusable shape "review-styles", which is
not one of the shapes offered (offered: review-style, review-security,
pr-publish)`.

The field-disposition rule at `coordinator.go:995-1002` binds here: `reuse:`
and `bind:` are new `graph.Node` fields and must each get an explicit
disposition in `validatePlannedNodes`' docstring, or
`TestPlannedNodeFieldDispositionsAreComplete`
(`internal/coordinator/field_dispositions_test.go:266`) reddens. That is the
mechanism this record relies on to stop the hole recurring, and it is why
`reuse:` is a new key rather than a re-legalised `use:`.

#### C.2 — the fragment's content changed between plan time and run time

**The engine pins a digest, and refuses the run rather than the citation.**
This is ADR 0022's manifest, applied to a second artifact class. At catalog
time, trusted code records for every *offered* entry: the id, the absolute
source path, the file size, and its SHA-256 — the same three facts
`newAgentStaging` records (`0022:126-128`, `agentstage.go:181`,
`agentstage.go:301`). The record is written to
`<run-dir>/reuse-catalog.json`, beside `graph.json`, owner-only, as this run's
statement of what it offered and what the plan took.

At splice time the file is re-read and re-hashed. A mismatch is **not** a
dropped citation and **not** a silent re-splice of the new bytes: it fails the
plan with the id, the path, and both digests. The operator confirmed a
topology derived from bytes that no longer exist; substituting different ones
under the same name is the failure mode, not the recovery.

Note the difference from `GuardAgentStaging` (`agentstage.go:340`), which
re-materialises before *every* spawn because the hazard there is what the
previous node wrote. A fragment is a **load-time** splice (ADR 0013's title):
after resolution there is nothing left to re-check, because there is no file
the run still reads. One check, at splice, is the whole obligation.

**Where:** in the catalog-splice step, before `Graph.Validate`.
**What the operator sees:** a refusal naming the shape and the path, and the
plan is preserved as a refused spec under the existing rejected-spec name
(`cmd/oh-my-graph/main.go:1141`), not discarded.

#### C.3 — the same fragment cited twice, and node-id collision

**ADR 0027's `/` namespace fully covers the case it applies to, and on today's
admitted set it never applies at all.** Both halves matter.

*What 0027 covers.* `0027:669-672`: *"Two uses of one fragment in one graph
produce `qa-a/impl` and `qa-b/impl` and cannot collide."* This is not theory —
`graphs/adr-driven-dev.yaml` cites `repair-round` twice (`:272`, `:291`) and
the loader reports `round1/review, round1/apply` and `round2/review,
round2/apply`. The distinguishing prefix is the **citing node's id**, read from
the graph by `resolveNode` (`fragment.go:901`), joined by `splicedID`
(`fragment.go:396`) with the separator constant at `fragment.go:393`. The
comment at `fragment.go:389-392` states why it cannot collide with an authored
id: no author and no planner can write `/` — `refuseAuthoredNamespaces`
(`fragment.go:716`) refuses it in files, `validatePlannedNodeID`
(`coordinator.go:1484`) refuses it in plans. Every input to the mechanism is
trusted code plus a local file; the plan supplies nothing. So the coverage
carries over to a catalog citation **unchanged**, with no new rule.

*Where it does not apply.* A **single-node** fragment mints no namespace: its
body is spliced onto the citing node and declares no id of its own
(`fragment.go:984-986`, and the same fact restated at `fragment.go:1142-1144`).
Two citations of `review-style` produce two nodes bearing the *planner's own
two ids* — and duplicate planned ids are already refused by `Graph.Validate`'s
uniqueness check, reached from `validate.go:147`'s issue set. There is no
remainder here either, and no `/` is minted.

*The honest conclusion.* **All three fragments admitted by §3's inertness test
on today's corpus are single-node** (`pr-publish`, `review-security`,
`review-style` — forms at `pr-publish.yaml:29`, `review-security.yaml:12`,
`review-style.yaml:11`). ADR 0027's namespace machinery is therefore **fully
adequate and entirely unexercised** by this decision as it would ship: correct
for the multi-node case, and the multi-node case is currently empty. What
closes the remainder is nothing — there is no remainder — but the reason is
contingent on the corpus, not structural, so it must be re-checked the moment
a multi-node fragment becomes admissible. That is a §6 falsification hook, not
a §2 mechanism.

One consequence to record rather than discover later: a spliced multi-node
fragment mints `/` ids *after* `validatePlannedNodeID` has run, which is
consistent — `nodeIDPattern` (`validate.go:353`) accepts
`segment(/segment)*`, so the spliced graph passes `Graph.Validate`, and the
planned-node refusal of `/` continues to mean what it says: *the planner may
not write one.*

### 2.4 (D) New fragments — deferred, and this record says so plainly

**A design that only reads the catalog and never adds to it answers half the
maintainer's ask.** `노드가 없으면 만들고` is the first half; this record covers
the second. That is deliberate, and it is a deferral to a second ADR, not an
omission.

**Why deferred, in order of weight.**

1. **It is a different trust question, not a harder version of the same one.**
   Everything in §2 is trusted code *reading* the operator's files. Writing a
   fragment makes oh-my-graph author a file in the operator's repository that
   later runs will treat as *the operator's own reviewed shape*. The
   provenance label and the provenance stop matching.
2. **The self-citation loop is the actual hazard, and §C.2's digest does not
   touch it.** A fragment distilled from planner output, cited by a later
   planner, is planner text re-entering a later run through a channel whose
   whole premise is "this came from the operator". The digest pin proves the
   bytes did not change; it says nothing about who wrote them. Closing that
   needs a provenance field, a disclosure rule, and probably a separate
   directory — three decisions that would be buried as a subsection here.
3. **The order is the safer one.** Whether `auto` should grow the library is
   only worth deciding if `auto` will cite the library. §6's falsification
   answers that with a cheap read-only experiment. Deciding to write first
   risks a bigger library that is cited exactly as often as the empty one.

**What the second ADR must decide**, so the deferral is a queue entry and not
a shrug:

- **Who writes the file.** Trusted code, from an accepted plan's node bodies —
  never the planner naming a path, which would reopen §1.1's refusal from the
  write side.
- **From what.** Which nodes qualify: this record's position, offered as the
  starting proposal rather than a decision, is *only* nodes that PASSed with an
  engine-run `verify` (ADR 0033's run-is-the-unit), because a node that
  self-reported success is not evidence of a shape worth keeping.
- **Where.** ADR 0013 gives exactly one location and no search path
  (`fragment.go:1319`, and the error text at `fragment.go:1337` spells the
  rule out). A written fragment either goes there — into the operator's
  repository — or the location rule grows a second case. Both are real
  changes.
- **The name**, and what happens when it collides with an existing fragment.
- **Default or opt-in.** This record's position: explicit opt-in, for reason 1.
- **Provenance**, per reason 2: how a machine-distilled fragment is marked, and
  whether §2's catalog offers it at all.

### 2.5 (E) What the operator can turn off

**The switch:** `--no-reuse` on `auto` (and on `chat`'s planning path, which
shares `plannerPrompt`, `coordinator.go:1564`).

**The default: on** — the catalog is offered. Reading `graphs/fragments/`
beside the invocation directory is already what `run` does for every
hand-written graph that carries a `use:` (`fragment.go:1319`); this adds no new
class of read, and a feature that must be discovered by flag is a feature the
measurement in §6 would never see used.

**Three ways a run has no catalog, and they are indistinguishable by design:**
`--no-reuse`; no `graphs/fragments/` sibling; or a `graphs/fragments/` in which
no file passes §3's inertness test. In all three the catalog block of §2.2 is
**omitted entirely** — not rendered as an empty list. An empty menu spends
prompt tokens teaching a vocabulary with no words in it and invites the planner
to invent an id, which is then §C.1's refusal charged to the engine's own
prompt.

**What a run looks like with it off:** exactly today's run. No catalog block in
the planner prompt; `reuse:` refused by the same disposition case that governs
it when on; no `reuse-catalog.json` in the run directory; the plan printout
(`printPlanForRuntime`, `cmd/oh-my-graph/main.go:1205`) says nothing about
reuse.

**With it on, the plan printout must say so**, following ADR 0022 §8's rule
that a claim about the operator's files on disk has to put the path on the
screen: one line naming the directory scanned, the count offered, the count
admitted-vs-skipped, and — per citation — the id, the source path and the
digest recorded in `reuse-catalog.json`. `printPlanForRuntime` already carries
this class of disclosure for agents and skills (`main.go:1243-1244`).

---

## 3. Whether the 0017/0022 shape holds here — it does not, as posed

The candidate shape was: *the engine stages a catalog of the operator's
fragments and offers the planner a list of identifiers the planner did not
write; the planner selects from a menu instead of resolving a path, so trusted
code still performs every resolution.*

**It breaks, at `with:`, and the break is not a detail.**

A skill and an agent are *closed* artifacts. Trusted code stages a file; the
node loads it; the planner supplies no parameter to it — `0017:410-418` and
`coordinator.go:1462` both come down to "the planner emits nothing." A
fragment is not closed. It is a **template with open string slots**
(`substitutions:`), and §1.3 measured where those slots land. A planner that
may write `with:` reaches, *through trusted splicing*:

- `success_check.verify.command` — `e2e-verify.yaml:65`,
  `repair-round.yaml:101`. The engine runs this through `sh -c`, outside every
  per-node guard. `validatePlannedNodeVerify` (`coordinator.go:1353`) refuses a
  planner-authored one *outright*. `repair-round.yaml:16-18` warns the human
  author of exactly this, about artifacts.
- `allowed_tools` — `gated-lane.yaml:86`. Bounded for a plan by
  `validatePlannedNodeTools` (`coordinator.go:1516`) against
  `plannedToolAllowlist` (`coordinator.go:70`).
- `agent:` — `repair-round.yaml:41`. Refused outright by
  `validatePlannedNodeAgent` (`coordinator.go:1462`), the hole ADR 0022 exists
  to close.

So the naive menu is a laundering channel: it returns the planner all three of
the fields it is specifically forbidden to write, wrapped in a mechanism whose
selling point is that trusted code performs the resolution. Trusted code
resolving the *path* while the planner fills the *shell command* is not the
0017/0022 property. It is the property's name attached to its opposite.

**The alternative taken instead** is §2's: keep the menu, and narrow what may
be on it to fragments where the break cannot occur. A slot is inert when every
`{{ with.X }}` occurrence of it lies inside a `prompt:` scalar; a fragment is
admissible when all its slots are inert. On today's corpus that is three of
six (§1.3), and the three excluded are excluded for a reason a reader can
check in one line each.

**Why the narrowed form is genuinely the 0017/0022 property and not a weaker
cousin:** on an admitted fragment, the planner's `bind:` values reach only
prompt text, which is a channel the planner already owns outright. The set of
things a planner can cause is unchanged by this decision; only the *amount of
already-reviewed text it can reach* grows. That is what "reuse" was supposed to
mean.

**Two places where this record departs from 0017/0022 and must own it:**

1. **0017 refused a plan-time selector**; this record installs one. `0017:463-476`
   argues that any rule good enough to pick a skill *is* a selector, and the
   only one this project measured got 7% recall with one of five mappings
   semantically wrong. The disanalogy is real: a skill has a **run-time**
   selector — the CLI's own description gate, firing when the node knows its
   task. A fragment has none, because a fragment is a load-time splice (ADR
   0013). If reuse is to exist at all, something must choose at plan time, and
   the only candidate that has the goal in view is the planner. 0017 does not
   endorse this; it simply does not reach it.
2. **0022 (l) narrowed the agent scan out of repository scope** (`0022:142-149`),
   and this record adds a read of the *invocation repository's*
   `graphs/fragments/`. That is the same direction, walked forward. The
   defensible difference: a staged agent becomes the node's **system prompt**,
   through a channel `--setting-sources ""` cannot shut and which is
   deliberately invisible in `graph.json` (`0017:420-425`). A spliced fragment
   becomes **nodes on the operator's screen** — `printPlanForRuntime`
   (`main.go:1205`) prints every node id, its dependencies and its tools, and
   the resolved graph is what `graph.json` holds. A repository that plants a
   fragment gets nodes the operator reads before the run; a repository that
   planted an agent got a system prompt nobody could see.

   **The residual, stated rather than hidden:** the plan is always *printed*,
   but only *asked about* when the run is not already committed —
   `confirmPlan` runs under `if !committed` (`main.go:723-731`), while the
   committed path prints and proceeds (`main.go:738`). So "the operator sees
   it first" is a claim about the interactive path. On the committed path the
   disclosure lands in the transcript and in `reuse-catalog.json`, and nothing
   blocks. Whether that is sufficient is the sharpest open question in this
   record, and §6 does not resolve it.

---

## 4. (G) Consequences

**Prompt size — unmeasured.** ADR 0017 §4 measured its comparable at **+6,008
prompt tokens per invocation for 35 skills, ~172 per skill** (`0017:481-494`).
No equivalent measurement exists for this catalog and none is claimed: the
entry shape is four short fields and the raw material is the six one-line
`description:` values quoted in §1.3, but the token cost of the rendered block
is `unverified`. Two structural differences point the same way and are
reasoning, not measurement: this catalog is charged **once per planning call**,
not once per node invocation, and it is charged against a prompt template that
is already ~110 lines (`coordinator.go:1698-1808`). An Accepted status should
require the measurement, in the `docs/measurements/` form ADR 0022 used
(`0022:13-19`).

**Plan-time coupling to the operator's filesystem.** New, and real. Today a
planner reply is a function of the goal, the inputs and the template; with this
change it is also a function of what sits in `graphs/fragments/` at plan time.
Two runs of the same goal in two directories can plan differently, and neither
is wrong. The `reuse-catalog.json` sidecar exists so that the difference is
recoverable after the fact rather than mysterious. It also means the planning
call is no longer reproducible from the goal alone — a property nothing
currently depends on, so far as this record checked (`unverified`: no audit of
consumers was performed).

**A stale catalog.** Three distinct staleness windows, with three different
outcomes:

| window | outcome |
|---|---|
| catalog built → prompt rendered | none; same call |
| plan accepted → splice | §C.2 — digest mismatch fails the plan, naming both digests |
| after the splice | none. The splice is load-time; the run reads no fragment file again |

The one that has no answer here is *semantic* staleness: a fragment whose
bytes are unchanged but whose meaning has drifted from its `description:`. The
loader already has a drift channel for fragments (`lf.advisories`,
`fragment.go:1321`); whether a catalog entry should be suppressed by an
advisory is left open, and named as owed.

**Blast radius.** ADR 0027 already warns that a fragment edit is a multi-graph
change. This widens it: a fragment edit is now also a change to what future
`auto` runs are offered, in a repository whose owner may not have written any
graph at all.

**What gets cheaper.** The three admitted shapes are already-reviewed prompts
with settled verdict contracts — `review-style.yaml:19-29` is a worked example
of the verdict-token discipline that planner-authored review nodes get wrong.
A cited node inherits it. That is the actual usefulness the maintainer asked
for, and it is worth naming that it is bounded to three shapes today.

---

## 5. Alternatives considered

**Legalise `use:`/`with:` for planned nodes.** Rejected — §1.1, and it is the
thing the maintainer explicitly ruled out. The refusal at `coordinator.go:639`
is correct.

**Offer the whole library, `with:` included.** Rejected — §3. It is the
candidate shape as posed, and it hands back `verify`, `allowed_tools` and
`agent:`.

**Menu of ids with no `bind:` at all.** Rejected on measurement: all six
shipped fragments declare substitutions (§1.3), so the admitted set would be
**empty** and the mechanism would ship citing nothing. It would also make §6's
falsification untestable in exactly the way §6 warns about.

**Trusted code chooses the fragment, not the planner** — the pure 0017 shape.
Rejected: `0017:463-476`'s own argument is that a trusted-code selector at plan
time is the measured defect (ADR 0012's 7%). A fragment has no run-time gate to
fall back on, so this alternative is the bad half of 0017 with none of the
good half.

**Stage the fragments into `<run-dir>/` like skills and agents.** Not needed,
and rejected as cost with no property: staging exists so a *spawned process*
loads a file (`--plugin-dir`, `0017:369-370`). Nothing spawns a fragment. The
splice happens in-process before any subprocess exists, and §C.2's digest is
the whole integrity obligation.

---

## 6. (F) Falsification — what would show this wrong

**Pre-registered, before implementation.**

**The primary observation.** Over the first **N = 20** `auto` runs in which a
**non-empty catalog was offered**, if the number of nodes carrying a
`reuse:` citation is **0**, this design is falsified as *built*.

**Where the count is read from.** `<OMG_HOME>/runs/*/reuse-catalog.json` — the
sidecar §C.2 requires, beside `graph.json`
(`generatedSpecFileName`, `cmd/oh-my-graph/main.go:1135`). It carries both
terms: the ids **offered** (the denominator — a run with an empty catalog is
not in the sample) and the ids **cited** (the numerator). Read by parsing the
JSON, not by grep — this repository has a scar on that distinction, and the
survey preceding this record found the injected claim *"the planner prompt
mentions fragments 13 times"* was a whole-file grep count against a true
in-prompt count of **0**. The run feed is deliberately not the source: its
`EventType` set is closed per version (`internal/runfeed/runfeed.go:52-56`),
so counting from it would mean a consumer-contract change to answer a
question a sidecar answers for free.

**The candidate falsifier, assessed.** *"If the catalog is offered and planned
graphs still cite nothing after N runs, then the constraint was never the
refusal — it was that the operator's fragments are not general enough to be
worth citing, which is a different problem this ADR would not fix."*

This is a **real and likely** alternative explanation, and it is the strongest
argument against shipping §2. §1.3's measurement is circumstantial support for
it: the two most-copied shapes in the shipped corpus (`repair-round`, the pair
`0027:33` reports 13 of 18 operator lanes writing out longhand; and
`gated-lane`, the whole-lane shape) are **both excluded** by §3's inertness
test, because both bind an engine-run verify command. That 13-of-18 is
`unverified` here: the lane corpus it was measured over is not in this
worktree, so this record quotes ADR 0027's 2026-08-16 measurement rather than
re-taking it. `repair-round.yaml:5-9` states the same thing in the fragment's
own header, and that file *is* in this tree. The mechanism admits the three *smallest*
shapes and excludes the two the corpus actually reaches for. If reuse is going
to be worth anything, that is where it would have to bite, and here it does
not.

But the candidate is **not decisive on its own**, because a zero has two causes
this measurement cannot separate: (i) nothing in the catalog fits the goals
that were run, and (ii) the catalog block is at the wrong place in a 110-line
prompt and the planner never weighs it. Separating them needs one more bit, so
the falsifier is pre-registered as a **pair**:

- **0/20 cited, and ≥5/20 goals judged by a human as matching an offered
  shape** → the design is **wrong**. The mechanism was present, applicable and
  unused, which points at (ii): the prompt, the catalog's position, or the
  planner's incentive.
- **0/20 cited, and 0/20 judged matchable** → the design is **inapplicable, not
  wrong**, and the candidate's reading holds. The corrective is not in this
  record; it is §2.4's deferred second ADR, whose whole point is making the
  library contain shapes worth citing. This outcome should reorder the queue,
  not revert the code.

**Two secondary falsifiers**, both cheap:

- **§C.3's coverage claim is corpus-contingent.** It holds because all three
  admitted fragments are single-node. The first admitted **multi-node**
  fragment re-opens the question, and its first double citation is the test:
  two citations must produce `<a>/<internal>` and `<b>/<internal>` and pass
  `Graph.Validate`, or ADR 0027's coverage does not carry over as claimed.
- **§3's inertness test is a claim about the *shape* of fragments, not just
  today's six.** If the inertness test admits a fragment that turns out to
  reach a non-prompt field — through a nested `use:` (ADR 0029) whose inner
  fragment binds a token forwarded from the outer one, the pattern at
  `gated-lane.yaml:102-103` — then the test is unsound and the admission rule,
  not the catalog, is what needs rewriting. Inertness must be computed
  **transitively through the citation chain**, and that requirement is stated
  here so its absence in an implementation is a review-blocking defect rather
  than a discovery.

---

## 7. Out of scope

This record does not touch and does not decide:

- **The four exec seams** (`runner.CLIRunner`, `verify.ShellVerifier`,
  `worktree.GitManager`, `browser.ExecOpener`). No spawner is added, moved or
  changed; a fragment splice happens in-process and spawns nothing. No fifth
  seam, and so no ADR is owed on that count.
- **`internal/childenv.Scrub`.** Untouched. One list, no runtime branch.
- **The graph schema's existing keys.** `use:`, `with:`, `prompt:`,
  `allowed_tools:`, `success_check:`, `agent:`, `cwd:`, `worktree:` keep their
  present meanings and their present planned-node dispositions. `reuse:` and
  `bind:` are new keys valid only in a planner reply and never present in a
  persisted graph (§C.2's ordering) — precisely so that no existing key's
  refusal has to be relaxed.
- **ADR 0030's build-evidence gate.** A `reuse:` node is subject to it exactly
  as any planned node is.
- **ADR 0034's permission default.** Unchanged.
- **The auto ceiling's other layers.** ADR 0004's layers 0, 2, 3 and 4 are
  untouched, and layer 1 stays `""` for every planned node, `reuse:` or not —
  a spliced fragment supplies prompt text and a declared `allowed_tools`, not a
  settings scope.

---

## 8. References

- **ADR 0017** — `docs/adr/0017-planned-nodes-get-skill-activation-not-inlined-skill-text.md`.
  The staging shape, and the property this record must not break:
  `0017:415-418` (a planner that names a local resource is the hole
  `validatePlannedNodeAgent` closes), `0017:420-425` (choosing stays in trusted
  code; what the planner may declare does not move), `0017:463-476` (why no
  plan-time selector — the argument §3 departs from, with reasons),
  `0017:481-494` (the +6,008-token measurement §4 has no counterpart for).
- **ADR 0022** — `docs/adr/0022-a-mapped-node-gets-its-agent-staged-not-its-settings-back.md`.
  `0022:126-128` (plan-time manifest: source path and SHA-256 — the model for
  §C.2), `0022:133-136` (re-materialise before every spawn — and why a
  load-time splice needs only one check), `0022:142-149` (the scan narrowed out
  of repository scope — the decision §3 walks forward from, with the
  disanalogy stated), `0022:150-152` (the printout names path, size and hash —
  §2.5's disclosure).
- **ADR 0027** — `docs/adr/0027-the-reusable-unit-is-a-loop-not-a-node.md`.
  `0027:669-672` (two uses of one fragment cannot collide), assessed against
  §C.3.
- **ADR 0013** — `docs/adr/0013-a-fragment-is-a-load-time-node-splice-not-a-runtime-concept.md`.
  A fragment is a load-time splice; one location, no search path.
- **ADR 0029** — `docs/adr/0029-a-fragment-may-cite-a-fragment.md`. The nesting
  §6's second secondary falsifier is about.
- **The refusal site** — `internal/coordinator/coordinator.go:639`, with its
  reasoning at `:626-635`, its backstop at `internal/graph/validate.go:621`,
  and the type that connects them at `internal/graph/validate.go:30-38`. Kept.
- **CLAUDE.md's planner invariant** — `CLAUDE.md:108`: *"agent mapping —
  scanned from `~/.claude/agents` only, with the matched definition staged so a
  mapped node keeps ceiling layer 1 (ADR 0022) — and skill activation (ADR
  0017…)"*. Trusted code resolves local files; the planner never names them.
- **The corpus** — `graphs/fragments/*.yaml` (six files) and `graphs/*.yaml`
  (nine graphs), read in full; counts cross-checked against
  `./bin/oh-my-graph lint graphs/*.yaml`.
