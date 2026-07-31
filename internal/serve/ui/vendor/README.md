# Vendored: cytoscape.js

- File: `cytoscape.min.js`
- Version: **3.34.0** (pinned — the exact version `https://unpkg.com/cytoscape@3/dist/cytoscape.min.js` resolved to when vendored, 2026-08-01)
- Upstream: <https://github.com/cytoscape/cytoscape.js>
- License: **MIT** — Copyright (c) 2016-2026, The Cytoscape Consortium. The
  full license text is the header comment of `cytoscape.min.js` itself.

Vendored (and embedded via `go:embed`) so the `oh-my-graph serve` page has
zero runtime network dependencies. To upgrade: fetch a new pinned version,
sanity-check it is non-trivially-sized JavaScript, and update the version
recorded here in the same commit.
