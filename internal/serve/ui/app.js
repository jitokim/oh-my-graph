// oh-my-graph serve — single-run live view. Hand-written, no build step.
//
// Feed-first: the chronological run feed is the main surface — what each
// node produced, why something failed — and the DAG is a compact,
// collapsible side map (clicking a map node scrolls the feed to that node's
// latest entry). A settled node reads as ONE entry: its terminal entry
// absorbs its started-line (retry lines stay — a retry is a real
// transition). Four read sources, mirroring the run-feed contract
// (docs/RUN-FEED.md), and — on a paused gate's entry only — one write:
//   /api/graph   the DAG structure (polled until the snapshot exists — a
//                fresh run has no state.json until its first node completes)
//   /api/events  the event stream over SSE (replay, then follow)
//   /api/result  one node's handoff artifact, fetched lazily for that
//                node's settled feed entries (200 body / 204 none / 404
//                unknown node); fetched once per settled node and shared
//                across every entry that shows it
//   /api/transcript  a running node's live "now doing" tail (assistant text
//                and tool-use names from its session transcript), polled
//                every few seconds onto the node's open feed line and gone
//                the moment the node settles (200 entries / 204 nothing)
//   /api/gate/approve, /api/gate/reject  POSTed by the approve/reject buttons
//                on the entry of the gate the run is paused at, carrying the
//                per-process token this page was served with (see decideGate;
//                202 leg started / 4xx-5xx refused, with the reason shown)
// Events may arrive before the structure does; per-node state is kept in
// `nodes` and painted onto the cytoscape map whenever either side updates.
// The feed is derived purely from the stream, so a full EventSource replay
// rebuilds it deterministically: on reconnect the feed state resets and the
// replay rebuilds it exactly, the same way totalCost is recomputed.
//
// Theme: CSS custom properties in style.css are the token source of truth;
// cytoscape styles cannot read CSS vars, so the resolved values are read via
// getComputedStyle at init and the graph is re-styled on theme change.

"use strict";

// This page's gate token, rendered into it by the process that served it
// (serve.handleIndex). It is sent back on every gate decision and on nothing
// else; a page served by some other origin has no way to know it, which is
// what keeps a cross-origin POST from deciding this run's gate.
const GATE_TOKEN = document.querySelector('meta[name="omg-token"]')?.content || "";

// Status palette — style.css's custom properties (--pending, --running, …)
// are the one source of truth; node borders read them via statusColor(), so
// no status hex lives in this file. Same hexes in both themes; status is
// never color alone — every status surface pairs the color with a text
// label, and "running" gets motion as its primary signal.
const STATES = ["pending", "running", "passed", "failed", "gate-paused"];

function statusColor(state) {
  return cssVar(`--${state}`);
}

// node id -> the per-node record nodeInfo() seeds (see its literal for the
// full shape and the result-fetch states). `verdict` is retained from the
// stream for completeness but currently displayed nowhere — the feed entry's
// status word and the card border already carry the same fact.
const nodes = new Map();
let cy = null;
let totalCost = 0;
let runStartedMs = null;
let runEndedMs = null;

const $ = (id) => document.getElementById(id);

function nodeInfo(id) {
  if (!nodes.has(id)) {
    nodes.set(id, {
      state: "pending", verdict: "", sessionId: "", costUsd: 0, detail: "",
      startedMs: null, endedMs: null,
      // The node's handoff artifact, fetched lazily once the node settles:
      // resultState is "none" | "loading" | "loaded" | "empty" | "error".
      // resultGen guards a stale in-flight fetch against a restart that
      // invalidated it mid-air.
      result: "", resultState: "none", resultGen: 0,
    });
  }
  return nodes.get(id);
}

// --- theme -------------------------------------------------------------------

const THEME_KEY = "omg-theme";
const osDark = window.matchMedia("(prefers-color-scheme: dark)");

// The explicit toggle choice (persisted as data-theme on <html>) wins over
// the OS setting both ways; with no stored choice the media query decides.
const stored = localStorage.getItem(THEME_KEY);
if (stored === "light" || stored === "dark") {
  document.documentElement.dataset.theme = stored;
}

function effectiveTheme() {
  return document.documentElement.dataset.theme || (osDark.matches ? "dark" : "light");
}

$("theme-toggle").addEventListener("click", () => {
  const next = effectiveTheme() === "dark" ? "light" : "dark";
  document.documentElement.dataset.theme = next;
  localStorage.setItem(THEME_KEY, next);
  if (cy) cy.style(buildCyStyle());
});

osDark.addEventListener("change", () => {
  // Only an OS-driven change with no explicit choice re-themes the page.
  if (!document.documentElement.dataset.theme && cy) cy.style(buildCyStyle());
});

function cssVar(name) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

// --- map panel (collapsible) -------------------------------------------------

// The open/closed choice persists next to the theme key; with no stored
// choice, narrow windows start collapsed — on a phone-width window the feed
// is the page.
const MAP_KEY = "omg-map";

