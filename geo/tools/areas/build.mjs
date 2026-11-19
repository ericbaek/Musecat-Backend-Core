// Builds geo/data/admin_areas.geojson.gz from the pinned sources in
// sources.json. See README.md for the output contract and per-country rules.
//
//   node build.mjs --raw <download dir> --geonames <cities500.zip> [--out <file>]
//
// The raw directory holds each source at its `file` path; hashes are checked
// before anything is read so a build is reproducible from the manifest.
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import zlib from "node:zlib";

const here = path.dirname(fileURLToPath(import.meta.url));
const args = Object.fromEntries(
  process.argv
    .slice(2)
    .reduce((pairs, value, index, all) => {
      if (value.startsWith("--")) pairs.push([value.slice(2), all[index + 1]]);
      return pairs;
    }, []),
);
const rawDir = path.resolve(args.raw ?? "");
const geonamesZip = path.resolve(args.geonames ?? "");
const outFile = path.resolve(
  args.out ?? path.join(here, "../../data/admin_areas.geojson.gz"),
);
const workDir = path.resolve(args.work ?? path.join(rawDir, "../work"));
fs.mkdirSync(workDir, { recursive: true });

const manifest = JSON.parse(
  fs.readFileSync(path.join(here, "sources.json"), "utf8"),
);
const rules = JSON.parse(fs.readFileSync(path.join(here, "rules.json"), "utf8"));
const mapshaper = path.join(here, "node_modules/.bin/mapshaper");

function verifySources() {
  for (const source of manifest.sources) {
    const file = path.join(rawDir, source.file);
    const hash = createHash("sha256").update(fs.readFileSync(file)).digest("hex");
    if (hash !== source.sha256) {
      throw new Error(`${source.id}: sha256 ${hash} != ${source.sha256}`);
    }
  }
}

function run(...commands) {
  execFileSync(
    process.execPath,
    ["--max-old-space-size=12288", mapshaper, ...commands],
    { stdio: ["ignore", "ignore", "inherit"] },
  );
}

const readJSON = (file) => JSON.parse(fs.readFileSync(file, "utf8"));
const work = (name) => path.join(workDir, name);
const precisionFor = (country) => `precision=${rules.precision[country] ?? rules.precision.default}`;

// --- geometry helpers ------------------------------------------------------

function ringContains(ring, x, y) {
  let inside = false;
  for (let i = 0, j = ring.length - 1; i < ring.length; j = i++) {
    const [xi, yi] = ring[i];
    const [xj, yj] = ring[j];
    if (yi > y !== yj > y && x < ((xj - xi) * (y - yi)) / (yj - yi) + xi) {
      inside = !inside;
    }
  }
  return inside;
}

const polygonsOf = (geometry) =>
  !geometry
    ? []
    : geometry.type === "Polygon"
      ? [geometry.coordinates]
      : geometry.type === "MultiPolygon"
        ? geometry.coordinates
        : [];

