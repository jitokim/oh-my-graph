// oh-my-graph serve — the dashboard. Hand-written, no build step.
//
// One page for every run. Two read sources, no writes (a gate is decided on
// the run's own view, which is one click away):
//   /api/cards         every run's card as JSON — the no-subscription read,
//                      not used by this page but the same shape it renders
//   /api/cards/events  card updates over SSE: one `card` frame per run that
//                      is new or has changed since the last sweep, one
//                      `card_removed` frame per run directory that is gone,
//                      and one `cards_ready` after the first sweep
//
// The page is a pure function of the cards it has been sent: every frame is
// keyed by run id and replaces, so a reconnect rebuilds the whole dashboard
// deterministically — the same replay property the single-run feed has. The
// one thing a replay cannot say is what went AWAY while the page was
// disconnected (the server's seen-set is per connection, so it has no
// card_removed to send), which is why `cards_ready` also drops every card the
// replay it closes did not mention.
//
// The server sends a card only when its run changed, so an idle dashboard with
// forty settled runs is silent.
//
// The one thing that ticks without a frame is elapsed: a card carries its
// leg boundaries as timestamps, so a running card's clock advances locally
// once a second rather than costing a poll.

"use strict";

// run id -> the card object last received for it.
const cards = new Map();

const $ = (id) => document.getElementById(id);

// --- theme -------------------------------------------------------------------

// Same key and same rule as the run view (app.js): an explicit toggle choice
// wins over the OS preference, both ways. The tokens themselves live in
// style.css, which both pages load — this is only the switch.
const THEME_KEY = "omg-theme";
const stored = localStorage.getItem(THEME_KEY);
if (stored === "light" || stored === "dark") {
  document.documentElement.dataset.theme = stored;
}

$("theme-toggle").addEventListener("click", () => {
  const dark = document.documentElement.dataset.theme
    ? document.documentElement.dataset.theme === "dark"
    : window.matchMedia("(prefers-color-scheme: dark)").matches;
  const next = dark ? "light" : "dark";
  document.documentElement.dataset.theme = next;
  localStorage.setItem(THEME_KEY, next);
});

// --- the subscription --------------------------------------------------------

function connect() {
  const source = new EventSource("api/cards/events");
  // Run ids the CURRENT connection has replayed. The server keeps its
  // seen-set per connection, so a run whose directory disappeared while we
  // were disconnected gets no `card_removed` frame — the new connection's
  // replay simply never mentions it. That replay is therefore the only truth
  // about which runs still exist, and anything it does not mention is dropped
  // at cards_ready. Pruning THERE rather than on the error keeps a transient
  // reconnect from blanking a dashboard someone is reading.
  let replayed = new Set();

  source.onopen = () => {
    replayed = new Set();
  };

  source.addEventListener("card", (event) => {
    let card;
    try {
      card = JSON.parse(event.data);
    } catch {
      return; // a frame this page cannot read is skipped, never fatal
    }
    if (!card || !card.run_id) return;
    replayed.add(card.run_id);
    cards.set(card.run_id, card);
    render();
  });

  source.addEventListener("card_removed", (event) => {
    try {
      const runID = JSON.parse(event.data).run_id;
      replayed.delete(runID);
      cards.delete(runID);
    } catch {
      return;
    }
    render();
  });

  source.addEventListener("cards_ready", () => {
    // The sweep behind this frame listed every run there is, so a card still
    // held from an earlier connection and absent from it is a run directory
    // that is gone. Dropping it here is what keeps a reconnect from leaving a
    // tile whose link now 404s.
    for (const runID of cards.keys()) {
      if (!replayed.has(runID)) cards.delete(runID);
    }
    setStatus("live", true);
    render();
  });

  source.addEventListener("stream_error", (event) => {
    setBanner(readError(event.data));
  });

  source.onerror = () => {
    // Connection dropped (server stopped, laptop slept). EventSource retries
    // on its own, and the replay on reconnect rebuilds every card and prunes
    // the ones it does not mention (cards_ready above), so the last known
    // dashboard is left on screen — stale for a moment beats blank.
    setStatus("reconnecting", false);
  };
}

function readError(data) {
  try {
    return JSON.parse(data).error || "";
  } catch {
    return "";
  }
}