function mapOpen() {
  return !$("map").classList.contains("collapsed");
}

function setMapOpen(open) {
  $("map").classList.toggle("collapsed", !open);
  // No split to drag while the map is collapsed.
  $("splitter").hidden = !open;
  const toggle = $("map-toggle");
  toggle.setAttribute("aria-expanded", String(open));
  toggle.textContent = open ? "▸" : "◂";
  if (open && cy) {
    // The canvas was 0×0 while hidden; re-measure, then fit.
    cy.resize();
    cy.fit(24);
  }
}

const storedMap = localStorage.getItem(MAP_KEY);
setMapOpen(storedMap === null ? window.innerWidth >= 900 : storedMap === "open");

$("map-toggle").addEventListener("click", () => {
  const next = !mapOpen();
  setMapOpen(next);
  localStorage.setItem(MAP_KEY, next ? "open" : "closed");
});

// --- feed/map split (drag to resize) -----------------------------------------

// The dragged map width persists next to the theme and map keys, validated on
// read the same way; with no stored (or invalid) choice the CSS default — a
// viewport-proportional clamp — decides, so the map grows with the window.
// The width is applied as the --map-w custom property, not an inline width,
// so the collapsed state's `width: auto` still wins while collapsed.
const MAP_WIDTH_KEY = "omg-map-width";
const MAP_MIN = 240;
const FEED_MIN = 280;
const splitter = $("splitter");

// The user's chosen width in px, or null to leave the CSS default in charge.
let chosenMapWidth = null;

function clampMapWidth(w) {
  const max = Math.max(MAP_MIN, window.innerWidth - FEED_MIN - splitter.offsetWidth);
  return Math.round(Math.min(Math.max(w, MAP_MIN), max));
}

function applyMapWidth(w) {
  $("map").style.setProperty("--map-w", `${w}px`);
}

const storedWidth = Number(localStorage.getItem(MAP_WIDTH_KEY));
if (Number.isFinite(storedWidth) && storedWidth >= MAP_MIN) {
  chosenMapWidth = storedWidth;
  applyMapWidth(clampMapWidth(chosenMapWidth));
}

// Pointer events (not mouse events) so capture keeps the drag alive when the
// pointer leaves the 7px handle — one code path for mouse and trackpad. The
// map width is applied live on every move, but the cytoscape canvas is only
// re-measured and re-fit on drag END: cy.resize() re-reads the container and
// redraws, far too heavy per pointermove.
let dragPointerId = null;
let dragStartX = 0;
let dragStartWidth = 0;

splitter.addEventListener("pointerdown", (e) => {
  dragPointerId = e.pointerId;
  dragStartX = e.clientX;
  dragStartWidth = $("map").getBoundingClientRect().width;
  splitter.setPointerCapture(e.pointerId);
  splitter.classList.add("dragging");
  e.preventDefault();
});

splitter.addEventListener("pointermove", (e) => {
  if (dragPointerId !== e.pointerId) return;
  applyMapWidth(clampMapWidth(dragStartWidth + (dragStartX - e.clientX)));
});

function endDrag(e) {
  if (dragPointerId !== e.pointerId) return;
  dragPointerId = null;
  splitter.classList.remove("dragging");
  chosenMapWidth = clampMapWidth($("map").getBoundingClientRect().width);
  applyMapWidth(chosenMapWidth);
  localStorage.setItem(MAP_WIDTH_KEY, String(chosenMapWidth));
  if (cy) {
    cy.resize();
    cy.fit(24);
  }
}
splitter.addEventListener("pointerup", endDrag);
splitter.addEventListener("pointercancel", endDrag);

// A maximized/resized window must re-fit the graph (the cytoscape canvas
// keeps its old pixel size until cy.resize()) and re-clamp a chosen width so
// the feed keeps its minimum. Debounced: resize fires per frame while the
// user drags the window edge.
let windowResizeTimer = null;
window.addEventListener("resize", () => {
  clearTimeout(windowResizeTimer);
  windowResizeTimer = setTimeout(() => {
    if (chosenMapWidth != null) applyMapWidth(clampMapWidth(chosenMapWidth));
    if (cy && mapOpen()) {
      cy.resize();
      cy.fit(24);
    }
  }, 150);
});

// --- cytoscape style ---------------------------------------------------------

