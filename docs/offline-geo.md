# Offline geography bundle

Arcade creation and basic-location edits resolve country and timezone locally
when the bundle provides valid results. The application embeds the
versioned, gzip-compressed GeoJSON files in `geo/data/` and loads them once at
startup into an immutable in-memory resolver. `MUSECAT_GEO_DATA_DIR` may point
to an unpacked replacement bundle when an operator intentionally upgrades the
boundary data; an invalid override fails closed instead of falling back to a
network provider.

The embedded files are:

- `countries.geojson.gz`: Natural Earth country polygons from the
  [datasets/geo-countries repository](https://github.com/datasets/geo-countries).
  Each feature carries an `ISO3166-1-Alpha-2` property.
- `timezones.geojson.gz`: timezone polygons from
  [timezone-boundary-builder](https://github.com/evansiroky/timezone-boundary-builder),
  release 2026c, with IANA `tzid` properties.

The exact source URLs, release information, and SHA-256 hashes are recorded in
[`geo/data/SOURCES.md`](../geo/data/SOURCES.md). Keep that file with any data
refresh so a deployed binary can be reproduced and attributed.

Only `Polygon` and `MultiPolygon` geometries are accepted. A coordinate normally
matches exactly one country and one timezone polygon. When no country contains
the coordinate, a single country exterior boundary within 1,000 metres may
resolve a coastline approximation gap. Distances use a local equirectangular
projection with antimeridian-aware segments. Multiple nearby country features
and overlapping country matches still fail. A missing country or an invalid code
such as Natural Earth's `-99` sentinel uses the existing BigDataCloud country
provider, then Nominatim, within an eight-second total budget. The offline
timezone is retained; no external timezone request is made. Invalid provider
codes are rejected. Successful fallbacks are cached for 24 hours in the bounded
geo cache and concurrent lookups for the same coordinate share one request.
Failed lookups are not cached and return `503` without persisting an arcade.
Timezone and city containment use the original coordinate without snapping;
missing or ambiguous timezones still fail. The resolver validates that
the timezone is an installed IANA location and returns a failure when the
timezone is missing or ambiguous, or no country provider succeeds.

Admin areas and Passport cities come from `geo/data/admin_areas.geojson.gz`,
built offline from pinned boundary sources (see `geo/tools/areas/README.md`).
Each arcade stores `admin1` (ISO 3166-2 where one exists), `admin2`,
`admin2_name`, `city` (a catalog key such as `KR:11` or `JP:14:横浜市`) and
`area_status` on the `arcade` record. A hook recomputes them in the same save
whenever `arcade.basic` or `arcade.country` changes, so creation, edits, bot
edits and rollbacks all agree. Only units of the arcade's stored country are
considered. Inside one unit is `resolved`; within 5 km of one (coastline,
reclaimed land) is `near`; countries without city boundaries take the nearest
capital or large-town seat within 60 km as `estimated`; nothing in reach is
`unmapped`. HK, MO and SG are one admin-1 and one city each. Classification
never blocks a write; failures are logged and stored as `unmapped`.
`area_manual` keeps an administrator's override until it is cleared.

City labels are not obtained from an external reverse-geocoder and there is no
city collection: the embedded catalog carries each city's local, English,
Korean and Japanese names, admin-1 and marker point. The former
`passport_city` collection and `arcade_basic.city_id` are deprecated and no
longer read or written by Core; Full owns their removal and the backfill of
existing arcades.
