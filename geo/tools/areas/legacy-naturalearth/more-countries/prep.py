"""Splits Natural Earth admin-1 into per-country GeoJSON with a `slug` field.

Usage: python3 -I prep.py <nine.geojson> <outdir>
"""
import json
import re
import sys
import unicodedata

src, outdir = sys.argv[1], sys.argv[2]
data = json.load(open(src))


def slugify(text):
    text = unicodedata.normalize("NFKD", text).encode("ascii", "ignore").decode()
    return re.sub(r"[^a-z0-9]+", "-", text.lower()).strip("-")


MY = {
    "Pulau Pinang": "penang",
}

ID = {
    "Jakarta Raya": "jakarta",
    "Bangka-Belitung": "bangka-belitung",
}

PH_REGION = {
    "National Capital Region": "metro-manila",
    "Cordillera Administrative Region (CAR)": "cordillera",
    "Ilocos (Region I)": "ilocos",
    "Cagayan Valley (Region II)": "cagayan-valley",
    "Central Luzon (Region III)": "central-luzon",
    "CALABARZON (Region IV-A)": "calabarzon",
    "MIMAROPA (Region IV-B)": "mimaropa",
    "Bicol (Region V)": "bicol",
    "Western Visayas (Region VI)": "western-visayas",
    "Central Visayas (Region VII)": "central-visayas",
    "Eastern Visayas (Region VIII)": "eastern-visayas",
    "Zamboanga Peninsula (Region IX)": "zamboanga-peninsula",
    "Northern Mindanao (Region X)": "northern-mindanao",
    "Davao (Region XI)": "davao",
    "SOCCSKSARGEN (Region XII)": "soccsksargen",
    "Dinagat Islands (Region XIII)": "caraga",
    "Autonomous Region in Muslim Mindanao (ARMM)": "bangsamoro",
}
# Negros Island Region (2024): both Negros provinces, Bacolod and Siquijor.
PH_NEGROS = {"Negros Occidental", "Negros Oriental", "Bacolod", "Siquijor"}

# Vietnam's 34 provinces since 2025-07-01, by Natural Earth adm1_code.
VN_MERGE = {
    "ha-noi": ["Ha Noi"],
    "hue": ["Thừa Thiên - Huế"],
    "lai-chau": ["Lai Chau"],
    "dien-bien": ["Điện Biên"],
    "son-la": ["Son La"],
    "lang-son": ["Lạng Sơn"],
    "quang-ninh": ["Quảng Ninh"],
    "thanh-hoa": ["Thanh Hóa"],
    "nghe-an": ["Nghệ An"],
    "ha-tinh": ["Ha Tinh"],
    "cao-bang": ["Cao Bằng"],
    "tuyen-quang": ["Tuyên Quang", "Hà Giang"],
    "lao-cai": ["Lào Cai", "Yên Bái"],
    "thai-nguyen": ["Thái Nguyên", "VNM-451"],  # VNM-451 is Bắc Kạn
    "phu-tho": ["Phú Thọ", "Vĩnh Phúc", "Hòa Bình"],
    "bac-ninh": ["Bắc Ninh", "Bắc Giang"],
    "hung-yen": ["VNM-461", "Thái Bình"],  # VNM-461 is Hưng Yên
    "hai-phong": ["Hải Phòng", "Hải Dương"],
    "ninh-binh": ["Ninh Bình", "Hà Nam", "Nam Định"],
    "quang-tri": ["Quảng Trị", "Quảng Bình"],
    "da-nang": ["Đà Nẵng", "Quàng Nam"],
    "quang-ngai": ["Quảng Ngãi", "Kon Tum"],
    "gia-lai": ["Gia Lai", "Bình Định"],
    "khanh-hoa": ["Khánh Hòa", "Ninh Thuận"],
    "lam-dong": ["Lâm Đồng", "Đắk Nông", "Bình Thuận"],
    "dak-lak": ["Đắk Lắk", "Phú Yên"],
    "ho-chi-minh": ["Hồ Chí Minh city", "Bình Dương", "Bà Rịa - Vũng Tàu"],
    "dong-nai": ["VNM-497", "Bình Phước"],  # VNM-497 is Đồng Nai
    "tay-ninh": ["Tây Ninh", "Long An"],
    "can-tho": ["Can Tho", "Sóc Trăng", "Hau Giang"],
    "vinh-long": ["Vĩnh Long", "Bến Tre", "Trà Vinh"],
    "dong-thap": ["Ðong Tháp", "Tiền Giang"],
    "ca-mau": ["Cà Mau", "Bạc Liêu"],
    "an-giang": ["An Giang", "Kiên Giang"],
}
VN = {name: slug for slug, names in VN_MERGE.items() for name in names}

TW = {
    "Taipei City": "taipei",
    "New Taipei City": "new-taipei",
    "Keelung City": "keelung",
    "Taoyuan": "taoyuan",
    "Hsinchu City": "hsinchu-city",
    "Hsinchu": "hsinchu-county",
    "Miaoli": "miaoli",
    "Taichung City": "taichung",
    "Changhua": "changhua",
    "Nantou": "nantou",
    "Yunlin": "yunlin",
    "Chiayi City": "chiayi-city",
    "Chiayi": "chiayi-county",
    "Tainan City": "tainan",
    "Kaohsiung City": "kaohsiung",
    "Pingtung": "pingtung",
    "Yilan": "yilan",
    "Hualien": "hualien",
    "Taitung": "taitung",
    "Penghu": "penghu",
    "Kinmen": "kinmen",
}

AU_KEEP = {
    "New South Wales",
    "Victoria",
    "Queensland",
    "Western Australia",
    "South Australia",
    "Tasmania",
    "Australian Capital Territory",
    "Northern Territory",
}

TH_BANGKOK = {
    "Bangkok Metropolis",
    "Nonthaburi",
    "Pathum Thani",
    "Samut Prakan",
    "Samut Sakhon",
    "Nakhon Pathom",
}

SG = {
    "Central Singapore": "central",
    "North East": "north-east",
    "North West": "north-west",
    "South East": "south-east",
    "South West": "south-west",
}


def slug_for(p):
    cc, name, code, region = p["iso_a2"], p["name"], p["adm1_code"], p.get("region") or ""
    if cc == "MY":
        return MY.get(name, slugify(name))
    if cc == "ID":
        return ID.get(name, slugify(name))
    if cc == "PH":
        if name in PH_NEGROS:
            return "negros-island"
        return PH_REGION[region]
    if cc == "VN":
        return VN.get(code) or VN[name]
    if cc == "TW":
        return TW[name]
    if cc == "AU":
        return slugify(name) if name in AU_KEEP else None
    if cc == "US":
        return slugify(name)
    if cc == "TH":
        return "bangkok" if name in TH_BANGKOK else slugify(region)
    if cc == "SG":
        # Natural Earth's CDC districts are too coarse to place arcades.
        return "singapore"
    raise KeyError(cc)


by_country = {}
for feature in data["features"]:
    props = feature["properties"]
    slug = slug_for(props)
    if slug is None:
        continue
    by_country.setdefault(props["iso_a2"], []).append(
        {"type": "Feature", "properties": {"slug": slug}, "geometry": feature["geometry"]}
    )

for cc, features in by_country.items():
    with open(f"{outdir}/{cc}.geojson", "w") as fh:
        json.dump({"type": "FeatureCollection", "features": features}, fh)
    print(cc, len(features), len({f["properties"]["slug"] for f in features}))