function buildCyStyle() {
  return [
    {
      selector: "node",
      style: {
        shape: "round-rectangle",
        "corner-radius": 8,
        width: 170,
        height: 56,
        // Fill stays the neutral card surface; the thick border carries
        // status (thin status borders are a documented Airflow UX failure).
        "background-color": cssVar("--surface"),
        "border-width": 4,
        "border-color": statusColor("pending"),
        label: "data(label)",
        "text-wrap": "wrap",
        "text-valign": "center",
        "text-halign": "center",
        // DEVIATION from the spec's 13px/600 id line over a muted 10px state
        // line: a cytoscape node has exactly one label with one text style,
        // so both lines share one size/weight/color. The id still leads and
        // the state word stays short.
        "font-size": 12,
        "font-weight": 600,
        "line-height": 1.4,
        color: cssVar("--ink"),
      },
    },
    // Gates keep a distinct shape with the same border-as-status treatment.
    { selector: "node[?gate]", style: { shape: "round-hexagon", width: 150, height: 64 } },
    ...STATES.map((s) => ({
      selector: `node[state = "${s}"]`,
      style: { "border-color": statusColor(s) },
    })),
    {
      selector: "edge",
      style: {
        width: 2,
        "line-color": cssVar("--muted"),
        "target-arrow-color": cssVar("--muted"),
        "target-arrow-shape": "triangle",
        "curve-style": "bezier",
      },
    },
    {
      // Accent outline IN ADDITION to the status border — never shadow alone.
      selector: "node:selected",
      style: {
        "outline-width": 2,
        "outline-color": cssVar("--accent"),
        "outline-offset": 2,
      },
    },
  ];
}

function layoutOptions() {
  // Left-to-right layered DAG via the vendored dagre extension; silent
  // breadthfirst fallback if it failed to register.
  let dagre = null;
  try { dagre = cytoscape("layout", "dagre"); } catch { dagre = null; }
  return dagre
    ? { name: "dagre", rankDir: "LR", nodeSep: 24, rankSep: 64 }
    : { name: "breadthfirst", directed: true, padding: 24, spacingFactor: 1.15 };
}

// --- graph structure ---------------------------------------------------------

async function loadGraph() {
  let payload;
  try {
    const resp = await fetch("api/graph");
    if (!resp.ok) {
      // Transient or not, keep trying: the SSE side self-heals (EventSource
      // reconnects), and a page that gives up on structure after one bad
      // response would stream events into an invisible graph forever.
      banner(`graph: ${(await resp.text()).trim()}`);
      setTimeout(loadGraph, 2000);
      return;
    }
    payload = await resp.json();
  } catch (err) {
    banner(`graph: ${err}`);
    setTimeout(loadGraph, 2000);
    return;
  }
  $("run-id").textContent = payload.run_id;
  renderGoal(payload.goal);
  if (!payload.available) {
    // Honest window: structure is unknown until the first node's terminal
    // verdict writes state.json. Keep polling; events still stream meanwhile.
    setStatus("waiting for structure", false);
    setTimeout(loadGraph, 2000);
    return;
  }
  render(payload);
}

// renderGoal shows the header's goal-lineage chip when this run is one cycle
// of an iterated auto goal (ADR 0011: serve stays a per-run view and shows
// the goal block in its header). The goal text is untrusted input, so it is
// set via textContent only; the title carries the full text for hover when
// the chip's CSS ellipsis truncates it.
function renderGoal(goal) {
  if (!goal) return;
  const el = $("goal-lineage");
  el.textContent = `goal “${goal.text}” · cycle ${goal.cycle}/${goal.max_cycles}`;
  el.title = goal.text;
  el.hidden = false;
}

function render(payload) {
  const elements = [];
  for (const node of payload.nodes || []) {
    elements.push({
      data: { id: node.id, label: `${node.id}\npending`, state: "pending", gate: node.type === "gate" },
    });
    for (const parent of node.depends_on || []) {
      elements.push({ data: { id: `${parent}->${node.id}`, source: parent, target: node.id } });
    }
  }
  cy = cytoscape({
    container: $("cy"),
    elements,
    autoungrabify: true,
    style: buildCyStyle(),
    layout: layoutOptions(),
  });
  // The map is orientation; the story lives in the feed. Tapping a node
  // jumps the feed to that node's latest entry and flash-highlights it.
  cy.on("tap", "node", (evt) => {
    const entry = latestEntryByNode.get(evt.target.id());
    if (!entry) return;
    entry.scrollIntoView({ block: "center" });
    entry.classList.remove("flash");
    void entry.offsetWidth; // restart the animation on repeat taps
    entry.classList.add("flash");
  });
  if (mapOpen()) cy.fit(24);
  paint();
}

// --- canvas controls ---------------------------------------------------------

function zoomBy(factor) {
  if (!cy) return;
  cy.zoom({ level: cy.zoom() * factor, renderedPosition: { x: cy.width() / 2, y: cy.height() / 2 } });
}
$("zoom-in").addEventListener("click", () => zoomBy(1.25));
$("zoom-out").addEventListener("click", () => zoomBy(0.8));
$("zoom-fit").addEventListener("click", () => { if (cy) cy.fit(24); });

// --- event stream ------------------------------------------------------------