function bboxOf(geometry) {
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

function contains(feature, x, y) {
  const [minX, minY, maxX, maxY] = feature.bbox;
  if (x < minX || x > maxX || y < minY || y > maxY) return false;
  return polygonsOf(feature.geometry).some(
    ([outer, ...holes]) =>
      ringContains(outer, x, y) && !holes.some((hole) => ringContains(hole, x, y)),
  );
}

// --- Natural Earth admin-1 (worldwide fallback and ISO labels) -------------

function loadNaturalEarth() {
  let shp = path.join(rawDir, "ne/ne_10m_admin_1_states_provinces.shp");
  if (!fs.existsSync(shp)) shp = path.join(workDir, "ne/ne_10m_admin_1_states_provinces.shp");
  if (!fs.existsSync(shp)) {
    execFileSync("unzip", ["-o", "-q", path.join(rawDir, "ne/ne1.zip"), "-d", path.join(workDir, "ne")]);
  }
  run(
    "-i", shp, "encoding=utf8",
    "-each", "iso=iso_3166_2, cc=iso_a2, nm=name, rg=region, adm=adm1_code, ix=this.innerX, iy=this.innerY",
    "-filter-fields", "iso,cc,nm,rg,adm,ix,iy",
    "-simplify", `interval=${rules.simplifyMeters.NE}`, "keep-shapes",
    "-o", work("ne.geojson"), precisionFor("NE"),
  );
  const features = readJSON(work("ne.geojson")).features;
  for (const feature of features) feature.bbox = bboxOf(feature.geometry);
  return features;
}

function admin1CodeOf(neFeature) {
  const { iso, cc, adm } = neFeature.properties;
  if (rules.wholeCountry.includes(cc)) return cc;
  return /^[A-Z]{2}-[A-Z0-9]{1,3}$/.test(iso ?? "") ? iso : `${cc}-${adm}`;
}

// --- per-country admin-2 sources -------------------------------------------

function koreaUnits() {
  const source = path.join(rawDir, "kr/HangJeongDong_ver20261001.geojson");
  run(
    "-i", source,
    "-dissolve", "sgg", "copy-fields=sggnm,sido,sidonm",
    "-filter-islands", `min-area=${rules.minIslandKm2}km2`, "remove-empty",
    "-simplify", `interval=${rules.simplifyMeters.KR}`, "keep-shapes",
    "-o", work("KR.geojson"), precisionFor("KR"),
  );
  const kr = rules.countries.KR;
  return readJSON(work("KR.geojson")).features.map((feature) => {
    const { sgg, sggnm, sido, sidonm } = feature.properties;
    const admin1 = kr.sidoToAdmin1[sido];
    if (!admin1) throw new Error(`KR: unknown sido ${sido}`);
    let city = `KR:${sgg}`;
    let cityName = sggnm;
    const general = sggnm.match(/^(.+?시)(.+구)$/);
    if (kr.metropolitanSido.includes(sido)) {
      city = `KR:${sido}`;
      cityName = sidonm;
    } else if (kr.metropolitanGuInSido[sido] && /구$/.test(sggnm) && !/시/.test(sggnm)) {
      cityName = kr.metropolitanGuInSido[sido];
      city = `KR:${sido}:${cityName}`;
    } else if (general) {
      cityName = general[1];
      city = `KR:${sido}:${cityName}`;
    }
    return { country: "KR", admin1, admin2: sgg, name: sggnm, city, cityName, geometry: feature.geometry };
  });
}

function japanUnits() {
  let shp = path.join(rawDir, "n03/N03-20250101.shp");
  if (!fs.existsSync(shp)) shp = path.join(workDir, "n03/N03-20250101.shp");
  if (!fs.existsSync(shp)) {
    execFileSync("unzip", ["-o", "-q", path.join(rawDir, "N03-20250101_GML.zip"),
      "N03-20250101.shp", "N03-20250101.shx", "N03-20250101.dbf", "N03-20250101.prj", "N03-20250101.cpg",
      "-d", path.join(workDir, "n03")]);
  }
  run(
    "-i", shp, "encoding=utf8",
    "-filter", "N03_007 != null && N03_007 !== ''",
    "-dissolve", "N03_007", "copy-fields=N03_004,N03_005",
    "-filter-islands", `min-area=${rules.minIslandKm2}km2`, "remove-empty",
    "-simplify", `interval=${rules.simplifyMeters.JP}`, "keep-shapes",
    "-o", work("JP.geojson"), precisionFor("JP"),
  );
  const jp = rules.countries.JP;
  return readJSON(work("JP.geojson")).features.map((feature) => {
    const { N03_007: code, N03_004: city, N03_005: ward } = feature.properties;
    const name = `${city ?? ""}${ward ?? ""}`;
    let key = `JP:${code}`;
    let cityName = city ?? name;
    if (ward) key = `JP:${code.slice(0, 2)}:${city}`; // 政令指定都市の区 → 市
    else if (code >= jp.tokyoSpecialWards[0] && code <= jp.tokyoSpecialWards[1]) {
      key = "JP:13:tokyo-23";
      cityName = jp.tokyoSpecialWardsName;
    }
    return { country: "JP", admin1: `JP-${code.slice(0, 2)}`, admin2: code, name, city: key, cityName, geometry: feature.geometry };
  });
}

function geoBoundariesUnits(country, ne) {
  const config = rules.countries[country];
  const file = path.join(rawDir, `gb/${config.iso3}-ADM2.geojson`);
  run(
    "-i", file,
    "-each", "ix=this.innerX, iy=this.innerY",
    "-filter-islands", `min-area=${rules.minIslandKm2}km2`, "remove-empty",
    "-simplify", `interval=${rules.simplifyMeters[country] ?? rules.simplifyMeters.default}`, "keep-shapes",
    "-o", work(`${country}.geojson`), precisionFor(country),
  );
  const parents = ne.filter((feature) => feature.properties.cc === country);
  const foreign = ne.filter((feature) => feature.properties.cc !== country);
  const units = [];
  for (const feature of readJSON(work(`${country}.geojson`)).features) {
    const { shapeID, shapeName, ix, iy } = feature.properties;
    // CHN ADM2 also draws Taiwan, Hong Kong and Macau; those come from their
    // own sources, so drop any unit whose interior lies in another country.
    if (foreign.some((candidate) => contains(candidate, ix, iy))) continue;
    const parent =
      parents.find((candidate) => contains(candidate, ix, iy)) ??
      nearestByInnerPoint(parents, ix, iy);
    const admin1 = parent ? admin1CodeOf(parent) : country;
    const regionCity = config.cityByRegion?.[parent?.properties.rg];
    let city = `${country}:${shapeID}`;
    let cityName = shapeName;
    if (config.cityIsAdmin1?.includes(admin1)) {
      city = `${country}:${admin1}`;
      cityName = parent.properties.nm;
    } else if (regionCity) {
      city = `${country}:region:${regionCity}`;
      cityName = regionCity;
    }
    units.push({ country, admin1, admin2: shapeID, name: shapeName, city, cityName, geometry: feature.geometry });
  }
  return units;
}

function nearestByInnerPoint(features, x, y) {
  let best;
  let bestDistance = Infinity;
  for (const feature of features) {
    const { ix, iy } = feature.properties;
    const distance = Math.hypot(ix - x, iy - y);
    if (distance < bestDistance) {
      bestDistance = distance;
      best = feature;
    }
  }
  return best;
}

function naturalEarthUnits(ne, covered) {
  const units = [];
  const whole = new Map();
  for (const feature of ne) {
    const { cc, nm } = feature.properties;
    if (!/^[A-Z]{2}$/.test(cc ?? "") || covered.has(cc)) continue;
    const admin1 = admin1CodeOf(feature);
    if (rules.wholeCountry.includes(cc)) {
      // One admin-1 and one city for the whole territory (HK, MO, SG …).
      const merged = whole.get(cc) ?? { country: cc, admin1, admin2: "", name: cc, city: `${cc}:all`, cityName: rules.wholeCountryNames[cc] ?? cc, geometry: { type: "MultiPolygon", coordinates: [] } };
      merged.geometry.coordinates.push(...polygonsOf(feature.geometry));
      whole.set(cc, merged);
      continue;
    }
    const city = rules.admin1IsCity.includes(cc) ? `${cc}:${admin1}` : "";
    units.push({ country: cc, admin1, admin2: "", name: nm, city, cityName: city ? nm : "", geometry: feature.geometry });
  }
  return [...units, ...whole.values()];
}

// --- GeoNames city links ---------------------------------------------------
//
// A city links to a GeoNames row only when the row clearly is that city: a row
// whose local or alternate name matches the city name near the city, or an
// administrative seat (PPLC/PPLA/PPLA2) inside it. A plain populated place
// inside is not enough, since GeoNames points drift across borders (its
// Uiwang point lies in Gunpo). The link only supplies translated names and a
// marker point; the city itself is the boundary union, linked or not.

const SEAT_CODES = new Set(["PPLC", "PPLA", "PPLA2"]);
// Fallback cities for countries without boundaries: capitals and admin-1
// seats only; PPLA2 would add every French commune seat.
const SEAT_CITY_CODES = new Set(["PPLC", "PPLA"]);
const CITY_CODES = new Set(["PPL", "PPLC", "PPLA", "PPLA2", "PPLA3", "PPLA4", "PPLA5"]);
const NAME_MATCH_DEGREES = 0.3;

const SUFFIX = /(특별자치시|특별자치도|특별시|광역시|시|군|구|都|道|府|県|市|町|村|区|City|County|Shire|Municipality|Regency|District|Province|省|市辖区|地区|自治州|盟|縣|縣市)$/iu;
function normalizeName(name) {
  // Accents are dropped (Hồ Chí Minh = Ho Chi Minh); NFC restores Hangul and kana.
  let value = String(name ?? "").normalize("NFKD").replace(/\p{M}/gu, "").replace(/đ/gi, "d").normalize("NFC").trim();
  for (let i = 0; i < 2; i++) {
    const stripped = value.replace(SUFFIX, "").trim();
    if (stripped.length >= 2 || /[^\x00-\x7F]/.test(stripped)) value = stripped;
  }
  return value.toLowerCase().replace(/[\s'’`-]/g, "");
}

function loadGeoNames() {
  const text = execFileSync("unzip", ["-p", geonamesZip, "cities500.txt"], {
    maxBuffer: 1 << 30,
  }).toString("utf8");
  const byCountry = new Map();
  for (const line of text.split("\n")) {
    const f = line.split("\t");
    if (f.length !== 19 || f[6] !== "P" || !CITY_CODES.has(f[7])) continue;
    const alternates = f[3] ? f[3].split(",") : [];
    const names = new Set([f[1], f[2], ...alternates].map(normalizeName).filter(Boolean));
    const row = { id: f[0], name: f[1], code: f[7], x: Number(f[5]), y: Number(f[4]), population: Number(f[14]) || 0, names, alternates };
    const list = byCountry.get(f[8]) ?? [];
    list.push(row);
    byCountry.set(f[8], list);
  }
  return byCountry;
}

const byPopulation = (top, row) => (!top || row.population > top.population ? row : top);

function linkCities(units, geonames) {
  const groups = new Map();
  for (const unit of units) {
    if (!unit.city) continue;
    const group = groups.get(unit.city) ?? [];
    group.push(unit);
    groups.set(unit.city, group);
  }
  const unlinked = [];
  const claimed = new Map();
  const fallback = new Map();
  for (const [key, members] of groups) {
    const country = members[0].country;
    const target = normalizeName(members[0].cityName);
    const [minX, minY, maxX, maxY] = members
      .map((member) => member.bbox)
      .reduce((a, b) => [Math.min(a[0], b[0]), Math.min(a[1], b[1]), Math.max(a[2], b[2]), Math.max(a[3], b[3])]);
    const rows = geonames.get(country) ?? [];
    const near = rows.filter(
      (row) =>
        row.x >= minX - NAME_MATCH_DEGREES && row.x <= maxX + NAME_MATCH_DEGREES &&
        row.y >= minY - NAME_MATCH_DEGREES && row.y <= maxY + NAME_MATCH_DEGREES,
    );
    const inside = near.filter((row) => members.some((member) => contains(member, row.x, row.y)));
    const best =
      (target && near.filter((row) => row.names.has(target)).reduce(byPopulation, undefined)) ||
      inside.filter((row) => SEAT_CODES.has(row.code)).reduce(byPopulation, undefined);
    for (const member of members) member.citySourceId = best?.id ?? "";
    if (best) {
      const others = claimed.get(best.id) ?? [];
      others.push(key);
      claimed.set(best.id, others);
    } else fallback.set(key, inside);
  }
  // Romanised-only rows (GeoNames has no 통영 for Tongyeong) fall back to the
  // most populous place inside that no other city claimed by name or seat.
  for (const [key, inside] of fallback) {
    const best = inside.filter((row) => !claimed.has(row.id)).reduce(byPopulation, undefined);
    if (!best) {
      unlinked.push(key);
      continue;
    }
    for (const member of groups.get(key)) member.citySourceId = best.id;
    claimed.set(best.id, [key]);
  }
  // Two cities must never share one Passport row; keep the link only for the
  // city whose name matches, else drop it from all of them.
  const shared = [...claimed].filter(([, keys]) => keys.length > 1);
  for (const [id, keys] of shared) {
    const row = [...geonames.values()].flat().find((candidate) => candidate.id === id);
    const keep = keys.filter((key) => row.names.has(normalizeName(groups.get(key)[0].cityName)));
    for (const key of keys) {
      if (keep.length === 1 && keep[0] === key) continue;
      for (const member of groups.get(key)) member.citySourceId = "";
      unlinked.push(key);
    }
  }
  return { unlinked, shared: shared.length };
}

// --- city catalog --------------------------------------------------------

const HANGUL = /\p{Script=Hangul}/u;
const KANA = /[\p{Script=Hiragana}\p{Script=Katakana}]/u;
const HAN_ONLY = /^[\p{Script=Han}]+$/u;
const HAN_COUNTRIES = new Set(["CN", "TW", "HK", "MO", "JP"]);
const shortest = (values) => values.reduce((a, b) => (!a || b.length < a.length ? b : a), "");
const longest = (values) => values.reduce((a, b) => (b.length > a.length ? b : a), "");

/**
 * GeoNames alternates carry no language tag. For Hangul, the longest spaceless
 * name is the transliteration (상하이 over 상해, 요코하마 over 요코하마 시);
 * for Han, the shortest two-plus character name skips one-glyph abbreviations
 * (上海 over 沪) and administrative suffixes.
 */
function pickHangul(values) {
  const plain = values.map((name) => name.replace(/\s+/g, "")).filter((name) => name.length >= 2);
  const bare = plain.filter((name) => !(/시$/.test(name) && plain.includes(name.slice(0, -1))));
  return longest(bare);
}
const pickHan = (values) => shortest(values.filter((name) => name.length >= 2));
const round = (value) => Math.round(value * 1e5) / 1e5;

/** Display names: the local name from the boundary source, then GeoNames. */
function namesOf(country, local, row) {
  const alternates = row?.alternates ?? [];
  const hangul = alternates.filter((name) => HANGUL.test(name));
  const kana = alternates.filter((name) => KANA.test(name));
  const han = alternates.filter((name) => HAN_ONLY.test(name));
  const names = { local };
  const en = row?.name || (/^[\p{Script=Latin}\p{N}\s.'’()-]+$/u.test(local) ? local : "");
  if (en) names.en = en;
  const ko = country === "KR" ? local.replace(/(특별자치시|특별시|광역시)$/, "") : pickHangul(hangul);
  if (ko) names.ko = ko;
  const ja =
    country === "JP" ? local : HAN_COUNTRIES.has(country) ? pickHan(han) || shortest(kana) : shortest(kana);
  if (ja) names.ja = ja;
  return names;
}

/** A point inside the unit for map markers: centroid when inside, else a grid probe. */
function interiorPoint(unit) {
  const [minX, minY, maxX, maxY] = unit.bbox;
  const cx = (minX + maxX) / 2;
  const cy = (minY + maxY) / 2;
  if (contains(unit, cx, cy)) return [cx, cy];
  let best;
  let bestDistance = Infinity;
  for (let i = 1; i < 24; i++) {
    for (let j = 1; j < 24; j++) {
      const x = minX + ((maxX - minX) * i) / 24;
      const y = minY + ((maxY - minY) * j) / 24;
      const distance = Math.hypot(x - cx, y - cy);
      if (distance < bestDistance && contains(unit, x, y)) {
        bestDistance = distance;
        best = [x, y];
      }
    }
  }
  return best ?? polygonsOf(unit.geometry)[0][0][0];
}

const areaOf = ([minX, minY, maxX, maxY]) => (maxX - minX) * (maxY - minY);

function buildCityCatalog(units, rowsById) {
  const groups = new Map();
  for (const unit of units) {
    if (!unit.city) continue;
    const group = groups.get(unit.city) ?? [];
    group.push(unit);
    groups.set(unit.city, group);
  }
  const catalog = {};
  for (const [key, members] of groups) {
    const largest = members.reduce((a, b) => (areaOf(b.bbox) > areaOf(a.bbox) ? b : a));
    const row = rowsById.get(members[0].citySourceId);
    const [x, y] = row && members.some((member) => contains(member, row.x, row.y)) ? [row.x, row.y] : interiorPoint(largest);
    const home = members.find((member) => contains(member, x, y)) ?? largest;
    catalog[key] = {
      country: largest.country,
      admin1: home.admin1,
      names: namesOf(largest.country, members[0].cityName, row),
      lat: round(y),
      lon: round(x),
      geonames_id: row?.id ?? "",
    };
  }
  return catalog;
}

/**
 * Countries with only Natural Earth admin-1 have no city boundaries. Their
 * cities are GeoNames seats and large towns; the server takes the nearest one
 * in the arcade's country and marks the result estimated.
 */
function buildSeatCatalog(ne, covered, geonames) {
  const catalog = {};
  const neByCountry = new Map();
  for (const feature of ne) {
    const list = neByCountry.get(feature.properties.cc) ?? [];
    list.push(feature);
    neByCountry.set(feature.properties.cc, list);
  }
  for (const [country, rows] of geonames) {
    if (covered.has(country) || rules.wholeCountry.includes(country)) continue;
    const regions = neByCountry.get(country) ?? [];
    for (const row of rows) {
      if (!SEAT_CITY_CODES.has(row.code) && row.population < rules.seatMinPopulation) continue;
      const region = regions.find((feature) => contains(feature, row.x, row.y));
      catalog[`GN:${row.id}`] = {
        country,
        admin1: region ? admin1CodeOf(region) : "",
        names: namesOf(country, row.name, row),
        lat: round(row.y),
        lon: round(row.x),
        geonames_id: row.id,
        estimated: true,
      };
    }
  }
  return catalog;
}

// --- main ------------------------------------------------------------------

verifySources();
const ne = loadNaturalEarth();
const units = [...koreaUnits(), ...japanUnits()];
for (const country of Object.keys(rules.countries)) {
  if (rules.countries[country].iso3) units.push(...geoBoundariesUnits(country, ne));
}
const covered = new Set(units.map((unit) => unit.country));
units.push(...naturalEarthUnits(ne, covered));
for (const unit of units) unit.bbox = bboxOf(unit.geometry);

const geonames = loadGeoNames();
const { unlinked, shared } = linkCities(units, geonames);
const rowsById = new Map([...geonames.values()].flat().map((row) => [row.id, row]));
const cities = buildCityCatalog(units, rowsById);
const seats = buildSeatCatalog(ne, covered, geonames);

const collection = {
  type: "FeatureCollection",
  // Places classified by country code alone: one admin-1 and one city each.
  whole_country: rules.wholeCountry,
  // Passport cities by key. Boundary cities are unions of the features that
  // name them; seat cities (estimated) cover countries without city rules.
  cities: { ...cities, ...seats },
  features: units.map((unit) => ({
    type: "Feature",
    properties: {
      country: unit.country,
      admin1: unit.admin1,
      admin2: unit.admin2,
      name: unit.name,
      city: unit.city,
    },
    geometry: unit.geometry,
  })),
};
const json = Buffer.from(JSON.stringify(collection));
fs.writeFileSync(outFile, zlib.gzipSync(json, { level: 9 }));

const perCountry = {};
for (const unit of units) {
  const row = (perCountry[unit.country] ??= { units: 0, admin1: new Set(), cities: new Set(), unlinked: 0 });
  row.units += 1;
  row.admin1.add(unit.admin1);
  if (unit.city) row.cities.add(unit.city);
  if (unit.city && !unit.citySourceId) row.unlinked += 1;
}
const report = Object.fromEntries(
  Object.entries(perCountry).map(([cc, row]) => [cc, { units: row.units, admin1: row.admin1.size, cities: row.cities.size, unlinkedUnits: row.unlinked }]),
);
fs.writeFileSync(work("report.json"), JSON.stringify({ shared, unlinked, report }, null, 2));
console.log(`wrote ${outFile} (${(fs.statSync(outFile).size / 1e6).toFixed(1)} MB gz, ${(json.length / 1e6).toFixed(1)} MB raw), ${units.length} units, ${unlinked.length} unlinked city groups`);
