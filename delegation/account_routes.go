package delegation

import "sort"

// Explicit general-user operations only; management and unlisted operations fail closed.
var accountRoutes = map[string]struct{}{
	"DELETE /arcade/draft":               {},
	"DELETE /arcade/notice":              {},
	"DELETE /arcade/photo/atom":          {},
	"GET /arcade":                        {},
	"GET /arcade/analytics":              {},
	"GET /arcade/campaigns":              {},
	"GET /arcade/campaigns/presence":     {},
	"GET /arcade/changelog":              {},
	"GET /arcade/draft":                  {},
	"GET /arcade/drafts":                 {},
	"GET /arcade/memo":                   {},
	"GET /arcade/notice":                 {},
	"GET /arcade/photo/atoms":            {},
	"GET /arcade/photo/file":             {},
	"GET /arcade/public":                 {},
	"GET /arcade/ranking":                {},
	"GET /arcade/request_admin":          {},
	"GET /arcade/visits":                 {},
	"GET /arcades/nearby":                {},
	"GET /arcades/updates":               {},
	"GET /arcades/updates/history":       {},
	"GET /campaign":                      {},
	"GET /campaign/photo":                {},
	"GET /campaigns":                     {},
	"GET /community/post":                {},
	"GET /community/posts":               {},
	"GET /game/catalog":                  {},
	"GET /game_series_version":           {},
	"GET /geo":                           {},
	"GET /geocode":                       {},
	"GET /hello":                         {},
	"GET /rankings":                      {},
	"GET /reverse_geocode":               {},
	"GET /search":                        {},
	"GET /stats":                         {},
	"GET /subway/map":                    {},
	"GET /subway/map/file":               {},
	"GET /supporter/score":               {},
	"GET /user":                          {},
	"GET /user/activity":                 {},
	"GET /user/changelog":                {},
	"GET /user/feedback":                 {},
	"GET /user/me":                       {},
	"GET /user/passport":                 {},
	"GET /user/passport/stamps":          {},
	"GET /user/report":                   {},
	"GET /user/visits":                   {},
	"POST /arcade/analytics/event":       {},
	"POST /arcade/edit_report":           {},
	"POST /arcade/flag":                  {},
	"POST /arcade/flag/delete":           {},
	"POST /arcade/flag/reaction":         {},
	"POST /arcade/location-verification": {},
	"POST /arcade/new":                   {},
	"POST /arcade/notice":                {},
	"POST /arcade/photo/upload":          {},
	"POST /arcade/request_admin":         {},
	"POST /arcade/rollback":              {},
	"POST /arcade/visit":                 {},
	"POST /campaign/check":               {},
	"POST /community/post":               {},
	"POST /support_feedback":             {},
	"POST /supporter/request":            {},
	"POST /user/check-in":                {},
	"POST /user/report":                  {},
	"PUT /arcade/basic":                  {},
	"PUT /arcade/favorite":               {},
	"PUT /arcade/favorite/order":         {},
	"PUT /arcade/game":                   {},
	"PUT /arcade/gtk":                    {},
	"PUT /arcade/hour":                   {},
	"PUT /arcade/memo":                   {},
	"PUT /arcade/notice":                 {},
	"PUT /arcade/photo":                  {},
	"PUT /arcade/public":                 {},
	"PUT /arcade/sns":                    {},
	"PUT /community/post":                {},
	"PUT /user/countries":                {},
	"PUT /user/favorite-visibility":      {},
	"PUT /user/game-preferences":         {},
	"PUT /user/profile":                  {},
	"PUT /user/visit-visibility":         {},
}

func AccountRouteAllowed(method, path string) bool {
	if method == "HEAD" {
		method = "GET"
	}
	_, ok := accountRoutes[method+" "+path]
	return ok
}
func AccountRoutePatterns() []string {
	out := make([]string, 0, len(accountRoutes))
	for key := range accountRoutes {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
