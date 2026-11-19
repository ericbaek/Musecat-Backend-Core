// GeoNames: populated places for linking cities, and language-tagged display
// names from alternateNamesV2.
import { execFileSync, spawn } from "node:child_process";
import readline from "node:readline";
import { contains } from "./geometry.mjs";

const CITY_CODES = new Set(["PPL", "PPLC", "PPLA", "PPLA2", "PPLA3", "PPLA4", "PPLA5"]);
const SEAT_CODES = new Set(["PPLC", "PPLA", "PPLA2"]);
const NAME_MATCH_DEGREES = 0.3;
export const LANGUAGES = ["en", "ko", "ja"];

const SUFFIX = /(특별자치시|특별자치도|특별시|광역시|시|군|구|都|道|府|県|市|町|村|区|City|County|Shire|Municipality|Regency|District|Province|省|市辖区|地区|自治州|盟|縣|縣市)$/iu;

/** Comparable form of a place name: no accents, suffix, case or spacing. */
export function normalizeName(name) {
  // Accents are dropped (Hồ Chí Minh = Ho Chi Minh); NFC restores Hangul and kana.
  let value = String(name ?? "").normalize("NFKD").replace(/\p{M}/gu, "").replace(/đ/gi, "d").normalize("NFC").trim();
  for (let i = 0; i < 2; i++) {
    const stripped = value.replace(SUFFIX, "").trim();
    if (stripped.length >= 2 || /[^\x00-\x7F]/.test(stripped)) value = stripped;
  }
  return value.toLowerCase().replace(/[\s'’`-]/g, "");
}

/** cities500 rows by country. */
export function loadGeoNames(zip) {
  const text = execFileSync("unzip", ["-p", zip, "cities500.txt"], { maxBuffer: 1 << 30 }).toString("utf8");
  const byCountry = new Map();
  for (const line of text.split("\n")) {
    const f = line.split("\t");
    if (f.length !== 19 || f[6] !== "P" || !CITY_CODES.has(f[7])) continue;
    const alternates = f[3] ? f[3].split(",") : [];
    const names = new Set([f[1], f[2], ...alternates].map(normalizeName).filter(Boolean));
    const row = { id: f[0], name: f[1], code: f[7], x: Number(f[5]), y: Number(f[4]), population: Number(f[14]) || 0, names, admin1: f[10], admin2: f[11] };
    const list = byCountry.get(f[8]) ?? [];
    list.push(row);
    byCountry.set(f[8], list);
  }
  return byCountry;
}

const byPopulation = (top, row) => (!top || row.population > top.population ? row : top);

/**
 * Links each city group to one GeoNames row, used only for display names and
 * the marker point. A row links when it clearly is that city: a name match
 * near the city, else an administrative seat inside it, else the most
 * populous place inside that no other city claimed (GeoNames has no 통영 for
 * Tongyeong). GeoNames points drift across borders (its Uiwang point lies in
 * Gunpo), and two cities never share a row.
 */
export function linkCities(groups, geonames) {
  const unlinked = [];
  const claimed = new Map();
  const fallback = new Map();
  const rowsById = new Map([...geonames.values()].flat().map((row) => [row.id, row]));
  for (const [group, members] of groups) {
    const country = members[0].country;
    const target = normalizeName(members[0].cityName);
    const [minX, minY, maxX, maxY] = members
      .map((member) => member.bbox)
      .reduce((a, b) => [Math.min(a[0], b[0]), Math.min(a[1], b[1]), Math.max(a[2], b[2]), Math.max(a[3], b[3])]);
    const near = (geonames.get(country) ?? []).filter(
      (row) =>
        row.x >= minX - NAME_MATCH_DEGREES && row.x <= maxX + NAME_MATCH_DEGREES &&
        row.y >= minY - NAME_MATCH_DEGREES && row.y <= maxY + NAME_MATCH_DEGREES,
    );
    const inside = near.filter((row) => members.some((member) => contains(member, row.x, row.y)));
    const best =
      (target && near.filter((row) => row.names.has(target)).reduce(byPopulation, undefined)) ||
      inside.filter((row) => SEAT_CODES.has(row.code)).reduce(byPopulation, undefined);
    if (best) {
      members.link = best.id;
      claimed.set(best.id, [...(claimed.get(best.id) ?? []), group]);
    } else fallback.set(group, inside);
  }
  for (const [group, inside] of fallback) {
    const best = inside.filter((row) => !claimed.has(row.id)).reduce(byPopulation, undefined);
    if (!best) {
      unlinked.push(group);
      continue;
    }
    groups.get(group).link = best.id;
    claimed.set(best.id, [group]);
  }
  // Keep a shared row only for the one city whose name matches it.
  const shared = [...claimed].filter(([, owners]) => owners.length > 1);
  for (const [id, owners] of shared) {
    const row = rowsById.get(id);
    const keep = owners.filter((group) => row.names.has(normalizeName(groups.get(group)[0].cityName)));
    for (const group of owners) {
      if (keep.length === 1 && keep[0] === group) continue;
      groups.get(group).link = "";
      unlinked.push(group);
    }
  }
  return { rowsById, unlinked, shared: shared.length };
}

/**
 * Streams alternateNamesV2 and keeps current (not historic or colloquial)
 * names in LANGUAGES for the given GeoNames ids.
 */
export async function loadAlternateNames(zip, ids) {
  const wanted = new Set(LANGUAGES);
  const out = new Map();
  const child = spawn("unzip", ["-p", zip, "alternateNamesV2.txt"], { stdio: ["ignore", "pipe", "inherit"] });
  const lines = readline.createInterface({ input: child.stdout, crlfDelay: Infinity });
  for await (const line of lines) {
    const f = line.split("\t");
    if (!wanted.has(f[2]) || !ids.has(f[1]) || f[6] === "1" || f[7] === "1" || f[8] || f[9]) continue;
    const entry = out.get(f[1]) ?? {};
    (entry[f[2]] ??= []).push({ name: f[3].trim(), preferred: f[4] === "1", short: f[5] === "1" });
    out.set(f[1], entry);
  }
  const code = await new Promise((resolve) => child.on("close", resolve));
  if (code !== 0) throw new Error(`unzip alternateNamesV2.txt exited ${code}`);
  return out;
}

// Administrative suffixes a bare alternate is preferred over (ソウル over ソウル特別市).
const CITY_SUFFIX = /\s*(특별자치시|특별시|광역시|시|特別市|特别市|廣域市|広域市|市|City|-si|Shi)$/u;
const HIRAGANA_ONLY = /^[\p{Script=Hiragana}ー\s]+$/u;

/**
 * One display name per language: GeoNames short names first, then preferred
 * names, then the earliest entry; a name that is another one plus a city
 * suffix gives way to the bare form, and hiragana readings give way to the
 * written form.
 */
export function pickName(candidates, language) {
  let list = (candidates ?? []).filter((candidate) => candidate.name);
  if (language === "ja" && list.some((candidate) => !HIRAGANA_ONLY.test(candidate.name))) {
    list = list.filter((candidate) => !HIRAGANA_ONLY.test(candidate.name));
  }
  const names = new Set(list.map((candidate) => candidate.name.replace(/\s+/g, "")));
  list = list.filter((candidate) => {
    const bare = candidate.name.replace(CITY_SUFFIX, "").replace(/\s+/g, "");
    return bare === candidate.name.replace(/\s+/g, "") || !names.has(bare);
  });
  const rank = (candidate) => (candidate.short ? 2 : 0) + (candidate.preferred ? 1 : 0);
  const best = list.reduce((top, candidate) => (!top || rank(candidate) > rank(top) ? candidate : top), undefined);
  if (!best) return "";
  // "요코하마 시" is the only Korean entry for Yokohama.
  return language === "ko" ? best.name.replace(/\s+시$/, "") : best.name;
}

/** Display names for a city: the boundary source's local name, then GeoNames. */
export function namesOf(country, local, row, alternates, override) {
  const names = { local };
  for (const language of LANGUAGES) {
    const picked = pickName(alternates?.[language], language);
    if (picked) names[language] = picked;
  }
  if (!names.en && row?.name) names.en = row.name;
  if (!names.en && /^[\p{Script=Latin}\p{N}\s.'’()-]+$/u.test(local)) names.en = local;
  // The boundary source is authoritative in its own language.
  if (country === "KR") names.ko = local.replace(/(특별자치시|특별시|광역시)$/, "");
  if (country === "JP") names.ja = local;
  return { ...names, ...override };
}
