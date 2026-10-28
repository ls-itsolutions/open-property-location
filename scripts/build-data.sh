#!/usr/bin/env bash
# Builds places.db for a Geofabrik region.
#
#   scripts/build-data.sh <geofabrik-path> <out-dir>
#   scripts/build-data.sh europe/germany/bremen out
#
# Requires go and osmium-tool. Intermediate files are removed on exit;
# <out-dir> gets places.db and data-version (the extract date).
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <geofabrik-path> <out-dir>" >&2
  exit 2
fi
region_path=$1
out_dir=$2

if [[ ! $region_path =~ ^[a-z0-9-]+(/[a-z0-9-]+)*$ ]]; then
  echo "invalid Geofabrik path: $region_path" >&2
  exit 2
fi
region=${region_path##*/}

repo=$(cd "$(dirname "$0")/.." && pwd)
mkdir -p "$out_dir"
work=$(mktemp -d "${out_dir%/}/.build.XXXXXX")
trap 'rm -rf "$work"' EXIT

echo "==> building opl"
(cd "$repo" && CGO_ENABLED=0 go build -o "$work/opl" ./cmd/opl)

echo "==> downloading $region_path"
curl --fail --location --retry 3 --silent --show-error \
  -o "$work/region.osm.pbf" "https://download.geofabrik.de/${region_path}-latest.osm.pbf"

timestamp=$(osmium fileinfo -g header.option.osmosis_replication_timestamp "$work/region.osm.pbf")
data_version=${timestamp%%T*}
if [[ ! $data_version =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]]; then
  echo "could not read the extract date (got '$timestamp')" >&2
  exit 1
fi

echo "==> filtering tags"
"$work/opl" filter-expressions > "$work/filters.txt"
osmium tags-filter --expressions="$work/filters.txt" \
  -o "$work/filtered.osm.pbf" "$work/region.osm.pbf"

echo "==> extracting public transport routes"
# Only the relations themselves: the importer needs the ids of their
# members, not the members.
"$work/opl" filter-expressions --routes > "$work/route-filters.txt"
osmium tags-filter --omit-referenced --expressions="$work/route-filters.txt" \
  -o "$work/routes.osm.gz" "$work/region.osm.pbf"
rm "$work/region.osm.pbf"

echo "==> exporting geometries"
# Points and areas only: closed ways would otherwise also be exported as
# lines. The importer expects one feature per line without the RS prefix.
osmium export --geometry-types=point,polygon --attributes=type,id \
  -f geojsonseq -x print_record_separator=false \
  -o "$work/places.geojsonseq" "$work/filtered.osm.pbf"

echo "==> importing"
"$work/opl" import --in "$work/places.geojsonseq" --routes "$work/routes.osm.gz" \
  --out "$out_dir/places.db" --region "$region" --data-version "$data_version"

echo "$data_version" > "$out_dir/data-version"
echo "==> done: $out_dir/places.db ($data_version)"
