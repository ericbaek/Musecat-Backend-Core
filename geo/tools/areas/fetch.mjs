// Downloads every source in sources.json into a raw directory and verifies its
// SHA-256. Files already present with the pinned hash are skipped, so the
// command is safe to re-run.
//
//   node fetch.mjs [--raw <dir>] [--accept-rolling]
//
// Most sources are immutable (commit- or release-pinned URLs). GeoNames dumps
// are `rolling`: the URL always serves today's snapshot. A changed rolling
// file fails unless --accept-rolling, which records the new hash and date in
// sources.json so the refresh shows up in review.
import { createHash } from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { Readable } from "node:stream";
import { pipeline } from "node:stream/promises";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const argv = process.argv.slice(2);
const flag = (name) => argv.includes(`--${name}`);
const option = (name, fallback) => {
  const index = argv.indexOf(`--${name}`);
  return index >= 0 ? argv[index + 1] : fallback;
};
const rawDir = path.resolve(option("raw", path.join(here, ".raw")));
const manifestPath = path.join(here, "sources.json");
const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));

async function sha256(file) {
  const hash = createHash("sha256");
  await pipeline(fs.createReadStream(file), hash);
  return hash.digest("hex");
}

async function download(url, file) {
  const response = await fetch(url, { headers: { "User-Agent": "musecat-geo-areas" } });
  if (!response.ok || !response.body) throw new Error(`${url}: HTTP ${response.status}`);
  const partial = `${file}.partial`;
  await pipeline(Readable.fromWeb(response.body), fs.createWriteStream(partial));
  fs.renameSync(partial, file);
}

let changedManifest = false;
for (const source of manifest.sources) {
  const file = path.join(rawDir, source.file);
  fs.mkdirSync(path.dirname(file), { recursive: true });
  if (fs.existsSync(file) && (await sha256(file)) === source.sha256) {
    console.log(`ok       ${source.id}`);
    continue;
  }
  console.log(`download ${source.id}`);
  await download(source.url, file);
  const hash = await sha256(file);
  if (hash === source.sha256) continue;
  if (source.rolling && flag("accept-rolling")) {
    source.sha256 = hash;
    source.year = new Date().toISOString().slice(0, 10);
    changedManifest = true;
    console.log(`updated  ${source.id} ${hash}`);
    continue;
  }
  fs.renameSync(file, `${file}.mismatch`);
  throw new Error(
    `${source.id}: sha256 ${hash} != pinned ${source.sha256}` +
      (source.rolling ? " (rolling source: re-run with --accept-rolling to refresh)" : ""),
  );
}
if (changedManifest) fs.writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
console.log(`sources ready in ${rawDir}`);
