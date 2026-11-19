# Admin-area boundaries

`build.mjs` turns the pinned sources in `sources.json` into
`geo/data/admin_areas.geojson.gz`. The server embeds that file and uses it to
classify each arcade into an admin-1 region and a Passport city. The build
runs offline; the server never downloads boundaries.

```sh
npm install
npm run fetch        # downloads every source into .raw/ and checks its SHA-256
npm run build        # writes geo/data/admin_areas.geojson.gz and .work/admin1_map.geojson
```

`fetch` skips files that already match their hash. GeoNames dumps are
`rolling` (the URL always serves today's snapshot): a changed file fails until
you run `npm run fetch -- --accept-rolling`, which records the new hash and
date in `sources.json` so the refresh shows up in review. `build` checks every
hash again before it reads anything. Options: `--raw <dir>`, `--work <dir>`,
`--out <file>`, `--map <file>`, `--update-lock`.

Code layout: `lib/units.mjs` (per-country units), `lib/names.mjs` (GeoNames
links and names), `lib/catalog.mjs` (keys, catalogs, key lock, version),
`lib/geometry.mjs`.

## Output

Each feature is one admin-2 unit, or one admin-1 unit where only Natural Earth
covers the country. Properties:

| Property | Meaning |
| --- | --- |
| `country` | ISO 3166-1 alpha-2 |
| `admin1` | ISO 3166-2 code where one exists (`KR-11`, `JP-14`). Otherwise `CC-<NE adm1_code>`. `KR-12` is the 2026 전남광주통합특별시, which has no ISO code yet. Whole-territory places (HK, MO, SG) use the country code. |
| `admin2` | Source unit id (KR 시군구 code, JP 団体コード, geoBoundaries `shapeID`); empty for Natural Earth units |
| `name` | Local unit name |
| `city` | Key of the Passport city the unit belongs to; empty when the country has no city rule |

The collection also carries:

- `version`: hash of the content. The server stores it per arcade and
  reclassifies rows from older versions after a release.
- `whole_country`: territories (HK, MO, SG) that are one admin-1 and one city.
- `renames`: retired city key → current key, so stored manual pins follow.
- `admin1`: per code, `names` (`local`, `en`, `ko`, `ja`) and `aliases` an
  address may use (경기, 東京都, California). Aliases two areas of one country
  share are dropped. The server flags an address naming only other areas.
- `cities`: the Passport city catalog by key: `country`, `admin1`, `names`,
  `lat`/`lon` marker point and `geonames_id`. `GN:<id>` keys are `estimated`
  fallback cities (capitals, admin-1 seats and towns of 100k+ at least 10 km
  from a bigger seat) for countries without city boundaries.

`admin1_map.geojson` (in the work directory) dissolves the same units by
admin-1 for the Web region map, so the map and the server always agree.

## City keys and the key lock

A key is `<admin-1 or country>/<local name>`: `KR-11/서울특별시`, `KR-41/수원시`,
`JP-13/東京都区部`, `US-CA/Los Angeles`, `GB/London` (spans boroughs, so the
country prefix), `HK/香港`. Same-named cities in one admin-1 (淇县 and 杞县 are
both Qixian; Baltimore city and county) get the coarsest marker position that
tells them apart (`CN-HA/Qixian~34.5,114.8`); `rules.cityKeyOverrides` can
name a group explicitly.

Keys are stored on arcades, so `keys.lock.json` lists every key ever shipped.
New keys are added automatically. When a key disappears (a renamed city, a
moved border, a shifted position suffix) the build fails until you either map
it in `rules.keyRenames` (old → new; shipped as `renames`) or list it in
`rules.keyRetired`. Commit the lock with the dataset.

## Names

`local` comes from the boundary source. `en`/`ko`/`ja` come from GeoNames
`alternateNamesV2`, which tags each name with a language: short names first,
then preferred names; a name that is another one plus a city suffix gives way
(ソウル over ソウル特別市), and hiragana readings give way to the written form.
Historic and colloquial names are ignored. KR keeps the 행정구역 name as `ko`
and JP keeps the N03 name as `ja`. Fix any name in `rules.cityNames[key]`, and
admin-1 names and aliases in `rules.admin1[code]`.

A city links to one GeoNames row in this order: a row whose name matches the
city near it; the most populous PPLC/PPLA/PPLA2 inside it; the most populous
place inside that no other city claimed. A row is never shared by two cities.
A city without a clear row (cities500 has no Yongin) keeps its local name.

## City rules (`rules.json`)

- KR: 특별·광역·특별자치시 are one city each. A 일반구 (수원시 장안구) joins its
  시. Gwangju's 구 inside 전남광주통합특별시 form 광주. Everything else is its 시·군.
- JP: wards of 政令指定都市 join the city. Tokyo's 23 special wards are one city.
  Other municipalities stand alone.
- CN: 直辖市 are one city each. CHN ADM2 mixes prefecture cities with county
  units under old names (Changshaxian; Wangcheng became a Changsha district in
  2011), so each unit joins the prefecture (GeoNames admin-2) most places
  inside it are filed under, named from `admin2Codes.txt`
  (`rollUpGeoNamesAdmin2`). Units with no coded place keep their own city.
- TW: each 縣市 is a city (Natural Earth admin-1). HK, MO and SG are one city each.
- Capitals split into districts in the source are one city: Bangkok (TH-10),
  Jakarta (ID-JK), Hà Nội, TP HCM, Hải Phòng, Đà Nẵng, Cần Thơ, and Greater
  London.
- US counties, AU LGAs and other geoBoundaries ADM2 units stand alone.
- CHN ADM2 also draws Taiwan, Hong Kong and Macau; those units are dropped.
- Natural Earth-only countries use the `GN:` seat cities.

## Why the dataset is committed, not in Git LFS

The server embeds the file with `go:embed`, and Full consumes Core as a Go
module. The Go module proxy and `go get` fetch plain Git contents and never
LFS objects, so an LFS pointer would be embedded instead of the data. The
file is ~8 MB gzip and changes only with a source refresh.

## Sources and licences

Only public-domain, CC BY, PDDL and open-government sources are used. ODbL and
CC BY-SA files are excluded. Attribution is listed in `geo/data/SOURCES.md`.
`legacy-naturalearth/` keeps the generators behind the old Web region SVG maps.

## Checks

The work directory keeps intermediate files and `report.json`: version, units,
admin-1 regions and cities per country, and cities without a GeoNames link.
Against the 3,553 public arcades of 2026-11-20, all JP and KR arcades fall
inside a unit; 13 elsewhere fall within 5 km of one (coast, reclaimed land),
66 take an estimated seat city and one has a coordinate at sea. The address
check flags 10, mostly real source-boundary errors (a Sanmenxia arcade inside
the CHN Pinglu unit across the Yellow River, Kuala Lumpur arcades in Gombak and
Petaling, Sanchong inside Natural Earth's Taipei) for an administrator to pin.
Decoded, the dataset takes about 45 MB of heap and classifies in about 2 µs.