function setStatus(text, live) {
  const el = $("dash-status");
  el.textContent = text;
  el.classList.toggle("live", !!live);
}

function setBanner(text) {
  $("banner").textContent = text || "";
}

// --- rendering ---------------------------------------------------------------

// In flight first, everything else below: the dashboard's whole premise is
// that the runs happening now are the working surface. A PLANNING run is one of
// them — it is the longest single wait in the tool, and the one #163 reported
// as invisible — so it groups with the running ones. An abandoned run is not:
// its leg is open only because the process that opened it died, so it groups
// with the settled runs, carrying its hint. Within each group, newest first:
// run ids are timestamps that sort lexically.
//
// This IS a hand-copy of runstatus.Status.InFlight() — the very predicate the Go
// side was rewritten to use so that a new in-flight value could not be dropped
// by an equality test. A page with no build step cannot import it, so the copy
// is held to the original by a Go test instead
// (TestLiveStates_AgreeWithTheInFlightPredicate reads this literal out of the
// embedded asset); do not edit it without editing that predicate.
const LIVE_STATES = new Set(["running", "planning"]);

// Every state a card can carry, in the order the header chips show them. It is
// the whole set on purpose — a state missing here has no chip, so a run in it
// would be invisible in the header's tally while sitting in plain sight below.
// TestCardStateTokens_AreDefinedByEveryAsset holds it to the Go side's set.
const COUNT_ORDER = ["planning", "running", "paused", "abandoned", "passed", "failed", "pending", "unknown"];

// Why a run reads as `unknown`, ONE sentence per reason code, for the group
// note above the unreadable cards. The codes are runstatus.SkipReason's, sent
// on the card as `error_code`; the card's own `error` field stays the per-run
// sentence and is still painted on each tile.
//
// The prose is here and not in the payload on purpose: /api/cards is a
// machine-readable surface, so it carries the code and this page turns the code
// into a sentence — the same split the CLI makes, where runstatus counts the
// reasons and `runs list` prints them. A code this page does not know still
// groups; it simply gets no note, and its cards still carry their own errors.
const UNREADABLE_WHY = {
  incompatible_snapshot:
    "written by a snapshot schema this build does not read — not damaged, but this build can neither open nor resume them",
  unreadable: "could not be read; each card below carries its own reason",
};

function render() {
  const all = [...cards.values()].sort((a, b) => (a.run_id < b.run_id ? 1 : -1));
  const live = all.filter((c) => LIVE_STATES.has(c.state));
  // A run this build cannot read is not settled history — it has no history to
  // show — so it gets its own section instead of padding the settled list. The
  // split is on `error_code`, which serve.brokenCard sets and nothing else does:
  // that is the refusal itself, and it is also the key the group counts on, so
  // this page does not hand-copy a state token to find them.
  const unreadable = all.filter((c) => c.error_code);
  const settled = all.filter((c) => !LIVE_STATES.has(c.state) && !c.error_code);

  paintGroup($("live-cards"), live);
  paintGroup($("settled-cards"), settled);
  paintUnreadable(unreadable);
  $("live-count").textContent = String(live.length);
  $("settled-count").textContent = String(settled.length);
  $("live-empty").hidden = live.length > 0;

  const cost = all.reduce((sum, c) => sum + (c.cost_usd || 0), 0);
  const unknown = all.some((c) => c.cost_unknown);
  $("dash-cost").textContent = unknown
    ? (cost > 0 ? `unknown · known $${cost.toFixed(4)}` : "unknown")
    : `$${cost.toFixed(4)}`;
  paintCounts(all);
}

// The header's own chips: how many runs are in each state, in the same dot
// anatomy the run view's header uses.
function paintCounts(all) {
  const by = new Map();
  for (const card of all) by.set(card.state, (by.get(card.state) || 0) + 1);
  const host = $("dash-counts");
  host.replaceChildren();
  for (const state of COUNT_ORDER) {
    const n = by.get(state) || 0;
    if (!n) continue;
    const chip = el("span", "chip");
    chip.append(el("i", `dot ${state}`), text(`${n} ${state}`));
    host.append(chip);
  }
}

