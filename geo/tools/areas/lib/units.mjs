// Per-country admin units. Each unit is one admin-2 area (or one admin-1 area
// where only Natural Earth covers the country) with:
//   country, admin1, admin2, name, geometry, bbox
//   group      internal id of the Passport city union (never shipped)
//   cityName   local name of that city
//   cityScope  prefix of the shipped city key: admin-1, or the country for
//              cities that span admin-1 codes (Greater London) or whole places
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { bboxOf, contains, polygonsOf } from "./geometry.mjs";

export function createContext({ rawDir, workDir, rules, mapshaper }) {
  const work = (name) => path.join(workDir, name);
  const precision = (key) => `precision=${rules.precision[key] ?? rules.precision.default}`;
  const simplify = (key) => `interval=${rules.simplifyMeters[key] ?? rules.simplifyMeters.default}`;
  const run = (...commands) =>
    execFileSync(process.execPath, ["--max-old-space-size=12288", mapshaper, ...commands], {
      stdio: ["ignore", "ignore", "inherit"],
    });
  const readFeatures = (file) => JSON.parse(fs.readFileSync(file, "utf8")).features;
  /** Extracts the named members of a zip into workDir once. */
  const extract = (zip, dir, members = []) => {
    const target = work(dir);
    if (!fs.existsSync(target) || fs.readdirSync(target).length === 0) {
      fs.mkdirSync(target, { recursive: true });
      execFileSync("unzip", ["-o", "-q", path.join(rawDir, zip), ...members, "-d", target]);
    }
    return target;
  };
  return { rawDir, workDir, rules, work, precision, simplify, run, readFeatures, extract };
}

const islands = (ctx) => ["-filter-islands", `min-area=${ctx.rules.minIslandKm2}km2`, "remove-empty"];

// --- Natural Earth admin-1 ---------------------------------------------------

/** Worldwide admin-1 polygons with ISO codes and localized names. */
export function loadNaturalEarth(ctx) {
  const dir = ctx.extract("ne/ne1.zip", "ne");
  ctx.run(
    "-i", path.join(dir, "ne_10m_admin_1_states_provinces.shp"), "encoding=utf8",
    "-each",
    "iso=iso_3166_2, cc=iso_a2, nm=name, rg=region, adm=adm1_code, ix=this.innerX, iy=this.innerY," +
      "n_en=name_en, n_ko=name_ko, n_ja=name_ja, n_zh=name_zh, n_zht=name_zht, n_local=name_local, n_alt=name_alt, n_woe=woe_name, n_gn=gn_name",
    "-filter-fields", "iso,cc,nm,rg,adm,ix,iy,n_en,n_ko,n_ja,n_zh,n_zht,n_local,n_alt,n_woe,n_gn",
    "-simplify", ctx.simplify("NE"), "keep-shapes",
    "-o", ctx.work("ne.geojson"), ctx.precision("NE"),
  );
  const features = ctx.readFeatures(ctx.work("ne.geojson"));
  for (const feature of features) feature.bbox = bboxOf(feature.geometry);
  return features;
}

export function admin1CodeOf(ctx, neFeature) {
  const { iso, cc, adm } = neFeature.properties;
  if (ctx.rules.wholeCountry.includes(cc)) return cc;
  return /^[A-Z]{2}-[A-Z0-9]{1,3}$/.test(iso ?? "") ? iso : `${cc}-${adm}`;
}

// --- Korea: 행정동 dissolved to 시군구 ------------------------------------------

export function koreaUnits(ctx) {
  ctx.run(
    "-i", path.join(ctx.rawDir, "kr/HangJeongDong_ver20261001.geojson"),
    "-dissolve", "sgg", "copy-fields=sggnm,sido,sidonm",
    ...islands(ctx),
    "-simplify", ctx.simplify("KR"), "keep-shapes",
    "-o", ctx.work("KR.geojson"), ctx.precision("KR"),
  );
  const kr = ctx.rules.countries.KR;
  return ctx.readFeatures(ctx.work("KR.geojson")).map((feature) => {
    const { sgg, sggnm, sido, sidonm } = feature.properties;
    const admin1 = kr.sidoToAdmin1[sido];
    if (!admin1) throw new Error(`KR: unknown sido ${sido}; add it to rules.countries.KR.sidoToAdmin1`);
    let cityName = sggnm;
    const general = sggnm.match(/^(.+?시)(.+구)$/);
    if (kr.metropolitanSido.includes(sido)) cityName = sidonm; // 특별·광역시 = one city
    else if (kr.metropolitanGuInSido[sido] && /구$/.test(sggnm) && !/시/.test(sggnm)) cityName = kr.metropolitanGuInSido[sido];
    else if (general) cityName = general[1]; // 일반구 → 시
    return { country: "KR", admin1, admin2: sgg, name: sggnm, group: `KR:${admin1}:${cityName}`, cityName, cityScope: admin1, admin1Name: sidonm, geometry: feature.geometry };
  });
}

// --- Japan: 国土数値情報 N03 dissolved to 市区町村 ------------------------------

