#!/bin/zsh
# Projects, dissolves and simplifies each country's admin-1 groups.
set -euo pipefail
S="${1:?usage: run.sh <work dir>}"
MS=$S/tools/node_modules/.bin/mapshaper
W=$S/work
# country | projection | map interval (m) | map min island km2 | boundary interval (m)
CONFIG=(
  "MY|+proj=tmerc +lon_0=109 +lat_0=4|1500|40|700"
  "ID|+proj=merc +lon_0=118|4000|300|1500"
  "PH|+proj=tmerc +lon_0=122 +lat_0=12|1500|60|700"
  "SG|+proj=tmerc +lon_0=103.82 +lat_0=1.35|60|2|60"
  "VN|+proj=tmerc +lon_0=106 +lat_0=16|1500|40|700"
  "TW|+proj=tmerc +lon_0=121 +lat_0=23.7|400|10|300"
  "AU|+proj=aea +lat_1=-18 +lat_2=-36 +lon_0=134|5000|300|2000"
  "US|albersusa|6000|300|2000"
  "TH|+proj=tmerc +lon_0=101 +lat_0=13|1500|40|700"
)
for row in $CONFIG; do
  IFS='|' read -r cc proj interval island binterval <<< "$row"
  $MS -i $W/$cc.geojson -dissolve slug \
    -filter-islands min-area=${island}km2 remove-empty \
    -proj "$proj" -simplify interval=$interval keep-shapes \
    -o $W/$cc.map.geojson precision=0.1 \
    -points inner -o $W/$cc.labels.geojson precision=0.1 2>&1 | grep -v Wrote || true
  $MS -i $W/$cc.geojson -dissolve slug \
    -filter-islands min-area=3km2 remove-empty \
    -simplify interval=$binterval keep-shapes \
    -o $W/$cc.bounds.geojson precision=0.001 2>&1 | grep -v Wrote || true
done
ls -la $W/*.map.geojson $W/*.bounds.geojson
