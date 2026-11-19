// Shipped catalogs: Passport cities with stable keys, GeoNames seat cities,
// admin-1 names and address aliases, plus the key lock and dataset version.
import { createHash } from "node:crypto";
import fs from "node:fs";
import { admin1CodeOf } from "./units.mjs";
import { areaOf, contains, interiorPoint, round5 } from "./geometry.mjs";
import { namesOf } from "./names.mjs";

/** Groups city units by their internal group id. */
export function groupCities(units) {
  const groups = new Map();
  for (const unit of units) {
    if (!unit.group) continue;
    const members = groups.get(unit.group) ?? [];
    members.push(unit);
    groups.set(unit.group, members);
  }
  return groups;
}

/**
 * Stable city keys: `<admin-1 or country>/<local name>`, e.g. KR-41/용인시,
 * JP-13/東京都区部, US-CA/Los Angeles, GB/London. Keys survive source
 * releases that renumber units; they change only when a city is renamed or
 * moves admin-1, which the key lock turns into an explicit rename.
 */
export function assignKeys(groups, rules) {
  const base = new Map();
  for (const [group, members] of groups) {
    const key = `${members[0].cityScope}/${members[0].cityName}`;
    base.set(key, [...(base.get(key) ?? []), group]);
  }
  const keys = new Map();
  for (const [key, list] of base) {
    if (list.length === 1) {
      keys.set(list[0], key);
      continue;
    }
    // Same name in one admin-1 (淇县 and 杞县 are both Qixian; Baltimore city
    // and county): suffix the coarsest marker position that tells them apart.
    for (const digits of [1, 2, 3]) {
      const tagged = list.map((group) => {
        const members = groups.get(group);
        const [x, y] = interiorPoint(members.reduce((a, b) => (areaOf(b.bbox) > areaOf(a.bbox) ? b : a)));
        return [group, `${key}~${y.toFixed(digits)},${x.toFixed(digits)}`];
      });
      if (new Set(tagged.map(([, value]) => value)).size === list.length) {
        for (const [group, value] of tagged) keys.set(group, value);
        break;
      }
    }
  }
  for (const [group, key] of Object.entries(rules.cityKeyOverrides ?? {})) {
    if (!keys.has(group)) throw new Error(`rules.cityKeyOverrides: unknown group ${group}`);
    keys.set(group, key);
  }
  const owners = new Map();
  for (const [group, key] of keys) owners.set(key, [...(owners.get(key) ?? []), group]);
  const missing = [...groups.keys()].filter((group) => !keys.has(group));
  const collisions = [...owners].filter(([, list]) => list.length > 1);
  if (missing.length || collisions.length) {
    const lines = [...missing.map((group) => `  ${group}`), ...collisions.map(([key, list]) => `  ${key}: ${list.join(", ")}`)].join("\n");
    throw new Error(`city key collisions; add rules.cityKeyOverrides for these groups:\n${lines}`);
  }
  return keys;
}

export function buildCityCatalog(groups, keys, rowsById, alternates, rules) {
  const catalog = {};
  for (const [group, members] of groups) {
    const key = keys.get(group);
    const largest = members.reduce((a, b) => (areaOf(b.bbox) > areaOf(a.bbox) ? b : a));
    const row = members.link ? rowsById.get(members.link) : undefined;
    const [x, y] = row && members.some((member) => contains(member, row.x, row.y)) ? [row.x, row.y] : interiorPoint(largest);
    const home = members.find((member) => contains(member, x, y)) ?? largest;
    catalog[key] = {
      country: largest.country,
      admin1: home.admin1,
      names: namesOf(largest.country, members[0].cityName, row, alternates.get(row?.id), rules.cityNames?.[key]),
      lat: round5(y),
      lon: round5(x),
      geonames_id: row?.id ?? "",
    };
  }
  return catalog;
}

// Fallback cities for countries without city boundaries: capitals and admin-1
// seats (PPLA2 would add every French commune seat) and large towns.
const SEAT_CITY_CODES = new Set(["PPLC", "PPLA"]);

