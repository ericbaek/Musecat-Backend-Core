# Vendored geography data

These compressed GeoJSON files are embedded into the application binary. They
are read locally at startup; the server does not download or query either
source while handling a request.

| File | Source and version | License/attribution | SHA-256 |
| --- | --- | --- | --- |
| `countries.geojson.gz` | [datasets/geo-countries](https://github.com/datasets/geo-countries/blob/master/data/countries.geojson), repository snapshot used for this release | Natural Earth-derived country boundaries; see the upstream repository and Natural Earth terms | `cc314bd804b9eee416f553401a42b2be208759284273a533c3977b3459821cbf` |
| `timezones.geojson.gz` | [timezone-boundary-builder](https://github.com/evansiroky/timezone-boundary-builder/releases/tag/2026c), `timezones.geojson.zip` / `combined.json`, release 2026c | MIT; see the upstream repository | `aef830dc0edea6cdd83b5f65e9c0752e93eae8b98270907d19ba3a37647b919a` |

Refreshes must update the source URL/version and hash in the same change. The
GeoNames city catalog is a separate database import and is documented by the
Passport city import contract.
