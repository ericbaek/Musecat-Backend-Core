const svg = await Bun.file(process.argv[2]).text();
const vb = svg.match(/viewBox="([^"]+)"/)![1];
const re = /<path\b[^>]*>/g;
const out: { id: string; d: string; x: number; y: number }[] = [];
for (const m of svg.matchAll(re)) {
  const tag = m[0]; const id = tag.match(/ id="([^"]+)"/)![1]; const d = tag.match(/ d="([^"]+)"/)![1].trim();
  // largest ring centroid
  let best = { a: 0, x: 0, y: 0 };
  for (const ring of d.split(/(?=M)/)) {
    const n = ring.replace(/[MZ]/g, " ").trim().split(/\s+/).map(Number);
    const pts: [number, number][] = []; for (let i = 0; i + 1 < n.length; i += 2) pts.push([n[i], n[i + 1]]);
    let a = 0, cx = 0, cy = 0;
    for (let i = 0; i < pts.length; i++) { const [x0, y0] = pts[i]; const [x1, y1] = pts[(i + 1) % pts.length]; const f = x0 * y1 - x1 * y0; a += f; cx += (x0 + x1) * f; cy += (y0 + y1) * f; }
    if (Math.abs(a) > Math.abs(best.a)) best = { a, x: cx / (3 * a), y: cy / (3 * a) };
  }
  out.push({ id, d, x: Math.round(best.x), y: Math.round(best.y) });
}
console.log(JSON.stringify({ vb, regions: out.map(({ id, x, y, d }) => ({ id, x, y, len: d.length })) }));
await Bun.write(process.argv[3], JSON.stringify({ viewBox: vb, regions: out }));