function connect() {
  const source = new EventSource("api/events");
  source.onopen = () => banner("");
  source.onmessage = (msg) => apply(JSON.parse(msg.data));
  // The server's non-terminal caution (e.g. a stream schema newer than the
  // binary): show it and keep consuming — unknown event types fall through
  // apply()'s default branch, so rendering degrades generically, per the
  // run-feed compatibility rule.
  source.addEventListener("stream_warning", (msg) => {
    banner(JSON.parse(msg.data).error);
  });
  // The server's terminal refusal, distinct from EventSource's own
  // connection errors.
  source.addEventListener("stream_error", (msg) => {
    source.close();
    banner(JSON.parse(msg.data).error);
  });
  source.onerror = () => {
    // Connection dropped (server stopped, laptop slept): EventSource retries
    // by itself, and the server replays the stream from zero. Everything
    // derived from the stream — the feed and the cost total — resets here so
    // the replay rebuilds it without duplicates (idempotence).
    setStatus("reconnecting", false);
    totalCost = 0;
    resetFeed();
  };
}

function apply(event) {
  const ts = event.ts ? Date.parse(event.ts) : NaN;
  switch (event.event) {
    case "run_started":
      runStartedMs = Number.isNaN(ts) ? Date.now() : ts;
      runEndedMs = null;
      setStatus("running", true);
      break;
    case "node_started":
    case "node_retried": {
      const info = nodeInfo(event.node_id);
      info.state = "running";
      // A (re)started node's previous result is stale by definition; reset
      // here, on the state change itself, so the next settled entry
      // refetches. The generation bump makes any fetch still in flight for
      // the old artifact land as a no-op.
      info.result = "";
      info.resultState = "none";
      info.resultGen++;
      if (!Number.isNaN(ts)) {
        info.startedMs = ts;
        info.endedMs = null;
      }
      break;
    }
    case "node_passed":
    case "node_failed": {
      const info = nodeInfo(event.node_id);
      info.state = event.event === "node_passed" ? "passed" : "failed";
      info.verdict = event.verdict || "";
      info.sessionId = event.session_id || "";
      info.costUsd = event.cost_usd || 0;
      info.detail = event.detail || "";
      if (!Number.isNaN(ts)) info.endedMs = ts;
      totalCost += event.cost_usd || 0;
      $("run-cost").textContent = `$${totalCost.toFixed(4)}`;
      break;
    }
    case "gate_paused":
      nodeInfo(event.node_id).state = "gate-paused";
      break;
    case "gate_approved":
      // A resolved gate stops rendering as paused right away, instead of
      // waiting for a later node event to repaint the graph.
      nodeInfo(event.node_id).state = "passed";
      break;
    case "gate_rejected":
      nodeInfo(event.node_id).state = "failed";
      break;
    case "run_finished":
      runEndedMs = Number.isNaN(ts) ? Date.now() : ts;
      setStatus(event.outcome, false);
      break;
    default:
      // Unknown event type (same-schema addition impossible, but be safe):
      // skip rather than fail, per the contract.
      break;
  }
  appendFeed(event, ts);
  paint();
  tick();
}

// --- feed --------------------------------------------------------------------

// The feed column pins to the bottom only while the reader is already there:
// the scroll listener keeps `feedAtBottom` current, and appending an entry
// scrolls only when it was true — a reader who scrolled up is never yanked.
const feedCol = $("feed-col");
let feedAtBottom = true;

feedCol.addEventListener("scroll", () => {
  feedAtBottom = feedCol.scrollTop + feedCol.clientHeight >= feedCol.scrollHeight - 4;
});

// Feed-derived indexes, all reset together on reconnect:
//   latestEntryByNode  node id -> its most recent entry (the map's tap target)
//   gateActions        gate node id -> the approve/reject pair on its paused
//                      entry, dropped when the stream reports the decision
//   startLine          node id -> its open started-line; the node's terminal
//                      entry absorbs it (one entry per settled node — a retry
//                      is a real transition and keeps its own line)
//   resultBlocks       node id -> the artifact renderings of its settled
//                      entries (block + inline head span)
//   liveElapsed        node id -> the ticking elapsed span of its latest
//                      running line (frozen and dropped from the map on settle)
//   liveTails          node id -> the live transcript tail <pre> of its
//                      latest running line (removed on settle, which also
//                      stops that node's polling)
const latestEntryByNode = new Map();
const startLine = new Map();
const resultBlocks = new Map();
const liveElapsed = new Map();
const liveTails = new Map();
const gateActions = new Map();

function resetFeed() {
  $("feed").textContent = "";
  latestEntryByNode.clear();
  startLine.clear();
  resultBlocks.clear();
  liveElapsed.clear();
  liveTails.clear();
  gateActions.clear();
  feedAtBottom = true;
}

function addEntry(kind, nodeId) {
  const li = document.createElement("li");
  li.className = `entry ${kind}`;
  if (nodeId) latestEntryByNode.set(nodeId, li);
  $("feed").appendChild(li);
  if (feedAtBottom) feedCol.scrollTop = feedCol.scrollHeight;
  return li;
}

