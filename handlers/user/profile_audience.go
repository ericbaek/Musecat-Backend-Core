package user

import (
	"context"
	"github.com/pocketbase/pocketbase/core"
)

type publicProfileAudienceKey struct{}

// UsePublicProfileAudience keeps the authenticated actor for domain writes while
// applying ordinary third-party visibility to connected-app profile reads.
func UsePublicProfileAudience(re *core.RequestEvent) {
	re.Request = re.Request.WithContext(context.WithValue(re.Request.Context(), publicProfileAudienceKey{}, true))
}

func publicProfileAudience(re *core.RequestEvent) bool {
	return re.Request.Context().Value(publicProfileAudienceKey{}) == true
}

func profileForRequest(re *core.RequestEvent) (*Profile, error) {
	return fetchMergedProfile(re.App, re.Auth.Id, !publicProfileAudience(re))
}
