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

City labels are not obtained from an external reverse-geocoder. `passport_city`
is imported from the versioned GeoNames catalog and stores names, aliases,
administrative ids, and coordinates locally. After the country lookup, the
request and the one-time Full backfill choose the nearest catalog city in that
country with a deterministic distance/tie-break rule. There is intentionally
no distance cutoff: an assigned city is more useful to Passport coverage than
an avoidable `도시 미분류` label. Full ships a versioned GeoNames `cities500`
catalog for the one-time backfill; a request with no valid city rows keeps the
city relation empty.

The Full repository owns the production data migration and seeds that catalog
when `passport_city` is empty. The guarded one-time backfill is idempotent and
can be retried safely. No proposal or editor token is required.
