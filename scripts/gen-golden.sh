#!/usr/bin/env bash
#
# Regenerate golden outputs from the Rust reference implementation.
#
# These files are the fidelity target for the Go port: every fixture gets the
# reference's Markdown, its positioned text items, and its detection verdict.
# The upstream repo commits only 6 Markdown snapshots, so this widens coverage
# to the whole corpus.
#
# Usage: scripts/gen-golden.sh [path-to-pdf-inspector]

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REF="${1:-$REPO_ROOT/../pdf-inspector}"

PDF2MD="$REF/target/release/pdf2md"
DETECT="$REF/target/release/detect-pdf"

if [[ ! -x "$PDF2MD" || ! -x "$DETECT" ]]; then
  echo "reference binaries not built at $REF/target/release" >&2
  echo "run: (cd $REF && cargo build --release --bin pdf2md --bin detect-pdf)" >&2
  exit 1
fi

FIXTURES="$REPO_ROOT/internal/pdf/testdata/fixtures"
OUT="$REPO_ROOT/internal/pdf/testdata/golden"
mkdir -p "$OUT/markdown" "$OUT/items" "$OUT/detect"

# password_for echoes the --password arguments a fixture needs, if any.
password_for() {
  case "$1" in
    encrypted-secret123.pdf) echo "--password secret123" ;;
    *) echo "" ;;
  esac
}

count=0
for pdf in "$FIXTURES"/*.pdf; do
  name="$(basename "$pdf" .pdf)"
  # shellcheck disable=SC2046 # word splitting is intended for the flag pair
  pw=$(password_for "$(basename "$pdf")")

  # shellcheck disable=SC2086
  "$PDF2MD" "$pdf" $pw           > "$OUT/markdown/$name.md"    2>/dev/null || echo "  markdown FAILED: $name" >&2
  # shellcheck disable=SC2086
  "$PDF2MD" "$pdf" --items-json $pw > "$OUT/items/$name.json"  2>/dev/null || echo "  items FAILED: $name" >&2
  # shellcheck disable=SC2086
  "$DETECT" "$pdf" --analyze --json $pw > "$OUT/detect/$name.json" 2>/dev/null || echo "  detect FAILED: $name" >&2

  count=$((count + 1))
  printf '  %-46s %8s bytes md\n' "$name" "$(wc -c < "$OUT/markdown/$name.md" | tr -d ' ')"
done

echo "generated goldens for $count fixtures into internal/pdf/testdata/golden"
