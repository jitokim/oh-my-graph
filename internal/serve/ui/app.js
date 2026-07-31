// oh-my-graph serve — single-run live view. Hand-written, no build step.
//
// Two sources, mirroring the run-feed contract (docs/RUN-FEED.md):
//   /api/graph   the DAG structure (polled until the snapshot exists — a
//                fresh run has no state.json until its first node completes)
//   /api/events  the event stream over SSE (replay, then follow)
// Events may arrive before the structure does; per-node state is kept in
// `nodes` and painted onto the cytoscape graph whenever either side updates.
//
// Theme: CSS custom properties in style.css are the token source of truth;
// cytoscape styles cannot read CSS vars, so the resolved values are read via
// getComputedStyle at init and the graph is re-styled on theme change.

"use strict";

// Status palette — one source of truth, used identically in node borders,
// legend chips, header chips (style.css mirrors these hexes) and the detail
// panel. Same hexes in both themes; status is never color alone — every
// status surface pairs the color with a text label, and "running" gets
// motion as its primary signal.
const COLORS = {
  pending: "#898781",
  running: "#fab219",
  passed: "#0ca30c",
  failed: "#d03b3b",
  "gate-paused": "#ec835a",
};
const STATES = ["pending", "running", "passed", "failed", "gate-paused"];

const nodes = new Map(); // node id -> {state, verdict, sessionId, costUsd, detail, startedMs, endedMs}
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
        "border-color": COLORS.pending,
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
      style: { "border-color": COLORS[s] },
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
      banner(`graph: ${(await resp.text()).trim()}`);
      return;
    }
    payload = await resp.json();
  } catch (err) {
    banner(`graph: ${err}`);
    return;
  }
  $("run-id").textContent = payload.run_id;
  if (!payload.available) {
    // Honest window: structure is unknown until the first node's terminal
    // verdict writes state.json. Keep polling; events still stream meanwhile.
    setStatus("waiting for structure", "", false);
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
  // The server's terminal refusal (e.g. a stream schema newer than the
  // binary), distinct from EventSource's own connection errors.
  source.addEventListener("stream_error", (msg) => {
    source.close();
    banner(JSON.parse(msg.data).error);
  });
  source.onerror = () => {
    // Connection dropped (server stopped, laptop slept): EventSource retries
    // by itself; replay-from-zero keeps the state idempotent enough (costs
    // are recomputed from scratch below on each full replay).
    setStatus("reconnecting", "", false);
    totalCost = 0;
  };
}

function apply(event) {
  const ts = event.ts ? Date.parse(event.ts) : NaN;
  switch (event.event) {
    case "run_started":
      runStartedMs = Number.isNaN(ts) ? Date.now() : ts;
      runEndedMs = null;
      setStatus("running", "running", true);
      break;
    case "node_started":
    case "node_retried": {
      const info = nodeInfo(event.node_id);
      info.state = "running";
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
      setStatus(event.outcome, event.outcome, false);
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

function stateWord(info) {
  if (info.state === "passed" && info.costUsd) return `passed $${info.costUsd.toFixed(2)}`;
  if (info.state === "gate-paused") return "⏸ paused";
  return info.state;
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
  $("detail-state").textContent = info.state === "gate-paused" ? "⏸ gate-paused" : info.state;
  $("detail-duration").textContent = info.startedMs
    ? fmtDuration((info.endedMs ?? Date.now()) - info.startedMs)
    : "—";
  $("detail-verdict").textContent = info.verdict || "—";
  $("detail-session").textContent = info.sessionId || "—";
  $("detail-cost").textContent = `$${info.costUsd.toFixed(4)}`;
  $("detail-detail").textContent = info.detail || "—";
  $("detail").hidden = false;
}

function setStatus(text, cls, live) {
  const el = $("run-status");
  el.textContent = text;
  el.className = `status ${cls}${live ? " live" : ""}`;
}

function banner(text) {
  $("banner").textContent = text;
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

// One 1s ticker drives the header elapsed time (from run_started to now,
// frozen at run_finished) and the live duration of a selected running node.
function tick() {
  const el = $("elapsed");
  if (runStartedMs == null) {
    el.hidden = true;
  } else {
    el.hidden = false;
    el.textContent = fmtDuration((runEndedMs ?? Date.now()) - runStartedMs);
  }
  if (selectedNode) {
    const info = nodes.get(selectedNode);
    if (info && info.state === "running") showDetail(selectedNode);
  }
}
setInterval(tick, 1000);

loadGraph();
connect();
