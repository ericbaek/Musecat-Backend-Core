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
`admin2_name`, `city`, `area_status`, `area_mismatch` and `area_version` on the
`arcade` record. A hook recomputes them in the same save whenever
`arcade.basic` or `arcade.country` changes, so creation, edits, bot edits and
rollbacks all agree. Only units of the arcade's stored country are considered.
Inside one unit is `resolved`; within 5 km of one (coastline, reclaimed land)
is `near`; countries without city boundaries take the nearest capital or
large-town seat within 60 km as `estimated`; nothing in reach is `unmapped`.
HK, MO and SG are one admin-1 and one city each. Classification never blocks a
write; failures are logged and stored as `unmapped`. `area_manual` keeps an
administrator's admin-1 and city until it is cleared.

City keys are `<admin1 or country>/<local name>`: `KR-11/서울특별시`,
`KR-41/수원시`, `JP-14/横浜市`, `JP-13/東京都区部`, `US-CA/Los Angeles`,
`GB/London`, `HK/香港`. Two same-named cities in one admin-1 get a coarse
position suffix (`CN-HA/Qixian~34.5,114.8`). Estimated seat cities are
`GN:<geonames id>`. Keys stay stable across source releases; the build's key
lock turns any removal into an explicit rename shipped in the dataset.

`area_mismatch` is set when the address names admin-1 areas of the country
(catalog aliases such as 경기, 東京都, California) but not the one the
coordinate falls in. It is a review hint only and never changes the area.

**Dataset updates.** The dataset carries a content `version`, stored per row
as `area_version`. After a release with a new dataset, the server reclassifies
older rows in the background once it starts serving: 100 rows per short write
transaction with a pause between batches, so SQLite's single writer is held
only briefly. Each write is conditional on the row still having the basic
revision, country and manual flag it was planned from, so a concurrent edit,
which recomputes in its own save, is never overwritten. Manual pins keep their
area, follow renamed city keys and refresh the address check. Full's
migrations may still backfill in one guarded step when a release changes the
schema.

City labels are not obtained from an external reverse-geocoder and there is no
city collection: the embedded catalog carries each city's local, English,
Korean and Japanese names, admin-1 and marker point, and each admin-1's names
and address aliases.

**Deprecated, scheduled for removal.** Core no longer reads or writes these;
Full owns their removal after clients stop reading them:

| Item | Replaced by | Remove when |
| --- | --- | --- |
| `passport_city` collection | embedded city catalog | Full migration after the Passport rollout is verified |
| `arcade_basic.city_id` (and the request field `city_id`) | `arcade.city` | Web and iOS no longer send or read it |
| Full's `cities500` import and GeoNames hierarchy job | `geo/tools/areas` build | together with `passport_city` |