const SEAT_SPACING_METERS = 10000;
const metersBetween = (a, b) => {
  const rad = Math.PI / 180;
  const dLat = (b.y - a.y) * rad;
  const dLon = (b.x - a.x) * rad;
  const h = Math.sin(dLat / 2) ** 2 + Math.cos(a.y * rad) * Math.cos(b.y * rad) * Math.sin(dLon / 2) ** 2;
  return 2 * 6371008.8 * Math.asin(Math.sqrt(h));
};

/**
 * Seat rows per country without city boundaries, keyed `GN:<geonames id>`.
 * Capitals and admin-1 seats always count; a large town counts only when no
 * bigger seat is within SEAT_SPACING_METERS, so Paris arrondissements (each a
 * 100k+ PPL row) fold into Paris.
 */
export function selectSeats(geonames, covered, rules) {
  const seats = [];
  for (const [country, rows] of geonames) {
    if (covered.has(country) || rules.wholeCountry.includes(country)) continue;
    const kept = rows.filter((row) => SEAT_CITY_CODES.has(row.code));
    const towns = rows
      .filter((row) => !SEAT_CITY_CODES.has(row.code) && row.population >= rules.seatMinPopulation)
      .sort((a, b) => b.population - a.population);
    for (const town of towns) {
      if (!kept.some((row) => metersBetween(row, town) < SEAT_SPACING_METERS)) kept.push(town);
    }
    for (const row of kept) seats.push({ country, row });
  }
  return seats;
}

/** The server takes the nearest seat in the arcade's country and marks it estimated. */
export function buildSeatCatalog(ctx, seats, ne, alternates) {
  const regionsByCountry = new Map();
  for (const feature of ne) {
    regionsByCountry.set(feature.properties.cc, [...(regionsByCountry.get(feature.properties.cc) ?? []), feature]);
  }
  const catalog = {};
  for (const { country, row } of seats) {
    const key = `GN:${row.id}`;
    const region = (regionsByCountry.get(country) ?? []).find((feature) => contains(feature, row.x, row.y));
    catalog[key] = {
      country,
      admin1: region ? admin1CodeOf(ctx, region) : "",
      names: namesOf(country, row.name, row, alternates.get(row.id), ctx.rules.cityNames?.[key]),
      lat: round5(row.y),
      lon: round5(row.x),
      geonames_id: row.id,
      estimated: true,
    };
  }
  return catalog;
}

// --- admin-1 names and address aliases ---------------------------------------

const ADMIN1_SUFFIX = /(특별자치도|특별자치시|특별시|광역시|도|都|府|県|省|市|自治区|壮族自治区|回族自治区|维吾尔自治区|特別行政區|特别行政区|縣)$/u;
const CJK = /[\p{Script=Hangul}\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}]/u;

/**
 * An alias is usable in an address check when it is long enough not to hit
 * ordinary words: two CJK characters, or four Latin letters (matched on word
 * boundaries by the server).
 */
const usableAlias = (alias) => (CJK.test(alias) ? alias.length >= 2 : alias.length >= 4);

function aliasesOf(values) {
  const out = new Set();
  for (const value of values) {
    for (const part of String(value ?? "").split("|")) {
      const alias = part.trim();
      if (!alias) continue;
      out.add(alias);
      // 경기도 → 경기, 東京都 → 東京, 广东省 → 广东; 北海道 keeps its 道.
      const bare = alias.replace(ADMIN1_SUFFIX, "");
      if (bare !== alias && CJK.test(bare)) out.add(bare);
    }
  }
  return [...out].filter(usableAlias);
}

/**
 * Admin-1 catalog: display names and the aliases an address may use for the
 * area. Aliases two admin-1 areas of one country share are dropped, so a match
 * always names one area.
 */
