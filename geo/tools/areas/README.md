# Admin-area boundaries

`build.mjs` turns the pinned sources in `sources.json` into
`geo/data/admin_areas.geojson.gz`. The server embeds that file and uses it to
classify each arcade into an admin-1 region and a Passport city. The build
runs offline; the server never downloads boundaries.

```sh
bun install
node build.mjs --raw <download dir> --geonames <cities500.zip> [--work <dir>] [--out <file>]
```

`<download dir>` holds each source at its `file` path. The build checks every
SHA-256 before it reads anything. `cities500.zip` is the same GeoNames dump that
Full imports into `passport_city`, so every linked id exists there.

## Output

Each feature is one admin-2 unit, or one admin-1 unit where only Natural Earth
covers the country. Properties:

| Property | Meaning |
| --- | --- |
| `country` | ISO 3166-1 alpha-2 |
| `admin1` | ISO 3166-2 code where one exists (`KR-11`, `JP-14`). Otherwise `CC-<NE adm1_code>`. `KR-12` is the 2026 전남광주통합특별시, which has no ISO code yet. Whole-territory places (HK, MO, SG) use the country code. |
| `admin2` | Source unit id (KR 시군구 code, JP 団体コード, geoBoundaries `shapeID`); empty for Natural Earth units |
| `name` | Local unit name |
| `city` | Key of the Passport city the unit belongs to (`KR:11`, `JP:14:横浜市`, `US:<shapeID>`); empty when the country has no city rule |

The collection also carries:

- `whole_country`: territories (HK, MO, SG) that are one admin-1 and one city,
  classified by country code without a shape test.
- `cities`: the Passport city catalog by key: `country`, `admin1`, `names`
  (`local` from the boundary source, `en`/`ko`/`ja` from GeoNames when linked),
  `lat`/`lon` marker point and `geonames_id`. Keys `GN:<id>` are `estimated`
  fallback cities (capitals, admin-1 seats and towns of 100k+) for countries
  without city boundaries; the server takes the nearest one in the country.

Passport reads this catalog directly; there is no city collection in the
database.

## City rules (`rules.json`)

A Passport city is a union of units. For names and a marker point it links to
a GeoNames row in this order:
a row whose local or alternate name matches the city near it; the most populous
PPLC/PPLA/PPLA2 inside it; the most populous place inside that no other city
claimed. A row is never shared by two cities. GeoNames points drift across
borders and miss towns (no Yongin in cities500), so a city without a clear row
keeps its local name instead of borrowing a neighbour's. GeoNames alternates
carry no language tag, so `ko`/`ja` are picked by script and may need fixes.

- KR: 특별·광역·특별자치시 are one city each. A 일반구 (수원시 장안구) joins its
  시. Gwangju's 구 inside 전남광주통합특별시 form 광주. Everything else is its 시·군.
- JP: wards of 政令指定都市 join the city. Tokyo's 23 special wards are one city.
  Other municipalities stand alone.
- CN: 直辖市 are one city each; other units are prefecture level.
- TW: each 縣市 is a city (Natural Earth admin-1). HK, MO and SG are one city each.
- Capitals split into districts in the source are one city: Bangkok (TH-10),
  Jakarta (ID-JK), Hà Nội, TP HCM, Hải Phòng, Đà Nẵng, Cần Thơ, and Greater
  London.
- US counties, AU LGAs and other geoBoundaries ADM2 units stand alone.
- CHN ADM2 also draws Taiwan, Hong Kong and Macau; those units are dropped.
- Natural Earth-only countries have no city rule. The server keeps the
  nearest-GeoNames estimate for them.

## Sources and licences

Only public-domain, CC BY, PDDL and open-government sources are used. ODbL and
CC BY-SA files are excluded. Attribution is listed in `geo/data/SOURCES.md`.
`legacy-naturalearth/` keeps the generators behind the Web region SVG maps.

## Checks

`--work` keeps intermediate files and `report.json`: units, admin-1 regions,
cities and unlinked cities per country. Against the 3,553 public arcades of
2026-11-20, all JP and KR arcades fall inside a unit; 18 elsewhere fall within
5 km of one (coast, reclaimed land) and one has a coordinate at sea.
