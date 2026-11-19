import { classifyArcadeRegion, arcadeDistrict, arcadeRegions } from "./web/services/arcade/region/arcadeRegions";
const all = await Bun.file(process.argv[2]).json();
const c = new Map<string, number>(); const miss: string[] = [];
for (const a of all) { const r = classifyArcadeRegion(a.address); if (!r) { miss.push(a.address); continue; } c.set(r.slug, (c.get(r.slug) ?? 0) + 1); }
for (const r of arcadeRegions) console.log(r.names.kr, c.get(r.slug) ?? 0);
console.log("unclassified", miss);
const seoulNoDistrict = all.filter((a:any)=>classifyArcadeRegion(a.address)?.slug==="seoul" && !arcadeDistrict(a.address)).map((a:any)=>a.address);
console.log("seoul w/o district", seoulNoDistrict);
