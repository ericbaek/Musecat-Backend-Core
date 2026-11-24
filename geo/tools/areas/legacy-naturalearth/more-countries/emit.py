"""Writes <Country>RegionMap.shapes.ts files and a combined preview page.

Usage: python3 -I emit.py <workdir> <outdir> <preview.html>
"""
import json
import math
import sys

work, outdir, preview = sys.argv[1], sys.argv[2], sys.argv[3]

COUNTRIES = {
    # code: (export prefix, file name, max width, max height, header note)
    "MY": ("malaysia", "Malaysia", 760, 760,
           "Malaysia only; Sabah, Sarawak and Labuan moved south-west as an\n// inset beside the peninsula and islands under 40km² omitted. Projected with a\n// Malaysia-centred transverse Mercator"),
    "ID": ("indonesia", "Indonesia", 900, 760,
           "Indonesia only; islands under 300km² omitted. Projected with an\n// equatorial Mercator"),
    "PH": ("philippines", "Philippines", 760, 900,
           "Philippines only; provinces and cities dissolved into the 18\n// administrative regions (Negros Island Region from 2024) and islands under\n// 60km² omitted. Projected with a Philippines-centred transverse Mercator"),
    "SG": ("singapore", "Singapore", 760, 600,
           "Singapore as one region (the five Community Development Council\n// districts dissolved), islands under 2km² omitted. Projected with a\n// Singapore-centred transverse Mercator"),
    "VN": ("vietnam", "Vietnam", 640, 900,
           "Vietnam only; the 63 former provinces dissolved into the 34 provinces\n// in force since 2025-07-01 and islands under 40km² omitted. Projected with a\n// Vietnam-centred transverse Mercator"),
    "TW": ("taiwan", "Taiwan", 700, 900,
           "Taiwan, Penghu and Kinmen; islands under 10km² omitted. Projected\n// with a Taiwan-centred transverse Mercator"),
    "AU": ("australia", "Australia", 820, 760,
           "Australia's eight states and territories; external territories and\n// islands under 300km² omitted. Projected with an Albers equal-area conic"),
    "US": ("unitedStates", "UnitedStates", 900, 600,
           "50 states and DC with Alaska and Hawaii as insets (mapshaper\n// albersusa); islands under 300km² omitted"),
    "TH": ("thailand", "Thailand", 600, 900,
           "Thailand only; the 77 provinces dissolved into Greater Bangkok\n// (Bangkok and its five neighbouring provinces) and Natural Earth's six\n// regions, islands under 40km² omitted. Projected with a Thailand-centred\n// transverse Mercator"),
}

INTERVALS = {"MY": 1500, "ID": 4000, "PH": 1500, "SG": 60, "VN": 1500, "TW": 400, "AU": 5000, "US": 6000, "TH": 1500}

# Projected-metre offsets applied before fitting.
INSETS = {
    "MY": {"slugs": {"sabah", "sarawak", "labuan"}, "dx": -760_000, "dy": -420_000},
}


def rings_of(geometry):
    if geometry["type"] == "Polygon":
        return [geometry["coordinates"]]
    return geometry["coordinates"]


def ring_area(ring):
    return abs(sum(x0 * y1 - x1 * y0 for (x0, y0), (x1, y1) in zip(ring, ring[1:] + ring[:1]))) / 2


