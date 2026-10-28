#!/usr/bin/env bash
# Plausibility checks for a freshly built places.db, so that a broken build is
# not published: file size and the number of places per category.
#
#   scripts/check-data.sh <places.db>
#
# Thresholds (defaults fit Germany): MIN_SIZE_MB, MAX_SIZE_MB, MIN_PER_CATEGORY.
# Requires sqlite3.
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <places.db>" >&2
  exit 2
fi
db=$1
min_size_mb=${MIN_SIZE_MB:-50}
max_size_mb=${MAX_SIZE_MB:-2000}
min_per_category=${MIN_PER_CATEGORY:-500}
repo=$(cd "$(dirname "$0")/.." && pwd)
failed=0

size_mb=$(( $(wc -c < "$db") / 1024 / 1024 ))
echo "size: $size_mb MB"
if (( size_mb < min_size_mb || size_mb > max_size_mb )); then
  echo "size outside $min_size_mb..$max_size_mb MB" >&2
  failed=1
fi

counts=$(sqlite3 "$db" 'SELECT category, count(*) FROM places GROUP BY category')
while read -r id; do
  n=$(awk -F'|' -v id="$id" '$1 == id { print $2 }' <<< "$counts")
  n=${n:-0}
  printf '%-16s %8d\n' "$id" "$n"
  if (( n < min_per_category )); then
    echo "$id: fewer than $min_per_category places" >&2
    failed=1
  fi
done < <(sed -n 's/^  - id: //p' "$repo/internal/categories/categories.yaml")

exit "$failed"
