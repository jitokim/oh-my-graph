#!/usr/bin/env bash
# Build the throwaway fixture repositories this measurement runs against.
#
# NOTHING here may name a real checkout. Every repository lives under
# /tmp/omg-0018/fixtures, has no remote, and carries a neutral commit identity,
# so a node let loose in one can do no damage worth naming.
#
# Two pairs. In each pair A is the repository oh-my-graph is invoked from and B
# is the foreign checkout the goal names by absolute path — the shape #103
# reported. Each pair has enough shape that one plausible goal spans both.
#
# Idempotent: it wipes and rebuilds the fixture root, so a second run of the
# measurement starts from the same HEAD every fixture had the first time.
set -euo pipefail

FIXROOT="${FIXROOT:-/tmp/omg-0018/fixtures}"

rm -rf "$FIXROOT"
mkdir -p "$FIXROOT"

init_repo() {
  local dir="$1"
  git -C "$dir" init -q -b main
  git -C "$dir" config user.name 'omg fixture'
  git -C "$dir" config user.email 'fixture@example.invalid'
  git -C "$dir" config commit.gpgsign false
  git -C "$dir" add -A
  git -C "$dir" -c user.name='omg fixture' -c user.email='fixture@example.invalid' \
    commit -q -m 'initial fixture state'
}

# ---------------------------------------------------------------- pair 1: A
mkdir -p "$FIXROOT/payments-api/payments" "$FIXROOT/payments-api/tests"
cat >"$FIXROOT/payments-api/README.md" <<'EOF'
# payments-api

A very small payment gateway client. Settings come from a YAML file whose keys
are described in the shared-config repository.
EOF
cat >"$FIXROOT/payments-api/payments/config.py" <<'EOF'
"""Loads the gateway settings from a plain key: value file."""


DEFAULTS = {
    "endpoint": "https://gateway.example.invalid/v1",
    "retries": 3,
}


def load_config(path):
    settings = dict(DEFAULTS)
    with open(path, encoding="utf-8") as handle:
        for line in handle:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            key, _, value = line.partition(":")
            settings[key.strip()] = value.strip()
    settings["retries"] = int(settings["retries"])
    return settings
EOF
cat >"$FIXROOT/payments-api/payments/client.py" <<'EOF'
"""The gateway client. Nothing here talks to a network."""

from payments.config import load_config


class Client:
    def __init__(self, config_path):
        self.settings = load_config(config_path)

    def describe(self):
        return "{endpoint} (retries={retries})".format(**self.settings)
EOF
cat >"$FIXROOT/payments-api/tests/test_config.py" <<'EOF'
from payments.config import load_config


def test_defaults_are_applied(tmp_path):
    path = tmp_path / "settings.yaml"
    path.write_text("endpoint: https://example.invalid\n", encoding="utf-8")
    settings = load_config(str(path))
    assert settings["endpoint"] == "https://example.invalid"
    assert settings["retries"] == 3
EOF
init_repo "$FIXROOT/payments-api"

# ---------------------------------------------------------------- pair 1: B
mkdir -p "$FIXROOT/shared-config/config" "$FIXROOT/shared-config/docs"
cat >"$FIXROOT/shared-config/README.md" <<'EOF'
# shared-config

The settings several services read. `config/defaults.yaml` is the shipped
default set; `docs/schema.md` describes every key in it.
EOF
cat >"$FIXROOT/shared-config/config/defaults.yaml" <<'EOF'
endpoint: https://gateway.example.invalid/v1
retries: 3
EOF
cat >"$FIXROOT/shared-config/docs/schema.md" <<'EOF'
# Settings schema

| key | type | default | meaning |
|-----|------|---------|---------|
| `endpoint` | string | `https://gateway.example.invalid/v1` | gateway base URL |
| `retries` | integer | `3` | how many times a failed call is retried |
EOF
init_repo "$FIXROOT/shared-config"

# ---------------------------------------------------------------- pair 2: A
mkdir -p "$FIXROOT/report-cli/reportcli" "$FIXROOT/report-cli/tests"
cat >"$FIXROOT/report-cli/README.md" <<'EOF'
# report-cli

Prints a small table of numbers. Rendering helpers live in the chart-lib
repository.
EOF
cat >"$FIXROOT/report-cli/reportcli/__init__.py" <<'EOF'
EOF
cat >"$FIXROOT/report-cli/reportcli/main.py" <<'EOF'
"""The report command."""

import argparse


def render_table(rows):
    return "\n".join("{:<10} {:>6}".format(name, value) for name, value in rows)


def main(argv=None):
    parser = argparse.ArgumentParser(prog="report")
    parser.add_argument("--rows", default="alpha=1,beta=2")
    args = parser.parse_args(argv)
    rows = []
    for pair in args.rows.split(","):
        name, _, value = pair.partition("=")
        rows.append((name, int(value)))
    print(render_table(rows))
    return 0
EOF
cat >"$FIXROOT/report-cli/tests/test_main.py" <<'EOF'
from reportcli.main import render_table


def test_render_table_aligns_columns():
    assert render_table([("alpha", 1)]).startswith("alpha")
EOF
init_repo "$FIXROOT/report-cli"

# ---------------------------------------------------------------- pair 2: B
mkdir -p "$FIXROOT/chart-lib/chartlib"
cat >"$FIXROOT/chart-lib/README.md" <<'EOF'
# chart-lib

Text rendering helpers shared by the reporting tools.
EOF
cat >"$FIXROOT/chart-lib/chartlib/__init__.py" <<'EOF'
from chartlib.render import render_sparkline

__all__ = ["render_sparkline"]
EOF
cat >"$FIXROOT/chart-lib/chartlib/render.py" <<'EOF'
"""Text renderers. Every function returns a string and prints nothing."""

BLOCKS = " .:-=+*#%@"


def render_sparkline(values):
    if not values:
        return ""
    top = max(values) or 1
    return "".join(BLOCKS[min(len(BLOCKS) - 1, v * (len(BLOCKS) - 1) // top)] for v in values)
EOF
cat >"$FIXROOT/chart-lib/CHANGELOG.md" <<'EOF'
# Changelog

## Unreleased

- `render_sparkline` renders a list of integers as a one-line sparkline.
EOF
init_repo "$FIXROOT/chart-lib"

echo "fixtures built under $FIXROOT:"
for repo in payments-api shared-config report-cli chart-lib; do
  printf '  %-16s %s %s\n' "$repo" \
    "$(git -C "$FIXROOT/$repo" rev-parse --short HEAD)" \
    "$(git -C "$FIXROOT/$repo" rev-parse --abbrev-ref HEAD)"
done
