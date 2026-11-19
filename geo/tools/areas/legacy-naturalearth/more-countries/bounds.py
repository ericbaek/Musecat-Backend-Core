"""Writes encoded lon/lat boundaries for the point fallback.

Usage: python3 -I bounds.py <workdir> <out.ts>
Encoding: polygons joined by ";", rings by "|", points "lon,lat" by " ".
"""
import json
import sys

work, out = sys.argv[1], sys.argv[2]
countries = ["MY", "ID", "PH", "VN", "TW", "AU", "US", "TH"]


def fmt(v):
    return ("%.3f" % v).rstrip("0").rstrip(".")


lines = [
    "// Generated from Natural Earth 1:10m admin-1 boundaries (public domain,",
    "// https://www.naturalearthdata.com), lon/lat, grouped like the region maps",
    "// and simplified with mapshaper (interval 500-5000m by country size, islands",
    "// under 10km² omitted). Server-side",
    "// fallback for arcades whose address has no recognisable region. Encoded as",
    '// polygons joined by ";", rings by "|" and "lon,lat" points by spaces to keep',
    "// the file compact. Regenerate rather than hand-editing.",
    "",
    "export const encodedRegionBoundaries: Record<",
    f"  {' | '.join(json.dumps(c) for c in countries)},",
    "  Record<string, string>",
    "> = {",
]
for cc in countries:
    features = json.load(open(f"{work}/{cc}.bounds.geojson"))["features"]
    lines.append(f"  {cc}: {{")
    for feature in sorted(features, key=lambda f: f["properties"]["slug"]):
        geometry = feature["geometry"]
        polys = [geometry["coordinates"]] if geometry["type"] == "Polygon" else geometry["coordinates"]
        encoded = ";".join(
            "|".join(" ".join(f"{fmt(x)},{fmt(y)}" for x, y in ring) for ring in poly) for poly in polys
        )
        slug = feature["properties"]["slug"]
        key = slug if slug.isalnum() else json.dumps(slug)
        lines.append(f"    {key}:")
        lines.append(f"      {json.dumps(encoded)},")
    lines.append("  },")
lines += ["};", ""]
with open(out, "w") as fh:
    fh.write("\n".join(lines))