function entryHead(li, dotState, title, word) {
  const head = document.createElement("div");
  head.className = "entry-head";
  const dot = document.createElement("i");
  dot.className = `dot ${dotState}`;
  head.appendChild(dot);
  if (title) {
    const t = document.createElement("span");
    t.className = "entry-title";
    t.textContent = title;
    head.appendChild(t);
  }
  const w = document.createElement("span");
  w.className = "entry-word";
  w.textContent = word;
  head.appendChild(w);
  li.appendChild(head);
  return head;
}

function fmtClock(ms) {
  const d = new Date(ms);
  const pad = (n) => String(n).padStart(2, "0");
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

// appendFeed turns one stream event into one feed entry. It runs after the
// state switch in apply(), so `nodes` already reflects the event.
function appendFeed(event, ts) {
  const clock = Number.isNaN(ts) ? "" : fmtClock(ts);
  switch (event.event) {
    case "run_started": {
      // A slim leg marker; on a resumed run the replay carries one per leg,
      // so a second marker IS the visible resume seam.
      const li = addEntry("marker");
      const label = document.createElement("span");
      label.textContent = clock ? `run started · ${clock}` : "run started";
      li.appendChild(label);
      break;
    }
    case "node_started": {
      // A node re-started without ever settling (an interrupted leg): its
      // superseded started-line is absorbed by the new one.
      const prev = startLine.get(event.node_id);
      if (prev) prev.remove();
      const li = addEntry("line", event.node_id);
      const head = entryHead(li, "running", event.node_id, "started");
      const elapsed = document.createElement("span");
      elapsed.className = "entry-elapsed";
      head.appendChild(elapsed);
      liveElapsed.set(event.node_id, elapsed);
      startLine.set(event.node_id, li);
      attachLiveTail(li, event.node_id);
      break;
    }
    case "node_retried": {
      const li = addEntry("line", event.node_id);
      const nth = event.retries ? `retry #${event.retries}` : "retried";
      const head = entryHead(li, "running", event.node_id, nth);
      if (event.detail) {
        const note = document.createElement("p");
        note.className = "entry-note";
        note.textContent = event.detail;
        li.appendChild(note);
      }
      const elapsed = document.createElement("span");
      elapsed.className = "entry-elapsed";
      head.appendChild(elapsed);
      liveElapsed.set(event.node_id, elapsed);
      attachLiveTail(li, event.node_id);
      break;
    }
    case "node_passed":
    case "node_failed": {
      const info = nodeInfo(event.node_id);
      const passed = event.event === "node_passed";
      const duration = nodeDuration(info);
      // Freeze the latest running-line's ticking elapsed at the exact final
      // span (it matters when that line is a retry line, which stays), and
      // stop ticking the node.
      const span = liveElapsed.get(event.node_id);
      if (span) span.textContent = duration;
      liveElapsed.delete(event.node_id);
      // The live tail is "now doing"; a settled node has no now. Removing it
      // here also matters for a retry line, which stays in the feed.
      removeLiveTail(event.node_id);
      // One entry per settled node: the terminal entry absorbs the node's
      // started-line — the start is implied by the duration — so the feed is
      // as long as the run, not twice as long. A retry line stays: a retry
      // is a real transition worth its own line.
      const started = startLine.get(event.node_id);
      if (started) started.remove();
      startLine.delete(event.node_id);
      const li = addEntry("rich", event.node_id);
      const word = passed ? "passed" : "failed";
      const head = entryHead(li, info.state, event.node_id, duration ? `${word} · ${duration}` : word);
      // A single-line artifact renders here, inline in the head, instead of
      // as a block — paintResultBlock() fills it once the fetch lands.
      const inline = document.createElement("span");
      inline.className = "artifact-inline";
      inline.hidden = true;
      head.appendChild(inline);
      // On failure the cause leads, emphasized — it is THE human information
      // then; the artifact follows as supporting evidence.
      if (!passed && info.detail) {
        const fail = document.createElement("p");
        fail.className = "entry-fail";
        fail.textContent = info.detail;
        li.appendChild(fail);
      }
      li.appendChild(buildArtifactBlock(event.node_id, inline));
      const meta = buildMetaLine(info);
      if (meta) li.appendChild(meta);
      break;
    }
    case "gate_paused": {
      const li = addEntry("line", event.node_id);
      entryHead(li, "gate-paused", event.node_id, "⏸ gate paused");
      // Decide it here — or from the terminal, which stays a first-class way
      // in (and the only one when this view is embedded in the running
      // process, whose POSTs are refused).
      buildGateActions(li, event.node_id);
      const note = document.createElement("p");
      note.className = "entry-note";
      const runRef = event.run_id || "<run-id>";
      note.textContent = `or from the terminal: oh-my-graph resume ${runRef} --approve ${event.node_id} (or --reject)`;
      li.appendChild(note);
      break;
    }
    case "gate_approved": {
      const li = addEntry("line", event.node_id);
      entryHead(li, "passed", event.node_id, "gate approved");
      dropGateActions(event.node_id);
      break;
    }
    case "gate_rejected": {
      const li = addEntry("line", event.node_id);
      entryHead(li, "failed", event.node_id, "gate rejected");
      dropGateActions(event.node_id);
      break;
    }
    case "run_finished": {
      // The run's ledger line: outcome word, total elapsed, total cost.
      const li = addEntry("ledger");
      const outcome = event.outcome || "finished";
      const dotState = outcome === "passed" || outcome === "failed" ? outcome : "pending";
      const elapsed = runStartedMs != null && runEndedMs != null
        ? ` · ${fmtDuration(runEndedMs - runStartedMs)}` : "";
      entryHead(li, dotState, "", `run ${outcome}${elapsed} · $${totalCost.toFixed(4)}`);
      break;
    }
    default:
      break;
  }
}

// --- gate decisions ----------------------------------------------------------

// The one place this page writes rather than reads: the approve/reject pair on
// a paused gate's feed entry, POSTing to /api/gate/* (see serve's
// handleGateDecision). The buttons live on that entry and nowhere else, and
// they are derived from the stream like everything else in the feed: a
// gate_paused entry gets them, and the gate_approved/gate_rejected that
// answers it takes them away — so a replay after a reconnect leaves them on
// exactly the gate that is still waiting.
function buildGateActions(li, nodeId) {
  const actions = document.createElement("div");
  actions.className = "gate-actions";
  const buttons = [];
  for (const decision of ["approve", "reject"]) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = `gate-btn ${decision}`;
    button.textContent = decision;
    button.addEventListener("click", () => decideGate(nodeId, decision, buttons));
    actions.appendChild(button);
    buttons.push(button);
  }
  li.appendChild(actions);
  gateActions.set(nodeId, actions);
}

