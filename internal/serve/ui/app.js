// oh-my-graph serve — single-run live view. Hand-written, no build step.
//
// Two sources, mirroring the run-feed contract (docs/RUN-FEED.md):
//   /api/graph   the DAG structure (polled until the snapshot exists — a
//                fresh run has no state.json until its first node completes)
//   /api/events  the event stream over SSE (replay, then follow)
// Events may arrive before the structure does; per-node state is kept in
// `nodes` and painted onto the cytoscape graph whenever either side updates.

"use strict";

const COLORS = {
  pending: "#8b93a7",
  running: "#2f6fed",
  passed: "#1e8e3e",
  failed: "#d93025",
  "gate-paused": "#e8930c",
};

const nodes = new Map(); // node id -> {state, verdict, sessionId, costUsd, detail}
let cy = null;
let totalCost = 0;
let selectedNode = null;

const $ = (id) => document.getElementById(id);

function nodeInfo(id) {
  if (!nodes.has(id)) {
    nodes.set(id, { state: "pending", verdict: "", sessionId: "", costUsd: 0, detail: "" });
  }
  return nodes.get(id);
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
    setStatus("waiting for structure", "");
    setTimeout(loadGraph, 2000);
    return;
  }
  render(payload);
}

function render(payload) {
  const elements = [];
  for (const node of payload.nodes || []) {
    elements.push({ data: { id: node.id, label: node.id, gate: node.type === "gate" } });
    for (const parent of node.depends_on || []) {
      elements.push({ data: { id: `${parent}->${node.id}`, source: parent, target: node.id } });
    }
  }
  cy = cytoscape({
    container: $("cy"),
    elements,
    autoungrabify: true,
    style: [
      {
        selector: "node",
        style: {
          label: "data(label)",
          "background-color": COLORS.pending,
          "text-valign": "bottom",
          "text-margin-y": 6,
          "font-size": 12,
          color: "#1f2430",
          width: 34,
          height: 34,
        },
      },
      { selector: "node[?gate]", style: { shape: "diamond" } },
      {
        selector: "edge",
        style: {
          width: 2,
          "line-color": "#c3c9d4",
          "target-arrow-color": "#c3c9d4",
          "target-arrow-shape": "triangle",
          "curve-style": "bezier",
        },
      },
      { selector: "node:selected", style: { "border-width": 3, "border-color": "#1f2430" } },
    ],
    layout: { name: "breadthfirst", directed: true, padding: 24, spacingFactor: 1.15 },
  });
  cy.on("tap", "node", (evt) => {
    selectedNode = evt.target.id();
    showDetail(selectedNode);
  });
  cy.on("tap", (evt) => {
    if (evt.target === cy) {
      selectedNode = null;
      $("detail").hidden = true;
    }
  });
  paint();
}

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
    setStatus("reconnecting", "");
    totalCost = 0;
  };
}

function apply(event) {
  switch (event.event) {
    case "run_started":
      setStatus("running", "running");
      break;
    case "node_started":
    case "node_retried":
      nodeInfo(event.node_id).state = "running";
      break;
    case "node_passed":
    case "node_failed": {
      const info = nodeInfo(event.node_id);
      info.state = event.event === "node_passed" ? "passed" : "failed";
      info.verdict = event.verdict || "";
      info.sessionId = event.session_id || "";
      info.costUsd = event.cost_usd || 0;
      info.detail = event.detail || "";
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
      setStatus(event.outcome, event.outcome);
      break;
    default:
      // Unknown event type (same-schema addition impossible, but be safe):
      // skip rather than fail, per the contract.
      break;
  }
  paint();
}

// --- painting ----------------------------------------------------------------

function paint() {
  if (!cy) return;
  for (const [id, info] of nodes) {
    const el = cy.getElementById(id);
    if (el.length) el.style("background-color", COLORS[info.state] || COLORS.pending);
  }
  if (selectedNode) showDetail(selectedNode);
}

function showDetail(id) {
  const info = nodeInfo(id);
  $("detail-id").textContent = id;
  $("detail-state").textContent = info.state;
  $("detail-verdict").textContent = info.verdict || "—";
  $("detail-session").textContent = info.sessionId || "—";
  $("detail-cost").textContent = `$${info.costUsd.toFixed(4)}`;
  $("detail-detail").textContent = info.detail || "—";
  $("detail").hidden = false;
}

function setStatus(text, cls) {
  const el = $("run-status");
  el.textContent = text;
  el.className = `status ${cls}`;
}

function banner(text) {
  $("banner").textContent = text;
}

loadGraph();
connect();
