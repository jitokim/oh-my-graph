#!/usr/bin/env bash
# Fixture pairs 3 and 4 — added after run 2, to widen the sample beyond the two
# goal shapes PREREG.md named. The pairs it adds have the SAME shape as 1 and 2
# (A is the invocation repository, B is the foreign checkout named by absolute
# path) and differ only in domain and wording; the metric, the population rule
# and the stop rule are untouched. The extension is disclosed in the report.
#
# Same rules as setup-fixtures.sh: /tmp only, no remote, neutral identity.
set -euo pipefail

FIXROOT="${FIXROOT:-/tmp/omg-0018/fixtures}"
mkdir -p "$FIXROOT"

init_repo() {
  local dir="$1"
  rm -rf "$dir/.git"
  git -C "$dir" init -q -b main
  git -C "$dir" config user.name 'omg fixture'
  git -C "$dir" config user.email 'fixture@example.invalid'
  git -C "$dir" config commit.gpgsign false
  git -C "$dir" add -A
  git -C "$dir" commit -q -m 'initial fixture state'
}

# ---------------------------------------------------------------- pair 3: A
rm -rf "$FIXROOT/docs-site"
mkdir -p "$FIXROOT/docs-site/content"
cat >"$FIXROOT/docs-site/README.md" <<'EOF'
# docs-site

The public documentation site. Colours come from the brand-assets repository;
this repository only references token names.
EOF
cat >"$FIXROOT/docs-site/content/styleguide.md" <<'EOF'
# Style guide

Buttons use the `accent` token for their background and `ink` for their label.

| element | token |
|---------|-------|
| primary button | `accent` |
| link hover | `accent` |
| body text | `ink` |
EOF
cat >"$FIXROOT/docs-site/content/theme.css" <<'EOF'
.button-primary { background: var(--accent); color: var(--ink); }
.link:hover { color: var(--accent); }
EOF
init_repo "$FIXROOT/docs-site"

# ---------------------------------------------------------------- pair 3: B
rm -rf "$FIXROOT/brand-assets"
mkdir -p "$FIXROOT/brand-assets"
cat >"$FIXROOT/brand-assets/README.md" <<'EOF'
# brand-assets

The single source of truth for colour tokens. `tokens.json` is what tools read;
`palette.md` is what people read.
EOF
cat >"$FIXROOT/brand-assets/tokens.json" <<'EOF'
{
  "accent": "#3b6ef5",
  "ink": "#101318",
  "paper": "#ffffff"
}
EOF
cat >"$FIXROOT/brand-assets/palette.md" <<'EOF'
# Palette

| token | value | used for |
|-------|-------|----------|
| `accent` | `#3b6ef5` | primary actions and links |
| `ink` | `#101318` | body text |
| `paper` | `#ffffff` | page background |
EOF
init_repo "$FIXROOT/brand-assets"

# ---------------------------------------------------------------- pair 4: A
rm -rf "$FIXROOT/order-service"
mkdir -p "$FIXROOT/order-service/orders" "$FIXROOT/order-service/tests"
cat >"$FIXROOT/order-service/README.md" <<'EOF'
# order-service

Serializes orders onto the wire. The message shape is defined in the proto-defs
repository; the serializer here is written by hand against it.
EOF
cat >"$FIXROOT/order-service/orders/serialize.py" <<'EOF'
"""Hand-written serializer for the Order message."""

FIELDS = ("id", "sku", "quantity")


def serialize(order):
    return {name: order[name] for name in FIELDS}
EOF
cat >"$FIXROOT/order-service/tests/test_serialize.py" <<'EOF'
from orders.serialize import serialize


def test_serialize_keeps_only_known_fields():
    out = serialize({"id": "o-1", "sku": "s-1", "quantity": 2, "extra": "x"})
    assert out == {"id": "o-1", "sku": "s-1", "quantity": 2}
EOF
init_repo "$FIXROOT/order-service"

# ---------------------------------------------------------------- pair 4: B
rm -rf "$FIXROOT/proto-defs"
mkdir -p "$FIXROOT/proto-defs/proto"
cat >"$FIXROOT/proto-defs/README.md" <<'EOF'
# proto-defs

Wire definitions shared by every service. FIELDS.md describes each field in
prose; proto/order.proto is the definition itself.
EOF
cat >"$FIXROOT/proto-defs/proto/order.proto" <<'EOF'
syntax = "proto3";

package orders;

message Order {
  string id = 1;
  string sku = 2;
  int32 quantity = 3;
}
EOF
cat >"$FIXROOT/proto-defs/FIELDS.md" <<'EOF'
# Order fields

| field | number | type | meaning |
|-------|--------|------|---------|
| `id` | 1 | string | the order's identifier |
| `sku` | 2 | string | the item ordered |
| `quantity` | 3 | int32 | how many were ordered |
EOF
init_repo "$FIXROOT/proto-defs"

echo "fixtures 3+4 built under $FIXROOT:"
for repo in docs-site brand-assets order-service proto-defs; do
  printf '  %-16s %s %s\n' "$repo" \
    "$(git -C "$FIXROOT/$repo" rev-parse --short HEAD)" \
    "$(git -C "$FIXROOT/$repo" rev-parse --abbrev-ref HEAD)"
done