export function japanUnits(ctx) {
  const dir = ctx.extract("N03-20250101_GML.zip", "n03", [
    "N03-20250101.shp", "N03-20250101.shx", "N03-20250101.dbf", "N03-20250101.prj", "N03-20250101.cpg",
  ]);
  ctx.run(
    "-i", path.join(dir, "N03-20250101.shp"), "encoding=utf8",
    "-filter", "N03_007 != null && N03_007 !== ''",
    "-dissolve", "N03_007", "copy-fields=N03_001,N03_004,N03_005",
    ...islands(ctx),
    "-simplify", ctx.simplify("JP"), "keep-shapes",
    "-o", ctx.work("JP.geojson"), ctx.precision("JP"),
  );
  const jp = ctx.rules.countries.JP;
  return ctx.readFeatures(ctx.work("JP.geojson")).map((feature) => {
    const { N03_001: pref, N03_004: city, N03_005: ward, N03_007: code } = feature.properties;
    const name = `${city ?? ""}${ward ?? ""}`;
    const admin1 = `JP-${code.slice(0, 2)}`;
    let cityName = city ?? name; // 政令指定都市の区 → 市
    if (!ward && code >= jp.tokyoSpecialWards[0] && code <= jp.tokyoSpecialWards[1]) cityName = jp.tokyoSpecialWardsName;
    return { country: "JP", admin1, admin2: code, name, group: `JP:${admin1}:${cityName}`, cityName, cityScope: admin1, admin1Name: pref, geometry: feature.geometry };
  });
}

// --- geoBoundaries ADM2, parented by Natural Earth admin-1 ---------------------

function nearestByInnerPoint(features, x, y) {
  let best;
  let bestDistance = Infinity;
  for (const feature of features) {
    const distance = Math.hypot(feature.properties.ix - x, feature.properties.iy - y);
    if (distance < bestDistance) {
      bestDistance = distance;
      best = feature;
    }
  }
  return best;
}

export function geoBoundariesUnits(ctx, country, ne) {
  const config = ctx.rules.countries[country];
  ctx.run(
    "-i", path.join(ctx.rawDir, `gb/${config.iso3}-ADM2.geojson`),
    "-each", "ix=this.innerX, iy=this.innerY",
    ...islands(ctx),
    "-simplify", ctx.simplify(country), "keep-shapes",
    "-o", ctx.work(`${country}.geojson`), ctx.precision(country),
  );
  const parents = ne.filter((feature) => feature.properties.cc === country);
  const foreign = ne.filter((feature) => feature.properties.cc !== country);
  const units = [];
  for (const feature of ctx.readFeatures(ctx.work(`${country}.geojson`))) {
    const { shapeID, shapeName, ix, iy } = feature.properties;
    // CHN ADM2 also draws Taiwan, Hong Kong and Macau, which come from their own
    // sources; a nameless CHN sliver covers part of Coloane. Drop both.
    if (!shapeName || foreign.some((candidate) => contains(candidate, ix, iy))) continue;
    const parent = parents.find((candidate) => contains(candidate, ix, iy)) ?? nearestByInnerPoint(parents, ix, iy);
    const admin1 = parent ? admin1CodeOf(ctx, parent) : country;
    const regionCity = config.cityByRegion?.[parent?.properties.rg];
    let unit = { group: `${country}:${shapeID}`, cityName: shapeName, cityScope: admin1 };
    if (config.cityIsAdmin1?.includes(admin1)) unit = { group: `${country}:${admin1}`, cityName: parent.properties.nm, cityScope: admin1 };
    else if (regionCity) unit = { group: `${country}:region:${regionCity}`, cityName: regionCity, cityScope: country };
    units.push({ country, admin1, admin2: shapeID, name: shapeName, ...unit, geometry: feature.geometry });
  }
  return units;
}

// --- Natural Earth only --------------------------------------------------------

export function naturalEarthUnits(ctx, ne, covered) {
  const units = [];
  const whole = new Map();
  for (const feature of ne) {
    const { cc, nm } = feature.properties;
    if (!/^[A-Z]{2}$/.test(cc ?? "") || covered.has(cc)) continue;
    const admin1 = admin1CodeOf(ctx, feature);
    if (ctx.rules.wholeCountry.includes(cc)) {
      // One admin-1 and one city for the whole territory (HK, MO, SG).
      const cityName = ctx.rules.wholeCountryNames[cc] ?? cc;
      const merged = whole.get(cc) ?? { country: cc, admin1, admin2: "", name: cityName, group: `${cc}:all`, cityName, cityScope: cc, geometry: { type: "MultiPolygon", coordinates: [] } };
      merged.geometry.coordinates.push(...polygonsOf(feature.geometry));
      whole.set(cc, merged);
      continue;
    }
    const isCity = ctx.rules.admin1IsCity.includes(cc);
    units.push({ country: cc, admin1, admin2: "", name: nm, group: isCity ? `${cc}:${admin1}` : "", cityName: isCity ? nm : "", cityScope: admin1, geometry: feature.geometry });
  }
  return [...units, ...whole.values()];
}