function dropGateActions(nodeId) {
  const actions = gateActions.get(nodeId);
  if (actions) actions.remove();
  gateActions.delete(nodeId);
}

// decideGate sends one decision. The token comes from the meta tag the serving
// process rendered into this page (serve.handleIndex); without it the server
// refuses, which is what stops another origin from POSTing on the user's
// behalf. A 202 means the leg started: the buttons stay disabled and the
// stream's own gate_approved/gate_rejected renders the outcome, so nothing is
// drawn here from the response. Any other status leaves the gate decidable and
// says why in the banner.
function decideGate(nodeId, decision, buttons) {
  for (const button of buttons) button.disabled = true;
  fetch(`api/gate/${decision}`, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-OMG-Token": GATE_TOKEN },
    body: JSON.stringify({ node: nodeId }),
  })
    .then(async (resp) => {
      if (resp.status === 202) {
        banner(`gate ${nodeId}: ${decision} sent — resuming`);
        return;
      }
      banner(`gate ${decision}: ${(await resp.text()).trim()}`);
      for (const button of buttons) button.disabled = false;
    })
    .catch((err) => {
      banner(`gate ${decision}: ${err}`);
      for (const button of buttons) button.disabled = false;
    });
}

// The artifact rendering of a settled entry. A multi-line artifact is a
// monospace pre capped at 24 lines with a "show more" expander when the
// content overflows; a single-line artifact renders inline in the entry's
// head (`inline`) with no block at all; a node with no artifact renders
// neither. The artifact is rendered via textContent ONLY — never innerHTML:
// node output is untrusted text, not markup.
function buildArtifactBlock(id, inline) {
  const wrap = document.createElement("div");
  wrap.className = "artifact-wrap";
  const pre = document.createElement("pre");
  pre.className = "artifact";
  const more = document.createElement("button");
  more.type = "button";
  more.className = "show-more";
  more.textContent = "show more";
  more.hidden = true;
  more.addEventListener("click", () => {
    const expanded = pre.classList.toggle("expanded");
    more.textContent = expanded ? "show less" : "show more";
  });
  wrap.appendChild(pre);
  wrap.appendChild(more);
  const block = { wrap, pre, more, inline };
  if (!resultBlocks.has(id)) resultBlocks.set(id, []);
  resultBlocks.get(id).push(block);
  paintResultBlock(id, block);
  fetchResult(id);
  return wrap;
}

// One compact accounting line: labelled session ref (click to copy) and
// per-node cost — cross-tool reference and bookkeeping, not headlines. Its
// anatomy stays the same whichever parts exist (cost may be absent; the
// session ref keeps its label and copy affordance); with neither part there
// is no line at all.
function buildMetaLine(info) {
  if (!info.sessionId && !info.costUsd) return null;
  const meta = document.createElement("p");
  meta.className = "entry-meta";
  if (info.sessionId) {
    const label = document.createElement("span");
    label.textContent = "session";
    meta.appendChild(label);
    const full = info.sessionId;
    const session = document.createElement("button");
    session.type = "button";
    session.className = "session-ref";
    session.textContent = full.slice(0, 8);
    session.title = `${full} — click to copy`;
    session.addEventListener("click", () => {
      navigator.clipboard.writeText(full).then(() => {
        session.textContent = "copied";
        setTimeout(() => { session.textContent = full.slice(0, 8); }, 900);
      });
    });
    meta.appendChild(session);
  }
  if (info.costUsd) {
    const cost = document.createElement("span");
    cost.textContent = `$${info.costUsd.toFixed(4)}`;
    meta.appendChild(cost);
  }
  return meta;
}

