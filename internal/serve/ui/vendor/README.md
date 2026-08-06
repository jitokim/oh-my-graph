# Vendored libraries

All three files are vendored (and embedded via `go:embed`) so the
`oh-my-graph serve` page has zero runtime network dependencies. Every file
is committed byte-for-byte identical to the published build it was pinned
from; the SHA-256 below is asserted by
`TestHandler_ServesEmbeddedUIWithVendoredCytoscape`, so an upgrade must
fetch a new pinned version, verify its hash against the fetch URL, and
update the version AND hash recorded here in the same commit.

Load order in index.html matters: cytoscape.js first, then dagre, then
cytoscape-dagre — the extension self-registers from the `cytoscape` and
`dagre` globals when loaded last.

**A cytoscape bump must re-verify the CSP** (`contentSecurityPolicy` in
`serve.go`). The served pages ship a strict policy that is written against what
these files actually do, and cytoscape sits right on two of its edges: it
injects an inline `<style>` element (`.__________cytoscape_container {
position: relative; }`) — which is the whole reason `style-src` carries
`'unsafe-inline'` — and today it needs neither `eval`/`new Function` nor a
Worker, which is why `script-src` stays `'self'` and `default-src` stays
`'none'`. A new version that changed either would break the map *silently* in
the browser, with nothing but a console violation to say so, because no Go test
can execute it. On upgrade, re-grep the new file for `eval(`, `new Function`
and `new Worker`, then load `/run/<id>/` and confirm the console is free of CSP
violations and the DAG still lays out.

## cytoscape.js

- File: `cytoscape.min.js` (~425 KB)
- Version: **3.34.0** (pinned — the exact version `https://unpkg.com/cytoscape@3/dist/cytoscape.min.js` resolved to when vendored, 2026-08-01)
- Upstream: <https://github.com/cytoscape/cytoscape.js>
- License: **MIT** — Copyright (c) 2016-2026, The Cytoscape Consortium. The
  full license text is the header comment of `cytoscape.min.js` itself.
- SHA-256: `9c2a3bf2592e0b14a1f7bec07c03a54f16dedf32af9cd0af155c716aa6c87bc3`

## dagre

- File: `dagre.min.js` (~277 KB)
- Version: **0.8.5** (pinned — fetched from `https://unpkg.com/dagre@0.8.5/dist/dagre.min.js`, 2026-08-01)
- Upstream: <https://github.com/dagrejs/dagre>
- License: **MIT** — Copyright (c) 2012-2014 Chris Pettitt (copyright lines
  are embedded in the minified file).
- SHA-256: `62eb9787ccfdbdf4148d4d99d31dbf9ee4770eafee81e637d759b52aac22cd51`

## cytoscape-dagre

- File: `cytoscape-dagre.js` (~12 KB)
- Version: **2.5.0** (pinned — the exact version `https://unpkg.com/cytoscape-dagre@2/cytoscape-dagre.js` resolved to when vendored, 2026-08-01)
- Upstream: <https://github.com/cytoscape/cytoscape.js-dagre>
- License: **MIT** — Copyright (c) 2016-2020, The Cytoscape Consortium. The
  published file carries no in-file license header; it is kept byte-identical
  to upstream (hash below), so the MIT text is reproduced here instead:

  > Permission is hereby granted, free of charge, to any person obtaining a
  > copy of this software and associated documentation files (the
  > "Software"), to deal in the Software without restriction, including
  > without limitation the rights to use, copy, modify, merge, publish,
  > distribute, sublicense, and/or sell copies of the Software, and to
  > permit persons to whom the Software is furnished to do so, subject to
  > the following conditions: The above copyright notice and this permission
  > notice shall be included in all copies or substantial portions of the
  > Software. THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY
  > KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES
  > OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND
  > NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE
  > LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION
  > OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION
  > WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

- SHA-256: `bf70fe402991dcbff33e05a7e4a5271c78020bb75e85d1c80ab7538e4157112e`
