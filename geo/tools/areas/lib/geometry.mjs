// Planar helpers on GeoJSON lon/lat coordinates. Good enough for containment
// tests and marker placement; distances are not used here.

export const polygonsOf = (geometry) =>
  !geometry
    ? []
    : geometry.type === "Polygon"
      ? [geometry.coordinates]
      : geometry.type === "MultiPolygon"
        ? geometry.coordinates
        : [];

export function bboxOf(geometry) {
  let minX = Infinity;
  let minY = Infinity;
  let maxX = -Infinity;
  let maxY = -Infinity;
  for (const polygon of polygonsOf(geometry)) {
    for (const [x, y] of polygon[0] ?? []) {
      if (x < minX) minX = x;
      if (x > maxX) maxX = x;
      if (y < minY) minY = y;
      if (y > maxY) maxY = y;
    }
  }
  return [minX, minY, maxX, maxY];
}

function ringContains(ring, x, y) {
  let inside = false;
  for (let i = 0, j = ring.length - 1; i < ring.length; j = i++) {
    const [xi, yi] = ring[i];
    const [xj, yj] = ring[j];
    if (yi > y !== yj > y && x < ((xj - xi) * (y - yi)) / (yj - yi) + xi) inside = !inside;
  }
  return inside;
}

/** Feature must carry `bbox` (set by bboxOf) for the fast reject. */
export function contains(feature, x, y) {
  const [minX, minY, maxX, maxY] = feature.bbox;
  if (x < minX || x > maxX || y < minY || y > maxY) return false;
  return polygonsOf(feature.geometry).some(
    ([outer, ...holes]) => ringContains(outer, x, y) && !holes.some((hole) => ringContains(hole, x, y)),
  );
}

export const areaOf = ([minX, minY, maxX, maxY]) => (maxX - minX) * (maxY - minY);

/** A point inside the feature for map markers: bbox centre when inside, else a grid probe. */
export function interiorPoint(feature) {
  const [minX, minY, maxX, maxY] = feature.bbox;
  const cx = (minX + maxX) / 2;
  const cy = (minY + maxY) / 2;
  if (contains(feature, cx, cy)) return [cx, cy];
  let best;
  let bestDistance = Infinity;
  for (let i = 1; i < 24; i++) {
    for (let j = 1; j < 24; j++) {
      const x = minX + ((maxX - minX) * i) / 24;
      const y = minY + ((maxY - minY) * j) / 24;
      const distance = Math.hypot(x - cx, y - cy);
      if (distance < bestDistance && contains(feature, x, y)) {
        bestDistance = distance;
        best = [x, y];
      }
    }
  }
  return best ?? polygonsOf(feature.geometry)[0][0][0];
}

export const round5 = (value) => Math.round(value * 1e5) / 1e5;