// --- live transcript tail ----------------------------------------------------

// A running node's open feed line carries what its session is doing RIGHT
// NOW: the last few assistant texts and tool-use names from /api/transcript,
// polled every few seconds while the node runs. Rendered via textContent
// ONLY — transcript content is untrusted text, exactly like an artifact. A
// 204 (no session id published — a session-handoff node or a gate — or no
// transcript on disk yet) simply keeps the element hidden; polling stops the
// moment the node leaves running, because settle removes the element.
const TAIL_POLL_MS = 3000;

function attachLiveTail(li, nodeId) {
  removeLiveTail(nodeId); // a superseded running line's tail dies with it
  const pre = document.createElement("pre");
  pre.className = "live-tail";
  pre.hidden = true;
  li.appendChild(pre);
  liveTails.set(nodeId, pre);
  pollLiveTail(nodeId); // first paint now, not a full interval later
}

function removeLiveTail(nodeId) {
  const pre = liveTails.get(nodeId);
  if (pre) pre.remove();
  liveTails.delete(nodeId);
}

function pollLiveTail(nodeId) {
  const pre = liveTails.get(nodeId);
  const info = nodes.get(nodeId);
  if (!pre || !info || info.state !== "running") return;
  fetch(`api/transcript?node=${encodeURIComponent(nodeId)}`)
    .then(async (resp) => {
      if (resp.status !== 200) return;
      const payload = await resp.json();
      // Settled (or superseded) while the fetch was in flight: stale, drop.
      if (liveTails.get(nodeId) !== pre) return;
      const lines = (payload.entries || []).map((e) =>
        e.type === "tool_use" ? `⏺ ${e.name}` : e.text
      );
      pre.textContent = lines.join("\n");
      pre.hidden = lines.length === 0;
      if (feedAtBottom) feedCol.scrollTop = feedCol.scrollHeight;
    })
    .catch(() => {}); // transient fetch failure: the next poll retries
}

setInterval(() => {
  for (const nodeId of liveTails.keys()) pollLiveTail(nodeId);
}, TAIL_POLL_MS);

// fetchResult kicks off the lazy /api/result fetch the first time a node is
// seen settled; every entry showing that node shares the one fetch. Fetch
// INVALIDATION lives in apply()'s (re)start branch (result reset + gen
// bump), so a response for a superseded attempt lands as a no-op.
function fetchResult(id) {
  const info = nodeInfo(id);
  if (info.resultState !== "none") return;
  info.resultState = "loading";
  const gen = info.resultGen;
  fetch(`api/result?node=${encodeURIComponent(id)}`)
    .then(async (resp) => {
      if (gen !== info.resultGen) return;
      if (resp.status === 200) {
        info.result = await resp.text();
        if (gen !== info.resultGen) return;
        info.resultState = "loaded";
      } else if (resp.status === 204 || resp.status === 404) {
        // 204: a settled node without an artifact (a gate, `handoff:
        // session`). 404 should be unreachable for an id the stream gave us.
        info.resultState = "empty";
      } else {
        info.resultState = "error";
      }
      paintResultBlocks(id);
    })
    .catch(() => {
      if (gen !== info.resultGen) return;
      info.resultState = "error";
      paintResultBlocks(id);
    });
}

function paintResultBlocks(id) {
  for (const block of resultBlocks.get(id) || []) paintResultBlock(id, block);
}

// Repaints happen as the fetch settles, so every branch sets both the inline
// span and the block — a later repaint fully reverses an earlier one.
function paintResultBlock(id, { wrap, pre, more, inline }) {
  const info = nodeInfo(id);
  const text = info.resultState === "loaded" ? info.result.trim() : "";
  // A short artifact belongs in the head line ("e2e passed · 18s — PASS"),
  // not in a full block; a missing (or blank) artifact renders nothing at
  // all, so the meta line never floats under a "no result" placeholder.
  const oneLine = text !== "" && !text.includes("\n");
  const absent = info.resultState === "empty" || (info.resultState === "loaded" && text === "");
  inline.hidden = !oneLine;
  inline.textContent = oneLine ? `— ${text}` : "";
  wrap.hidden = oneLine || absent;
  if (wrap.hidden) {
    more.hidden = true;
    return;
  }
  const placeholder = info.resultState !== "loaded";
  pre.classList.toggle("placeholder", placeholder);
  switch (info.resultState) {
    case "loaded": pre.textContent = info.result; break;
    case "error": pre.textContent = "result unavailable"; break;
    default: pre.textContent = "loading…"; break;
  }
  // The expander appears only when the capped block actually overflows.
  more.hidden = !pre.classList.contains("expanded") && pre.scrollHeight <= pre.clientHeight + 1;
}