def emit(cc):
    prefix, name, max_w, max_h, note = COUNTRIES[cc]
    features = json.load(open(f"{work}/{cc}.map.geojson"))["features"]
    labels = {
        f["properties"]["slug"]: f["geometry"]["coordinates"]
        for f in json.load(open(f"{work}/{cc}.labels.geojson"))["features"]
    }
    inset = INSETS.get(cc)
    shapes = {}
    for f in features:
        slug = f["properties"]["slug"]
        polys = rings_of(f["geometry"])
        label = labels[slug]
        if inset and slug in inset["slugs"]:
            dx, dy = inset["dx"], inset["dy"]
            polys = [[[[x + dx, y + dy] for x, y in ring] for ring in poly] for poly in polys]
            label = [label[0] + dx, label[1] + dy]
        shapes[slug] = {"polys": polys, "label": label}
    xs = [x for s in shapes.values() for p in s["polys"] for r in p for x, _ in r]
    ys = [y for s in shapes.values() for p in s["polys"] for r in p for _, y in r]
    minx, maxx, miny, maxy = min(xs), max(xs), min(ys), max(ys)
    pad = 6
    scale = min((max_w - 2 * pad) / (maxx - minx), (max_h - 2 * pad) / (maxy - miny))
    width = math.ceil((maxx - minx) * scale + 2 * pad)
    height = math.ceil((maxy - miny) * scale + 2 * pad)

    def pt(x, y):
        return (round((x - minx) * scale + pad, 1), round((maxy - y) * scale + pad, 1))

    def fmt(v):
        return ("%.1f" % v).rstrip("0").rstrip(".")

    out = {}
    for slug, shape in shapes.items():
        parts = []
        area = 0.0
        for poly in shape["polys"]:
            for index, ring in enumerate(poly):
                points = []
                for x, y in ring[:-1] if ring[0] == ring[-1] else ring:
                    p = pt(x, y)
                    if not points or points[-1] != p:
                        points.append(p)
                if len(points) < 3:
                    continue
                if index == 0:
                    area += ring_area(points)
                parts.append("M " + " ".join(f"{fmt(x)} {fmt(y)}" for x, y in points) + " Z")
        lx, ly = pt(*shape["label"])
        out[slug] = {"path": " ".join(parts), "label": (round(lx), round(ly)), "area": area}
    order = sorted(out, key=lambda s: -out[s]["area"])
    lines = [
        "// Generated from Natural Earth 1:10m admin-1 boundaries (public domain,",
        f"// https://www.naturalearthdata.com). {note}",
        f"// and simplified with mapshaper (interval={INTERVALS[cc]}m). Regenerate rather than",
        "// hand-editing paths.",
        "",
        f'export const {prefix}RegionMapViewBox = "0 0 {width} {height}";',
        "",
        f"export const {prefix}RegionShapes: Record<",
        "  string,",
        "  { path: string; label: { x: number; y: number } }",
        "> = {",
    ]
    for slug in sorted(out):
        key = slug if slug.replace("_", "").isalnum() and not slug[0].isdigit() else json.dumps(slug)
        lines += [
            f"  {key}: {{",
            f"    path: {json.dumps(out[slug]['path'])},",
            f"    label: {{ x: {out[slug]['label'][0]}, y: {out[slug]['label'][1]} }},",
            "  },",
        ]
    lines += [
        "};",
        "",
        "/** Paint order: larger regions first so enclosed or small ones stay clickable. */",
        f"export const {prefix}RegionPaintOrder = [",
        *[f"  {json.dumps(slug)}," for slug in order],
        "] as const;",
        "",
    ]
    with open(f"{outdir}/{name}RegionMap.shapes.ts", "w") as fh:
        fh.write("\n".join(lines))
    return width, height, out, order


svgs = []
for cc in COUNTRIES:
    width, height, out, order = emit(cc)
    body = "".join(
        f'<path d="{out[s]["path"]}" fill="#cfe0f5" stroke="#345" stroke-width="0.6"><title>{s}</title></path>'
        for s in order
    ) + "".join(
        f'<circle cx="{out[s]["label"][0]}" cy="{out[s]["label"][1]}" r="2" fill="red"/>'
        f'<text x="{out[s]["label"][0] + 3}" y="{out[s]["label"][1] - 2}" font-size="9">{s}</text>'
        for s in order
    )
    svgs.append(f'<h2>{cc} {width}x{height}</h2><svg viewBox="0 0 {width} {height}" width="{width}" height="{height}">{body}</svg>')
    print(cc, width, height, len(out))

with open(preview, "w") as fh:
    fh.write("<!doctype html><meta charset=utf-8><body style='font-family:sans-serif'>" + "".join(svgs))
