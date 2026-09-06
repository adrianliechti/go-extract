#!/usr/bin/env bash
#
# Score converter output against the corpus ground truth.
#
# For each source document that has a matching ground-truth Markdown file,
# convert it and report whether the output is identical, close, or divergent.
# Trailing whitespace is normalised on both sides before comparison.
#
# Usage: scripts/score.sh [corpus-root] [ext ...]

set -uo pipefail

ROOT="${1:-internal/pdf/testdata/external/test_documents}"
shift || true
EXTS=("$@")
if [ ${#EXTS[@]} -eq 0 ]; then
  EXTS=(docx xlsx pptx)
fi

if [ ! -d "$ROOT/ground_truth" ]; then
  echo "no ground_truth under $ROOT" >&2
  echo "fetch the corpus first (see the pdf repo's 'task corpus-fetch')" >&2
  exit 1
fi

BIN="$(mktemp -d)/ooxml2md"
go build -o "$BIN" ./cmd/ooxml2md || exit 1

norm() { sed -e 's/[[:space:]]*$//' -e '/^$/N;/^\n$/D' "$1"; }

total=0; identical=0; close=0; diverged=0; failed=0
declare -a problems=()

for ext in "${EXTS[@]}"; do
  gtdir="$ROOT/ground_truth/$ext"
  [ -d "$gtdir" ] || continue

  while IFS= read -r src; do
    base="$(basename "$src" ".$ext")"
    gt="$gtdir/$base.md"
    [ -f "$gt" ] || continue

    total=$((total + 1))
    if ! "$BIN" -quiet "$src" > /tmp/ooxml_out.md 2>/tmp/ooxml_err.txt; then
      failed=$((failed + 1))
      problems+=("FAIL      $ext/$base: $(head -1 /tmp/ooxml_err.txt)")
      continue
    fi

    if diff -q <(norm /tmp/ooxml_out.md) <(norm "$gt") >/dev/null 2>&1; then
      identical=$((identical + 1))
      continue
    fi

    # Compare word bags to separate "formatting differs" from "content missing".
    a=$(tr -cs '[:alnum:]' '\n' < /tmp/ooxml_out.md | sort | uniq -c | md5)
    b=$(tr -cs '[:alnum:]' '\n' < "$gt" | sort | uniq -c | md5)
    if [ "$a" = "$b" ]; then
      close=$((close + 1))
      problems+=("FORMAT    $ext/$base: same words, different layout")
    else
      diverged=$((diverged + 1))
      got=$(tr -cs '[:alnum:]' '\n' < /tmp/ooxml_out.md | grep -c . || true)
      want=$(tr -cs '[:alnum:]' '\n' < "$gt" | grep -c . || true)
      problems+=("CONTENT   $ext/$base: $got words vs $want expected")
    fi
  done < <(find "$ROOT" -iname "*.$ext" -not -path "*/.git/*" | sort)
done

printf '\n== ooxml ground-truth score ==\n'
printf 'compared:  %d\n' "$total"
printf 'identical: %d\n' "$identical"
printf 'format:    %d  (same words, layout differs)\n' "$close"
printf 'content:   %d  (word counts differ)\n' "$diverged"
printf 'failed:    %d\n' "$failed"

if [ ${#problems[@]} -gt 0 ]; then
  printf '\n-- details --\n'
  printf '%s\n' "${problems[@]}" | sort | head -60
fi
