# Vendored libraries

All three files are vendored (and embedded via `go:embed`) so the
`oh-my-graph serve` page has zero runtime network dependencies. To upgrade
one: fetch a new pinned version, sanity-check it is non-trivially-sized
JavaScript, and update the version recorded here in the same commit.

Load order in index.html matters: cytoscape.js first, then dagre, then
cytoscape-dagre — the extension self-registers from the `cytoscape` and
`dagre` globals when loaded last.

## cytoscape.js

- File: `cytoscape.min.js` (~425 KB)
- Version: **3.34.0** (pinned — the exact version `https://unpkg.com/cytoscape@3/dist/cytoscape.min.js` resolved to when vendored, 2026-08-01)
- Upstream: <https://github.com/cytoscape/cytoscape.js>
- License: **MIT** — Copyright (c) 2016-2026, The Cytoscape Consortium. The
  full license text is the header comment of `cytoscape.min.js` itself.

## dagre

- File: `dagre.min.js` (~277 KB)
- Version: **0.8.5** (pinned — fetched from `https://unpkg.com/dagre@0.8.5/dist/dagre.min.js`, 2026-08-01)
- Upstream: <https://github.com/dagrejs/dagre>
- License: **MIT** — Copyright (c) 2012-2014 Chris Pettitt.

## cytoscape-dagre

- File: `cytoscape-dagre.js` (~12 KB)
- Version: **2.5.0** (pinned — the exact version `https://unpkg.com/cytoscape-dagre@2/cytoscape-dagre.js` resolved to when vendored, 2026-08-01)
- Upstream: <https://github.com/cytoscape/cytoscape.js-dagre>
- License: **MIT** — Copyright (c) 2016-2020, The Cytoscape Consortium.
