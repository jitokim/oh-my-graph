# Installing oh-my-graph

The one-liner is on the [front page](../README.md#quickstart):

```sh
go install github.com/jitokim/oh-my-graph/cmd/oh-my-graph@latest
```

This file is everything else — the prebuilt-binary path for people who would
rather not keep a Go toolchain around, and exactly what `oh-my-graph init`
writes into your tree.

Contributors iterating on the source want neither of these: `make build` plus a
symlink onto `PATH` is the loop for that, and it is described in
[CONTRIBUTING.md](../CONTRIBUTING.md#build-test-smoke).

## Runtime prerequisite

Install and sign in to at least one supported model CLI:

- Claude (the default): install `claude` and complete its normal login.
- Codex: install `codex`, run `codex login`, then put the run-wide selector
  before the subcommand: `oh-my-graph --runtime codex run graph.yaml`.

oh-my-graph deliberately uses the CLI's saved login. It removes Anthropic and
OpenAI API-key environment variables from child processes. The selected
runtime is persisted with the run, so `resume` and browser gate actions reuse
it; an explicit different runtime is refused rather than converting a run
midstream.

## Prebuilt binaries

Each tagged release also publishes prebuilt binaries on the [GitHub Releases
page](https://github.com/jitokim/oh-my-graph/releases) — darwin and linux, on
both `arm64` and `amd64`, as `.tar.gz` archives with a `checksums.txt` next to
them. There's no Homebrew tap, and Windows is not in the build matrix — build
from source there (see
[Platform support](LIMITATIONS.md#platform-support), which recommends WSL).

Pick a tag from the Releases page, then:

```sh
VERSION=0.7.0 OS=darwin ARCH=arm64   # the tag (without the leading v) and your platform
ARCHIVE="oh-my-graph_${VERSION}_${OS}_${ARCH}.tar.gz"
curl -sSfLO "https://github.com/jitokim/oh-my-graph/releases/download/v${VERSION}/${ARCHIVE}"
curl -sSfLO "https://github.com/jitokim/oh-my-graph/releases/download/v${VERSION}/checksums.txt"
grep " ${ARCHIVE}$" checksums.txt | shasum -a 256 -c -   # on linux: sha256sum -c -
tar xzf "${ARCHIVE}"
./oh-my-graph version
```

Move `oh-my-graph` onto your `PATH` and every command in the README runs
unchanged.

## What `oh-my-graph init` unpacks

`go install` copies one executable and nothing else, so `init` unpacks the
example graphs embedded in that executable into `./graphs/` — including
`./graphs/fragments/`, the shared node shapes three of those templates cite
with `use:`, without which they would not load. Pass a directory
(`oh-my-graph init <dir>`) to write to `<dir>/graphs/` instead.

It never overwrites. A file that is already there is kept exactly as it is and
named on stdout as kept, and only the payload files that are missing are
written — so re-running `init` is also how you collect a template or fragment a
later release added, without your edits being touched. A kept file whose bytes
differ from the binary's copy is marked `DIFFERS`, so a tree carrying an old
fragment under a freshly written template is visible in the listing rather than
something you find out about at load time.

That location rule is also why **where you keep a graph file decides whether it
can cite a fragment at all**: resolution looks in the graph's own `fragments/`
sibling directory and nowhere else, so a graph saved as a bare `/tmp/lane.yaml`
can use no fragment. Write such graphs under an `init`-ed `<dir>/graphs/`, or
put a `fragments/` directory next to the graph — a symlink to one is fine, since
resolution only reads the path
([ADR 0013](adr/0013-a-fragment-is-a-load-time-node-splice-not-a-runtime-concept.md)).
