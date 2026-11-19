// Builds geo/data/admin_areas.geojson.gz from the pinned sources in
// sources.json. See README.md for the output contract and per-country rules.
//
//   npm run fetch                       # downloads into .raw/, checks hashes
//   npm run build [-- --raw <dir>] [--work <dir>] [--out <file>] [--map <file>] [--update-lock]
//
// Every source hash is checked before anything is read, so a build is
// reproducible from the manifest. keys.lock.json guards city keys (see
// lib/catalog.mjs checkKeyLock).
import { createHash } from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { pipeline } from "node:stream/promises";
import { fileURLToPath } from "node:url";
import zlib from "node:zlib";
import { bboxOf } from "./lib/geometry.mjs";
import { createContext, geoBoundariesUnits, japanUnits, koreaUnits, loadNaturalEarth, naturalEarthUnits } from "./lib/units.mjs";
import { linkCities, loadAlternateNames, loadGeoNames } from "./lib/names.mjs";
import { assignKeys, buildAdmin1Catalog, buildCityCatalog, buildSeatCatalog, checkKeyLock, groupCities, selectSeats, versionOf } from "./lib/catalog.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const argv = process.argv.slice(2);
const option = (name, fallback) => {
  const index = argv.indexOf(`--${name}`);
  return index >= 0 ? argv[index + 1] : fallback;
};
const rawDir = path.resolve(option("raw", path.join(here, ".raw")));
const workDir = path.resolve(option("work", path.join(here, ".work")));
const outFile = path.resolve(option("out", path.join(here, "../../data/admin_areas.geojson.gz")));
const mapFile = path.resolve(option("map", path.join(workDir, "admin1_map.geojson")));
const lockFile = path.join(here, "keys.lock.json");
fs.mkdirSync(workDir, { recursive: true });

const manifest = JSON.parse(fs.readFileSync(path.join(here, "sources.json"), "utf8"));
const rules = JSON.parse(fs.readFileSync(path.join(here, "rules.json"), "utf8"));
const sourceFile = (id) => path.join(rawDir, manifest.sources.find((source) => source.id === id).file);

async function verifySources() {
  for (const source of manifest.sources) {
    const file = path.join(rawDir, source.file);
    if (!fs.existsSync(file)) throw new Error(`${source.id}: missing ${file}; run npm run fetch`);
    const hash = createHash("sha256");
    await pipeline(fs.createReadStream(file), hash);
    const digest = hash.digest("hex");
    if (digest !== source.sha256) throw new Error(`${source.id}: sha256 ${digest} != ${source.sha256}; run npm run fetch`);
  }
}

const step = (label) => console.log(`[${new Date().toISOString().slice(11, 19)}] ${label}`);

await verifySources();
const ctx = createContext({ rawDir, workDir, rules, mapshaper: path.join(here, "node_modules/.bin/mapshaper") });

step("boundaries");
const ne = loadNaturalEarth(ctx);
const units = [...koreaUnits(ctx), ...japanUnits(ctx)];
for (const [country, config] of Object.entries(rules.countries)) {
  if (config.iso3) units.push(...geoBoundariesUnits(ctx, country, ne));
}
const covered = new Set(units.map((unit) => unit.country));
units.push(...naturalEarthUnits(ctx, ne, covered));
for (const unit of units) unit.bbox = bboxOf(unit.geometry);

step("cities");
const geonames = loadGeoNames(sourceFile("geonames-cities500"));
const groups = groupCities(units);
const keys = assignKeys(groups, rules);
const { rowsById, unlinked, shared } = linkCities(groups, geonames);
const seats = selectSeats(geonames, covered, rules);

step("names");
const ids = new Set([...[...groups.values()].map((members) => members.link).filter(Boolean), ...seats.map(({ row }) => row.id)]);
const alternates = await loadAlternateNames(sourceFile("geonames-alternatenames-v2"), ids);
const cities = { ...buildCityCatalog(groups, keys, rowsById, alternates, rules), ...buildSeatCatalog(ctx, seats, ne, alternates) };
const admin1 = buildAdmin1Catalog(ctx, units, ne, cities);
const lock = checkKeyLock(lockFile, cities, rules, { update: argv.includes("--update-lock") });

step("write");
const body = {
  type: "FeatureCollection",
  // Places classified by country code alone: one admin-1 and one city each.
  whole_country: rules.wholeCountry,
  // Retired city key → current key, for remapping stored manual pins.
  renames: rules.keyRenames ?? {},
  admin1,
  // Passport cities by key. Boundary cities are unions of the features that
  // name them; GN: seat cities (estimated) cover countries without city rules.
  cities,
  features: units.map((unit) => ({
    type: "Feature",
    properties: { country: unit.country, admin1: unit.admin1, admin2: unit.admin2, name: unit.name, city: unit.group ? keys.get(unit.group) : "" },
    geometry: unit.geometry,
  })),
};
const version = versionOf(body);
const json = Buffer.from(JSON.stringify({ version, ...body }));
fs.writeFileSync(outFile, zlib.gzipSync(json, { level: 9 }));

step("web map");
// Admin-1 outlines for the Web region map, from the same units.
const mapInput = path.join(workDir, "map-input.geojson");
fs.writeFileSync(mapInput, JSON.stringify({
  type: "FeatureCollection",
  features: units.map((unit) => ({ type: "Feature", properties: { country: unit.country, admin1: unit.admin1 }, geometry: unit.geometry })),
}));
ctx.run("-i", mapInput, "-dissolve", "admin1", "copy-fields=country", "-simplify", `interval=${rules.mapSimplifyMeters}`, "keep-shapes", "-o", mapFile, "precision=0.001");

const perCountry = {};
for (const unit of units) {
  const row = (perCountry[unit.country] ??= { units: 0, admin1: new Set(), cities: new Set() });
  row.units += 1;
  row.admin1.add(unit.admin1);
  if (unit.group) row.cities.add(unit.group);
}
const report = Object.fromEntries(Object.entries(perCountry).map(([cc, row]) => [cc, { units: row.units, admin1: row.admin1.size, cities: row.cities.size }]));
fs.writeFileSync(path.join(workDir, "report.json"), JSON.stringify({ version, shared, unlinked: unlinked.map((group) => keys.get(group)), report }, null, 2));
console.log(
  `wrote ${path.relative(process.cwd(), outFile)} version ${version}: ${(fs.statSync(outFile).size / 1e6).toFixed(1)} MB gz, ` +
    `${(json.length / 1e6).toFixed(1)} MB raw, ${units.length} units, ${Object.keys(cities).length} cities ` +
    `(${unlinked.length} without GeoNames link), ${Object.keys(admin1).length} admin-1; keys +${lock.added} -${lock.removed}`,
);
