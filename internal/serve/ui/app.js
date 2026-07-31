// oh-my-graph serve — single-run live view. Hand-written, no build step.
//
// Three sources, mirroring the run-feed contract (docs/RUN-FEED.md):
//   /api/graph   the DAG structure (polled until the snapshot exists — a
//                fresh run has no state.json until its first node completes)
//   /api/events  the event stream over SSE (replay, then follow)
//   /api/result  one node's handoff artifact, fetched lazily for the detail
//                panel once that node settles (200 body / 204 none / 404
//                unknown node)
// Events may arrive before the structure does; per-node state is kept in
// `nodes` and painted onto the cytoscape graph whenever either side updates.
//
// Theme: CSS custom properties in style.css are the token source of truth;
// cytoscape styles cannot read CSS vars, so the resolved values are read via
// getComputedStyle at init and the graph is re-styled on theme change.

"use strict";

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
// stream for completeness but currently displayed nowhere — the panel's
// status line and the card border already carry the same fact.
const nodes = new Map();
let cy = null;
let totalCost = 0;
let selectedNode = null;
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
      result: "", resultState: "none",
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
  if (!payload.available) {
    // Honest window: structure is unknown until the first node's terminal
    // verdict writes state.json. Keep polling; events still stream meanwhile.
    setStatus("waiting for structure", false);
    setTimeout(loadGraph, 2000);
    return;
  }
  render(payload);
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
  cy.on("tap", "node", (evt) => {
    selectedNode = evt.target.id();
    showDetail(selectedNode);
  });
  cy.on("tap", (evt) => {
    if (evt.target === cy) closeDetail();
  });
  paint();
}

function closeDetail() {
  selectedNode = null;
  $("detail").hidden = true;
  if (cy) cy.$(":selected").unselect();
}

$("detail-close").addEventListener("click", closeDetail);

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
    // by itself; replay-from-zero keeps the state idempotent enough (costs
    // are recomputed from scratch below on each full replay).
    setStatus("reconnecting", false);
    totalCost = 0;
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
      // here, on the state change itself, so a node retried while UNselected
      // also refetches when it re-settles — a paint function only runs for
      // the selected node and must not own this invalidation.
      info.result = "";
      info.resultState = "none";
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
  paint();
  tick();
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
// accounting, not the headline — it lives only in the detail panel (and the
// header total).
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
  if (selectedNode) showDetail(selectedNode);
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

function showDetail(id) {
  const info = nodeInfo(id);
  $("detail-id").textContent = id;

  // One status line under the id: dot + state word + duration. Verdict gets
  // no row of its own — it duplicates the state, and the card border already
  // wears it.
  $("detail-dot").className = `dot ${info.state}`;
  const word = info.state === "gate-paused" ? "⏸ gate-paused" : info.state;
  const duration = nodeDuration(info);
  $("detail-state").textContent = duration ? `${word} · ${duration}` : word;

  // `detail` only when non-empty; on failure it leads, emphasized — the
  // failure cause is THE human information then. Otherwise it is a footnote
  // under the result.
  const failed = info.state === "failed";
  const fail = $("detail-fail");
  fail.hidden = !(failed && info.detail);
  if (!fail.hidden) fail.textContent = info.detail;
  const note = $("detail-note");
  note.hidden = failed || !info.detail;
  if (!note.hidden) note.textContent = info.detail;

  renderResult(id, info);

  // One compact accounting line: truncated session ref (click to copy) and
  // per-node cost — cross-tool reference and bookkeeping, not headlines.
  const session = $("detail-session");
  session.hidden = !info.sessionId;
  if (info.sessionId) {
    session.textContent = info.sessionId.slice(0, 8);
    session.title = `${info.sessionId} — click to copy`;
  }
  $("detail-cost").textContent = info.costUsd ? `$${info.costUsd.toFixed(4)}` : "";

  $("detail").hidden = false;
}

// renderResult paints the panel's result block from the node's fetch state,
// kicking off the lazy /api/result fetch the first time the node is seen
// settled. Fetch-state INVALIDATION lives in apply()'s (re)start branch, not
// here — a paint function runs only for the selected node. The artifact is
// rendered via textContent ONLY — never innerHTML: node output is untrusted
// text, not markup.
function renderResult(id, info) {
  const pre = $("detail-result");
  const settled = info.state === "passed" || info.state === "failed";
  if (!settled) {
    setResult(pre, "no result yet", true);
    return;
  }
  switch (info.resultState) {
    case "loaded":
      setResult(pre, info.result, false);
      return;
    case "empty":
      setResult(pre, "no result", true);
      return;
    case "error":
      setResult(pre, "result unavailable", true);
      return;
    case "loading":
      // Repaint even mid-fetch: the pre is shared across selections, so
      // without this a click away and back would show the OTHER node's text
      // under this node's heading until the fetch lands.
      setResult(pre, "loading…", true);
      return;
  }
  info.resultState = "loading";
  setResult(pre, "loading…", true);
  fetch(`api/result?node=${encodeURIComponent(id)}`)
    .then(async (resp) => {
      if (resp.status === 200) {
        info.result = await resp.text();
        info.resultState = "loaded";
      } else if (resp.status === 204 || resp.status === 404) {
        // 204: a settled node without an artifact (a gate, `handoff:
        // session`). 404 should be unreachable for an id the graph gave us.
        info.resultState = "empty";
      } else {
        info.resultState = "error";
      }
      if (selectedNode === id) showDetail(id);
    })
    .catch(() => {
      info.resultState = "error";
      if (selectedNode === id) showDetail(id);
    });
}

function setResult(pre, text, placeholder) {
  pre.textContent = text;
  pre.classList.toggle("placeholder", placeholder);
}

$("detail-session").addEventListener("click", () => {
  const info = selectedNode && nodes.get(selectedNode);
  if (!info || !info.sessionId) return;
  navigator.clipboard.writeText(info.sessionId).then(() => {
    // Brief confirmation; the next repaint restores the truncated ref.
    $("detail-session").textContent = "copied";
    setTimeout(() => { if (selectedNode) showDetail(selectedNode); }, 900);
  });
});

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
// line, and the live duration of a selected running node.
function tick() {
  const el = $("elapsed");
  if (runStartedMs == null) {
    el.hidden = true;
  } else {
    el.hidden = false;
    el.textContent = fmtDuration((runEndedMs ?? Date.now()) - runStartedMs);
  }
  if (cy) {
    for (const [id, info] of nodes) {
      if (info.state !== "running") continue;
      const node = cy.getElementById(id);
      if (node.length) node.data("label", `${id}\n${stateWord(info)}`);
    }
  }
  if (selectedNode) {
    const info = nodes.get(selectedNode);
    if (info && info.state === "running") showDetail(selectedNode);
  }
}
setInterval(tick, 1000);

loadGraph();
connect();
