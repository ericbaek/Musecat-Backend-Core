package query

import (
	"fmt"
	"strings"
)

var regionAliasPairs = []string{
	"서울특별시", "서울",
	"부산광역시", "부산",
	"대구광역시", "대구",
	"인천광역시", "인천",
	"광주광역시", "광주",
	"대전광역시", "대전",
	"울산광역시", "울산",
	"세종특별자치시", "세종",
	"제주특별자치도", "제주",
	"강원특별자치도", "강원",
	"강원도", "강원",
	"경기도", "경기",
	"충청북도", "충북",
	"충청남도", "충남",
	"전북특별자치도", "전북",
	"전라북도", "전북",
	"전라남도", "전남",
	"경상북도", "경북",
	"경상남도", "경남",
}

func normalizeSearchText(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return ""
	}
	return strings.Join(strings.Fields(normalized), " ")
}

func normalizeAddressQuery(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return ""
	}
	normalized = strings.Join(strings.Fields(normalized), "")
	return regionAliasReplacer.Replace(normalized)
}

func buildNormalizedAddressSQLExpr(baseExpr string) string {
	expr := fmt.Sprintf("replace(%s, ' ', '')", baseExpr)
	for i := 0; i+1 < len(regionAliasPairs); i += 2 {
		expr = fmt.Sprintf(
			"replace(%s, '%s', '%s')",
			expr,
			regionAliasPairs[i],
			regionAliasPairs[i+1],
		)
	}
	return expr
}
