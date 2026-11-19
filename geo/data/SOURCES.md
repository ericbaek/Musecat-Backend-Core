# Vendored geography data

These compressed GeoJSON files are embedded into the application binary. They
are read locally at startup; the server does not download or query either
source while handling a request.

| File | Source and version | License/attribution | SHA-256 |
| --- | --- | --- | --- |
| `countries.geojson.gz` | [datasets/geo-countries](https://github.com/datasets/geo-countries/blob/master/data/countries.geojson), repository snapshot used for this release | Natural Earth-derived country boundaries; see the upstream repository and Natural Earth terms | `cc314bd804b9eee416f553401a42b2be208759284273a533c3977b3459821cbf` |
| `timezones.geojson.gz` | [timezone-boundary-builder](https://github.com/evansiroky/timezone-boundary-builder/releases/tag/2026c), `timezones.geojson.zip` / `combined.json`, release 2026c | MIT; see the upstream repository | `aef830dc0edea6cdd83b5f65e9c0752e93eae8b98270907d19ba3a37647b919a` |
| `admin_areas.geojson.gz` | Built by `geo/tools/areas/build.mjs` from the pinned sources in `geo/tools/areas/sources.json` (URLs, versions and SHA-256 of each input) | See "Admin-area attribution" below | `63115e73098d54a7babdd73ff521aa2391480f77dafda71e9552e3059d101dbd` |

Refreshes must update the source URL/version and hash in the same change. The
GeoNames city catalog is a separate database import and is documented by the
Passport city import contract.

## Admin-area attribution

`admin_areas.geojson.gz` is simplified and merged from these sources. Only
public-domain, CC BY, PDDL and open-government licences are used.

| Source | Licence | Attribution |
| --- | --- | --- |
| [geoboundaries-aus-adm2](https://github.com/wmgeolab/geoBoundaries/raw/9469f09/releaseData/gbOpen/AUS/ADM2/geoBoundaries-AUS-ADM2.geojson) | Creative Commons Attribution 4.0 International (CC BY 4.0) | geoBoundaries (Runfola et al. 2020), Australian Bureau of Statistics |
| [geoboundaries-can-adm2](https://github.com/wmgeolab/geoBoundaries/raw/9469f09/releaseData/gbOpen/CAN/ADM2/geoBoundaries-CAN-ADM2.geojson) | Open Government Canada 2.0 | geoBoundaries (Runfola et al. 2020), Statistics Canada (StatCan) |
| [geoboundaries-chn-adm2](https://github.com/wmgeolab/geoBoundaries/raw/9469f09/releaseData/gbOpen/CHN/ADM2/geoBoundaries-CHN-ADM2.geojson) | Open Data Commons Public Domain Dedication and License (PDDL) v1.0 | geoBoundaries (Runfola et al. 2020), National Administration of Surveying, Mapping and Geoinformation of China, Revolutionary GIS |
| [geoboundaries-gbr-adm2](https://github.com/wmgeolab/geoBoundaries/raw/9469f09/releaseData/gbOpen/GBR/ADM2/geoBoundaries-GBR-ADM2.geojson) | Open Government Licence v3.0 | geoBoundaries (Runfola et al. 2020), Office for National Statistics Open Geography Portal |
| [geoboundaries-idn-adm2](https://github.com/wmgeolab/geoBoundaries/raw/9469f09/releaseData/gbOpen/IDN/ADM2/geoBoundaries-IDN-ADM2.geojson) | Creative Commons Attribution 3.0 Intergovernmental Organisations (CC BY 3.0 IGO) | geoBoundaries (Runfola et al. 2020), Badan Pusat Statistik (BPS - Statistics Indonesia), World Food Programme, OCHA ROAP |
| [geoboundaries-mys-adm2](https://github.com/wmgeolab/geoBoundaries/raw/9469f09/releaseData/gbOpen/MYS/ADM2/geoBoundaries-MYS-ADM2.geojson) | Creative Commons Attribution 3.0 License | geoBoundaries (Runfola et al. 2020), geoBoundaries, citypopulation.de |
| [geoboundaries-nzl-adm2](https://github.com/wmgeolab/geoBoundaries/raw/9469f09/releaseData/gbOpen/NZL/ADM2/geoBoundaries-NZL-ADM2.geojson) | Creative Commons Attribution 4.0 International (CC BY 4.0) | geoBoundaries (Runfola et al. 2020), Stats NZ |
| [geoboundaries-phl-adm2](https://github.com/wmgeolab/geoBoundaries/raw/41af8f1/releaseData/gbOpen/PHL/ADM2/geoBoundaries-PHL-ADM2.geojson) | Creative Commons Attribution 3.0 Intergovernmental Organisations (CC BY 3.0 IGO) | geoBoundaries (Runfola et al. 2020), National Mapping and Resource Information Authority (NAMRIA), Philippines Statistics Authority (PSA), OCHA Philippines |
| [geoboundaries-tha-adm2](https://github.com/wmgeolab/geoBoundaries/raw/9469f09/releaseData/gbOpen/THA/ADM2/geoBoundaries-THA-ADM2.geojson) | Creative Commons Attribution 3.0 Intergovernmental Organisations (CC BY 3.0 IGO) | geoBoundaries (Runfola et al. 2020), Royal Thai Survey Department, OCHA ROAP |
| [geoboundaries-usa-adm2](https://github.com/wmgeolab/geoBoundaries/raw/9469f09/releaseData/gbOpen/USA/ADM2/geoBoundaries-USA-ADM2.geojson) | Public Domain | geoBoundaries (Runfola et al. 2020), United States Census Bureau, MAF/TIGER Database |
| [geoboundaries-vnm-adm2](https://github.com/wmgeolab/geoBoundaries/raw/9469f09/releaseData/gbOpen/VNM/ADM2/geoBoundaries-VNM-ADM2.geojson) | Creative Commons Attribution 3.0 Intergovernmental Organisations (CC BY 3.0 IGO) | geoBoundaries (Runfola et al. 2020), OCHA ROAP, Government of Viet Nam |
| [kr-sgis-admdong-20261001](https://raw.githubusercontent.com/vuski/admdongkor/078ef4ac13b8bfeb215d2eabace49f4fdea71d5e/ver20261001/HangJeongDong_ver20261001.geojson) | CC BY 4.0 (source: KOGL Type 1) | 통계청 SGIS 행정동 경계 (공공누리 제1유형), processed by vuski/admdongkor (CC BY 4.0) |
| [jp-ksj-n03-2025](https://nlftp.mlit.go.jp/ksj/gml/data/N03/N03-2025/N03-20250101_GML.zip) | CC BY 4.0 | 「国土数値情報（行政区域データ）」（国土交通省）を加工して作成 |
| [naturalearth-admin1-5.1.1](https://naciscdn.org/naturalearth/10m/cultural/ne_10m_admin_1_states_provinces.zip) | Public Domain | Made with Natural Earth |

geoBoundaries: Runfola, D. et al. (2020) geoBoundaries: A global database of political administrative boundaries. PLoS ONE 15(4): e0231866.