// Cards are rebuilt rather than diffed: a card is small, there are tens of
// them at most, and a rebuild cannot drift from the state it is given.
function paintGroup(host, group) {
  host.replaceChildren(...group.map(cardEl));
}

// The unreadable section: its cards, and above them one line per reason code
// carrying that reason's COUNT. That count is the whole point — a section that
// said only "not readable by this build" would hide the difference between one
// stray directory and four fifths of the corpus — so it is stated per reason
// rather than as one anonymous total, and the section disappears entirely when
// there is nothing to report.
function paintUnreadable(group) {
  $("unreadable-section").hidden = group.length === 0;
  $("unreadable-count").textContent = String(group.length);

  const by = new Map();
  for (const card of group) {
    by.set(card.error_code, (by.get(card.error_code) || 0) + 1);
  }
  const note = $("unreadable-why");
  note.replaceChildren();
  for (const [code, n] of by) {
    const why = UNREADABLE_WHY[code];
    note.append(withText(el("p", "section-why"), why ? `${n} ${why}` : `${n} ${code}`));
  }

  paintGroup($("unreadable-cards"), group);
}

function cardEl(card) {
  // The whole tile is the link to the run's own view. A relative href, so it
  // resolves under whatever path this page is served at.
  const a = el("a", `card ${card.state}`);
  a.href = `run/${encodeURIComponent(card.run_id)}/`;

  const top = el("div", "card-top");
  // The graph name is unknown until the run's first node completes (no
  // snapshot yet); the run id is always known, so it stands in.
  top.append(withText(el("span", "card-name"), card.name || card.run_id));
  // The state word is whatever the server put in `state`: the card's own
  // vocabulary for ADR 0023's enumeration, chosen by serve.runState (mostly the
  // status lower-cased, but PASS/FAIL are `passed`/`failed` and there are two
  // tokens no status maps to). This page renders it as given and translates
  // nothing.
  top.append(withText(el("span", "card-state"), card.state));
  a.append(top);

  a.append(withText(el("div", "card-run"), card.run_id));
  if (card.goal) {
    a.append(withText(el("div", "card-goal"),
      `goal · cycle ${card.goal.cycle}/${card.goal.max_cycles} · ${card.goal.text}`));
  }

  const meta = el("div", "card-meta");
  meta.append(withText(el("span", "elapsed"), elapsedText(card)));
  const cardCost = card.cost_unknown
    ? ((card.cost_usd || 0) > 0 ? `unknown · known $${card.cost_usd.toFixed(4)}` : "unknown")
    : `$${(card.cost_usd || 0).toFixed(4)}`;
  meta.append(withText(el("span", "cost"), cardCost));
  if (card.usage) {
    meta.append(withText(el("span", "tokens"),
      `${card.usage.input_tokens || 0}/${card.usage.cached_input_tokens || 0}/${card.usage.output_tokens || 0}/${card.usage.reasoning_output_tokens || 0} tok`));
  }
  for (const [state, n] of countChips(card)) {
    const chip = el("span", "chip");
    chip.append(el("i", `dot ${state}`), text(String(n)));
    chip.title = `${n} ${state}`;
    meta.append(chip);
  }
  a.append(meta);

  // An abandoned run's recovery hint, above the mini-DAG rather than below it:
  // this tile leads to a page with a gate button, and that button starts a leg
  // that spends money, so the hint has to be read before the click, not after.
  if (card.hint) {
    a.append(withText(el("div", "card-hint"), card.hint));
  }

  if (card.error) {
    a.append(withText(el("div", "card-error"), card.error));
    return a;
  }
  a.append(card.nodes && card.nodes.length ? miniDag(card.nodes) : withText(el("div", "mini-none"), "no nodes yet"));
  return a;
}

// Only the states a run actually has get a chip — an all-passed card should
// not carry four zeroes.
function countChips(card) {
  const counts = card.counts || {};
  return [
    ["running", counts.running],
    ["passed", counts.passed],
    ["failed", counts.failed],
    ["pending", counts.pending],
  ].filter(([, n]) => n > 0);
}

// --- elapsed -----------------------------------------------------------------