// --- painting ----------------------------------------------------------------

// nodeDuration is the node's wall-clock so far: live while it runs, frozen
// at its span once it settles, "" when it has not started (or the stream
// carried no usable timestamps).
function nodeDuration(info) {
  if (!info.startedMs) return "";
  if (info.state === "running") return fmtDuration(Date.now() - info.startedMs);
  if (!info.endedMs) return "";
  return fmtDuration(info.endedMs - info.startedMs);
}

// stateWord is the card face's second line: state + TIME. Cost is
// accounting, not the headline — it lives only in the feed's meta line (and
// the header total).
function stateWord(info) {
  if (info.state === "gate-paused") return "⏸ paused";
  const duration = nodeDuration(info);
  return duration ? `${info.state} ${duration}` : info.state;
}

function paint() {
  if (cy) {
    for (const [id, info] of nodes) {
      const el = cy.getElementById(id);
      if (!el.length) continue;
      el.data("state", info.state);
      el.data("label", `${id}\n${stateWord(info)}`);
    }
  }
  paintChips();
  syncPulse();
}

function paintChips() {
  const counts = {};
  if (cy) {
    cy.nodes().forEach((el) => {
      const s = el.data("state") || "pending";
      counts[s] = (counts[s] || 0) + 1;
    });
  } else {
    for (const info of nodes.values()) counts[info.state] = (counts[info.state] || 0) + 1;
  }
  const chips = $("state-chips");
  chips.textContent = "";
  for (const s of STATES) {
    if (!counts[s]) continue;
    const chip = document.createElement("span");
    chip.className = "chip";
    const dot = document.createElement("i");
    dot.className = `dot ${s}`;
    chip.appendChild(dot);
    const word = s === "gate-paused" ? "⏸ gate-paused" : s;
    chip.appendChild(document.createTextNode(`${counts[s]} ${word}`));
    chips.appendChild(chip);
  }
}

// Running motion: NOT cytoscape's animate() (a repeating pulse there is a
// known footgun with class manipulation) — one interval sine-oscillates
// border-opacity on the running nodes between ~0.45 and 1, and runs only
// while at least one node is running.
let pulseTimer = null;

function syncPulse() {
  if (!cy) return;
  const anyRunning = cy.nodes('[state = "running"]').length > 0;
  if (anyRunning && !pulseTimer) {
    pulseTimer = setInterval(() => {
      const t = performance.now() / 1000;
      const opacity = 0.725 + 0.275 * Math.sin(t * Math.PI * 2 * 0.8);
      cy.nodes('[state = "running"]').style("border-opacity", opacity);
    }, 100);
  } else if (!anyRunning && pulseTimer) {
    clearInterval(pulseTimer);
    pulseTimer = null;
  }
  // Nodes that just left running keep their last bypassed opacity: clear it.
  cy.nodes('[state != "running"]').removeStyle("border-opacity");
}

// setStatus renders the header status chip: the text names the state, and
// `live` toggles the one styled modifier (the CSS pulse). The state word
// deliberately gets no per-state class — text never wears a status color.
function setStatus(text, live) {
  const el = $("run-status");
  el.textContent = text;
  el.className = `status${live ? " live" : ""}`;
}

function banner(text) {
  // The banner is informational ink, not a status surface: a caution glyph
  // carries the tone so the text itself never wears a status color.
  $("banner").textContent = text ? `⚠ ${text}` : "";
}

// --- clock -------------------------------------------------------------------

function fmtDuration(ms) {
  const total = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  if (h) return `${h}h ${m}m ${s}s`;
  if (m) return `${m}m ${s}s`;
  return `${s}s`;
}

// One 1s ticker drives every live clock: the header elapsed time (from
// run_started to now, frozen at run_finished), each running node's card time
// line, and the ticking elapsed on each running node's open feed line.
function tick() {
  const el = $("elapsed");
  if (runStartedMs == null) {
    el.hidden = true;
  } else {
    el.hidden = false;
    el.textContent = fmtDuration((runEndedMs ?? Date.now()) - runStartedMs);
  }
  for (const [id, span] of liveElapsed) {
    const info = nodes.get(id);
    if (info && info.state === "running") span.textContent = nodeDuration(info);
  }
  if (!cy) return;
  for (const [id, info] of nodes) {
    if (info.state !== "running") continue;
    const node = cy.getElementById(id);
    if (node.length) node.data("label", `${id}\n${stateWord(info)}`);
  }
}
setInterval(tick, 1000);

loadGraph();
connect();
