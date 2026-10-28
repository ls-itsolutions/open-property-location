#!/usr/bin/env bash
# Starts opl on a database and checks that the nearest place of a category is
# within the given distance.
#
#   scripts/smoke-test.sh <opl-binary> <places.db> <lat> <lon> <category> <max-meters>
#   scripts/smoke-test.sh ./opl out/places.db 52.5219 13.4132 train_station 500
#
# Requires curl and jq.
set -euo pipefail

if [[ $# -ne 6 ]]; then
  echo "usage: $0 <opl-binary> <places.db> <lat> <lon> <category> <max-meters>" >&2
  exit 2
fi
opl=$1 db=$2 lat=$3 lon=$4 category=$5 max=$6
base="http://127.0.0.1:${PORT:-18080}"

"$opl" serve --data "$db" --addr "${base#http://}" &
pid=$!
trap 'kill "$pid"' EXIT

for _ in $(seq 50); do
  curl -sf "$base/health" > /dev/null && break
  sleep 0.2
done

resp=$(curl -sf "$base/v1/surroundings?lat=$lat&lon=$lon&categories=$category&limit=1")
echo "$resp" | jq .
if ! echo "$resp" | jq -e --argjson max "$max" \
  '.categories[0].places | length > 0 and .[0].distanceMeters <= $max' > /dev/null; then
  echo "smoke test failed: no $category within $max m of $lat,$lon" >&2
  exit 1
fi
echo "smoke test passed"