export function buildAdmin1Catalog(ctx, units, ne, cities) {
  const { rules } = ctx;
  const neByCode = new Map();
  for (const feature of ne) {
    const code = admin1CodeOf(ctx, feature);
    neByCode.set(code, [...(neByCode.get(code) ?? []), feature]);
  }
  const catalog = {};
  for (const unit of units) {
    if (catalog[unit.admin1]) continue;
    const sources = neByCode.get(unit.admin1) ?? [];
    const p = sources[0]?.properties ?? {};
    const local = unit.admin1Name ?? (rules.wholeCountry.includes(unit.country) ? rules.wholeCountryNames[unit.country] : p.n_local || p.nm) ?? unit.admin1;
    const names = { local };
    if (p.n_en || p.nm) names.en = p.n_en || p.nm;
    if (p.n_ko) names.ko = p.n_ko;
    if (p.n_ja) names.ja = p.n_ja;
    if (unit.country === "KR") names.ko = local;
    if (unit.country === "JP") names.ja = local;
    if (rules.wholeCountry.includes(unit.country)) {
      // HK, MO, SG: the admin-1 is the territory; district names are not aliases.
      catalog[unit.admin1] = { country: unit.country, names: { ...cities[`${unit.country}/${unit.cityName}`]?.names }, aliases: [] };
      continue;
    }
    const override = rules.admin1?.[unit.admin1] ?? {};
    Object.assign(names, override.names);
    const values = [local, ...Object.values(names), ...(override.aliases ?? [])];
    for (const { properties: q } of sources) {
      values.push(q.nm, q.n_en, q.n_ko, q.n_ja, q.n_zh, q.n_zht, q.n_local, q.n_alt, q.n_woe, q.n_gn);
    }
    const exclude = new Set((override.excludeAliases ?? []).map((alias) => alias.toLowerCase()));
    catalog[unit.admin1] = { country: unit.country, names, aliases: aliasesOf(values).filter((alias) => !exclude.has(alias.toLowerCase())) };
  }
  const owners = new Map();
  for (const [code, entry] of Object.entries(catalog)) {
    for (const alias of entry.aliases) {
      const id = `${entry.country}\u0000${alias.toLowerCase()}`;
      owners.set(id, [...(owners.get(id) ?? []), code]);
    }
  }
  for (const entry of Object.values(catalog)) {
    entry.aliases = entry.aliases.filter((alias) => owners.get(`${entry.country}\u0000${alias.toLowerCase()}`).length === 1).sort();
  }
  return catalog;
}

// --- key lock ------------------------------------------------------------------

/**
 * Keys are stored on arcades (manual pins, Passport history), so a build may
 * add keys but never silently drop one. A dropped key must be listed in
 * rules.keyRenames (old → new, shipped so the server remaps manual pins) or
 * rules.keyRetired.
 */
export function checkKeyLock(lockFile, cities, rules, { update }) {
  const next = Object.keys(cities).sort();
  const previous = fs.existsSync(lockFile) ? JSON.parse(fs.readFileSync(lockFile, "utf8")).keys : [];
  const present = new Set(next);
  const renames = rules.keyRenames ?? {};
  const retired = new Set(rules.keyRetired ?? []);
  for (const [from, to] of Object.entries(renames)) {
    if (!present.has(to)) throw new Error(`rules.keyRenames: ${from} → ${to}, but ${to} is not a city`);
  }
  const dropped = previous.filter((key) => !present.has(key) && !renames[key] && !retired.has(key));
  if (dropped.length) {
    const sample = dropped.slice(0, 40).map((key) => `  ${key}`).join("\n");
    throw new Error(
      `${dropped.length} city keys from keys.lock.json are gone. Map each to its new key in ` +
        `rules.keyRenames, or list it in rules.keyRetired:\n${sample}${dropped.length > 40 ? "\n  …" : ""}`,
    );
  }
  const added = next.filter((key) => !previous.includes(key));
  if (update || previous.length === 0 || added.length) {
    const kept = previous.filter((key) => !present.has(key)); // renamed or retired keys stay locked
    const keys = [...new Set([...next, ...kept])].sort();
    fs.writeFileSync(lockFile, `{\n  "keys": [\n${keys.map((key) => `    ${JSON.stringify(key)}`).join(",\n")}\n  ]\n}\n`);
  }
  return { added: added.length, removed: previous.filter((key) => !present.has(key)).length };
}

/** Content hash stored as `version`; the server recomputes rows with another. */
export function versionOf(collection) {
  return createHash("sha256").update(JSON.stringify(collection)).digest("hex").slice(0, 16);
}