// A settled run's elapsed is fixed (started → finished); a running one counts
// from its start to now, re-rendered by the ticker below. A run that has not
// emitted run_started yet has no elapsed to show.
function elapsedText(card) {
  const start = Date.parse(card.started_at || "");
  if (Number.isNaN(start)) return "—";
  const end = LIVE_STATES.has(card.state) ? Date.now() : Date.parse(card.ended_at || "");
  if (Number.isNaN(end)) return "—";
  return formatDuration(Math.max(0, end - start));
}

function formatDuration(ms) {
  const total = Math.floor(ms / 1000);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const pad = (n) => String(n).padStart(2, "0");
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`;
}

// One second is the finest granularity the elapsed text shows, so that is how
// often a live card's clock needs to move. Nothing else re-renders here — the
// cards themselves change only when the server says they did.
setInterval(() => {
  if ([...cards.values()].some((c) => LIVE_STATES.has(c.state))) render();
}, 1000);

// --- the mini-DAG ------------------------------------------------------------

// Layered left to right by depth (longest path from a root), one dot per node
// and one line per depends_on edge. Deliberately not cytoscape: a dashboard
// can hold dozens of cards, and this is orientation at a glance. Clicking the
// card opens the real map.
const MINI = { width: 100, height: 54, pad: 7, r: 3.4 };

function miniDag(nodes) {
  const depth = depths(nodes);
  const layers = new Map();
  for (const node of nodes) {
    const d = depth.get(node.id) || 0;
    if (!layers.has(d)) layers.set(d, []);
    layers.get(d).push(node);
  }
  const maxDepth = Math.max(...layers.keys());
  const at = new Map();
  for (const [d, layer] of layers) {
    layer.forEach((node, i) => {
      at.set(node.id, {
        x: spread(d, maxDepth, MINI.width),
        y: spread(i, layer.length - 1, MINI.height),
      });
    });
  }

  const svg = svgEl("svg", { viewBox: `0 0 ${MINI.width} ${MINI.height}`, preserveAspectRatio: "none" });
  svg.setAttribute("class", "mini");
  svg.setAttribute("aria-hidden", "true"); // the counts chips carry the same facts as text
  for (const node of nodes) {
    for (const dep of node.depends_on || []) {
      const from = at.get(dep);
      const to = at.get(node.id);
      if (!from || !to) continue;
      svg.append(svgEl("line", {
        class: "mini-edge", x1: from.x, y1: from.y, x2: to.x, y2: to.y,
      }));
    }
  }
  for (const node of nodes) {
    const p = at.get(node.id);
    svg.append(svgEl("circle", {
      class: `mini-node ${node.state}`, cx: p.x, cy: p.y, r: MINI.r,
    }));
  }
  return svg;
}

// spread places index i of n+1 evenly inside the padded extent, and centres a
// lone item rather than pinning it to the left edge.
function spread(i, n, extent) {
  const lo = MINI.pad;
  const hi = extent - MINI.pad;
  if (n <= 0) return (lo + hi) / 2;
  return lo + ((hi - lo) * i) / n;
}

// Longest path from a root, computed by relaxing until it settles. Bounded by
// the node count because the graph is a DAG (the loader validates that before
// a run ever exists), so this always terminates.
function depths(nodes) {
  const depth = new Map(nodes.map((n) => [n.id, 0]));
  for (let pass = 0; pass < nodes.length; pass++) {
    let moved = false;
    for (const node of nodes) {
      for (const dep of node.depends_on || []) {
        if (!depth.has(dep)) continue;
        const want = depth.get(dep) + 1;
        if (want > depth.get(node.id)) {
          depth.set(node.id, want);
          moved = true;
        }
      }
    }
    if (!moved) break;
  }
  return depth;
}

// --- DOM helpers -------------------------------------------------------------

// Every card's text goes through textContent: run ids, graph names, goal text
// and error strings are all untrusted input read off disk.
function el(tag, className) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  return node;
}

function withText(node, value) {
  node.textContent = value;
  return node;
}

function text(value) {
  return document.createTextNode(value);
}

function svgEl(tag, attrs) {
  const node = document.createElementNS("http://www.w3.org/2000/svg", tag);
  for (const [name, value] of Object.entries(attrs)) node.setAttribute(name, value);
  return node;
}

connect();
