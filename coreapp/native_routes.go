package coreapp

import (
	arcadeadmin "github.com/ericbaek/musecat-backend-core/handlers/arcade/admin"
	arcadeanalytics "github.com/ericbaek/musecat-backend-core/handlers/arcade/analytics"
	arcadecampaign "github.com/ericbaek/musecat-backend-core/handlers/arcade/campaign"
	arcadeflag "github.com/ericbaek/musecat-backend-core/handlers/arcade/flag"
	arcadephoto "github.com/ericbaek/musecat-backend-core/handlers/arcade/photo"
	arcadequery "github.com/ericbaek/musecat-backend-core/handlers/arcade/query"
	arcadereporting "github.com/ericbaek/musecat-backend-core/handlers/arcade/reporting"
	communityhandler "github.com/ericbaek/musecat-backend-core/handlers/community"
	gamecataloghandler "github.com/ericbaek/musecat-backend-core/handlers/gamecatalog"
	rankinghandler "github.com/ericbaek/musecat-backend-core/handlers/ranking"
	subwayhandler "github.com/ericbaek/musecat-backend-core/handlers/subway"
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
)

// Explicit aliases share web handlers, including their ownership checks and
// transaction boundaries. Public web reads remain available to signed-out apps.
func registerNativeParityRoutes(secured *router.RouterGroup[*core.RequestEvent]) {
	// Match web's default body limit; individual upload and administration limits
	// below override it. Existing native v1/draft envelopes stay unchanged.
	native := secured.Group("").Bind(apis.BodyLimit(apis.DefaultMaxBodySize))
	native.GET("/arcade", arcadequery.GetArcadeValues)
	native.GET("/arcade/changelog", arcadequery.ListArcadeChangelog)
	native.GET("/arcades/updates/history", arcadequery.ListArcadeUpdateHistory)
	native.GET("/arcade/analytics", arcadeanalytics.GetArcadeAnalytics)
	native.POST("/arcade/analytics/event", arcadeanalytics.RecordDirectionClick)
	native.GET("/arcade/photo/atoms", arcadephoto.ListArcadePhotoAtoms)
	native.GET("/arcade/photo/file", arcadephoto.DownloadArcadePhotoAtom)
	native.DELETE("/arcade/photo/atom", arcadephoto.DeleteArcadePhotoAtom)
	native.POST("/arcade/photo/upload", arcadephoto.UploadArcadePhotos).Bind(apis.BodyLimit(220 << 20))
	native.PUT("/arcade/photo", arcadephoto.UpdateArcadePhoto)
	native.POST("/arcade/flag", arcadeflag.CreateArcadeFlag).Bind(apis.BodyLimit(arcadeflag.MaxFlagBodyBytes))
	native.GET("/arcade/flag/photo", arcadeflag.DownloadArcadeFlagPhoto)
	native.POST("/arcade/flag/delete", arcadeflag.DeleteArcadeFlag)
	native.POST("/arcade/flag/reaction", arcadeflag.UpdateArcadeFlagReaction)
	native.GET("/arcade/request_admin", arcadeadmin.ListArcadeRequestAdmin)
	native.POST("/arcade/request_admin", arcadeadmin.CreateArcadeRequestAdmin)
	native.POST("/arcade/edit_report", arcadeadmin.CreateArcadeEditReport)
	native.POST("/arcade/rollback", arcadeadmin.RollbackArcadePart)
	native.GET("/arcade/report-settings", arcadereporting.GetSettings)
	native.PUT("/arcade/report-settings", arcadereporting.UpdateSettings).Bind(apis.BodyLimit(64 << 10))
	native.PUT("/arcade/inquiry-settings", arcadereporting.UpdateInquirySettings).Bind(apis.BodyLimit(64 << 10))
	native.POST("/arcade/report-subscription/status", arcadereporting.SubscriptionStatus).Bind(apis.BodyLimit(4 << 10))
	native.PUT("/arcade/report-subscription", arcadereporting.SaveSubscription).Bind(apis.BodyLimit(16 << 10))
	native.DELETE("/arcade/report-subscription", arcadereporting.DeleteSubscription).Bind(apis.BodyLimit(4 << 10))
	native.POST("/arcade/apns-subscription/status", arcadereporting.NativeAPNSSubscriptionStatus).Bind(apis.BodyLimit(4 << 10))
	native.PUT("/arcade/apns-subscription", arcadereporting.UpdateNativeAPNSSubscription).Bind(apis.BodyLimit(4 << 10))
	native.DELETE("/arcade/apns-subscription", arcadereporting.UpdateNativeAPNSSubscription).Bind(apis.BodyLimit(4 << 10))
	native.GET("/user", userhandler.GetUserByID)
	native.GET("/user/activity", userhandler.GetUserActivity)
	native.GET("/user/changelog", userhandler.GetUserChangelog)
	native.GET("/user/passport/stamps", userhandler.GetMyPassportStamps)
	native.PUT("/user/profile", userhandler.UpdateProfile).Bind(apis.BodyLimit(35 << 20))
	native.PUT("/user/game-preferences", userhandler.UpdateGamePreferences).Bind(apis.BodyLimit(128 << 10))
	native.POST("/user/signup", userhandler.SignUp)
	native.POST("/user/check-in", userhandler.CheckIn)
	native.PUT("/user/countries", userhandler.UpdateCountries)
	native.PUT("/user/visit-visibility", userhandler.UpdateVisitVisibility)
	native.PUT("/user/favorite-visibility", userhandler.UpdateFavoriteVisibility)
	native.POST("/user/withdraw", userhandler.Withdraw)
	native.PUT("/user/arcade-account/credentials", userhandler.UpdateArcadeCredentials).Bind(apis.BodyLimit(16 << 10))
	native.GET("/user/report", arcadeadmin.ListUserReport)
	native.POST("/user/report", arcadeadmin.CreateUserReport)
	native.GET("/user/arcade-inquiry-onboarding", arcadereporting.InquiryOnboarding)
	native.GET("/user/feedback", arcadeadmin.ListMySupportFeedback)
	native.GET("/user/feedback/thread", arcadeadmin.GetSupportFeedbackThread)
	native.POST("/user/feedback/thread", arcadeadmin.ReplySupportFeedback).Bind(apis.BodyLimit(arcadeadmin.MaxSupportFeedbackBodyBytes))
	native.PATCH("/user/feedback/thread", arcadeadmin.UpdateSupportFeedbackThreadStatus).Bind(apis.BodyLimit(4096))
	native.GET("/user/feedback/thread/photo", arcadeadmin.GetSupportFeedbackReplyPhoto)
	native.POST("/support_feedback", arcadeadmin.CreateSupportFeedback).Bind(apis.BodyLimit(arcadeadmin.MaxSupportFeedbackBodyBytes))
	native.GET("/rankings", rankinghandler.List)
	native.GET("/arcade/ranking", rankinghandler.ArcadeVisitRanking)
	native.GET("/arcade/campaigns", arcadecampaign.ListArcadeCampaigns)
	native.POST("/campaign/check", arcadecampaign.CheckCampaign)
	native.GET("/supporter/score", arcadeadmin.GetSupporterScore)
	native.POST("/supporter/request", arcadeadmin.CreateSupporterRequest)
	native.GET("/community/posts", communityhandler.ListPosts)
	native.GET("/community/post", communityhandler.GetPost)
	native.POST("/community/post", communityhandler.CreatePost).Bind(apis.BodyLimit(communityhandler.MaxPostBodyBytes))
	native.PUT("/community/post", communityhandler.UpdatePost).Bind(apis.BodyLimit(communityhandler.MaxPostBodyBytes))

	// Role gates are identical to RegisterAPIRoutes; native authentication never
	// turns an ordinary contributor into a reviewer, catalog editor, or owner.
	native.POST("/moderation/user/arcade-account", userhandler.CreateArcadeAccount).Bind(apis.BodyLimit(16 << 10))
	admin := native.Group("").Bind(arcadequery.RequireAdminAccess())
	admin.POST("/campaign", arcadecampaign.CreateCampaign)
	admin.PUT("/campaign", arcadecampaign.UpdateCampaign)
	admin.POST("/campaign/end", arcadecampaign.EndCampaign)
	admin.POST("/arcade/game/bulk_version", arcadeadmin.BulkUpdateArcadeGameVersion)
	admin.POST("/subway/map", subwayhandler.CreateMap).Bind(apis.BodyLimit(24 << 20))
	admin.PUT("/subway/map", subwayhandler.UpdateMap).Bind(apis.BodyLimit(24 << 20))
	admin.DELETE("/subway/map", subwayhandler.DeleteMap)
	admin.GET("/subway/map/history", subwayhandler.ListMapHistory)
	native.GET("/arcade/games", arcadequery.ListArcadeGames).Bind(arcadequery.RequireGameToolsAccess())
	catalog := native.Group("/moderation/game").Bind(arcadequery.RequireGameToolsAccess(), apis.BodyLimit(128<<10))
	catalog.GET("/catalog", gamecataloghandler.GetCatalog)
	catalog.POST("/catalog", gamecataloghandler.Create)
	catalog.PUT("/catalog", gamecataloghandler.Update)
	catalog.DELETE("/catalog", gamecataloghandler.Archive)
	catalog.POST("/catalog/restore", gamecataloghandler.Restore)
	catalog.POST("/catalog/compatibilities", gamecataloghandler.ReplaceCompatibilities)
	catalog.GET("/catalog/changes", gamecataloghandler.ListChanges)
	catalog.POST("/catalog/changes/revert", gamecataloghandler.Revert)
	review := native.Group("").Bind(arcadequery.RequireStrictReviewerAccess())
	review.GET("/support_feedback", arcadeadmin.ListSupportFeedback)
	review.PUT("/support_feedback", arcadeadmin.ReviewSupportFeedback)
	review.GET("/moderation/arcade/requests", arcadeadmin.ListModerationRequests)
	review.PUT("/moderation/arcade/request", arcadeadmin.ReviewModerationRequest)
	review.GET("/moderation/arcade/status", arcadeadmin.GetArcadeStatus)
	review.PUT("/moderation/arcade/status", arcadeadmin.UpdateArcadeStatus)
	review.GET("/moderation/arcade/edit-reports", arcadeadmin.ListArcadeEditReports)
	review.PUT("/moderation/arcade/edit-report", arcadeadmin.ReviewArcadeEditReport)
	review.GET("/moderation/supporter/requests", arcadeadmin.ListSupporterRequests)
	review.GET("/moderation/supporter/request", arcadeadmin.GetSupporterRequest)
	review.PUT("/moderation/supporter/request", arcadeadmin.ReviewSupporterRequest)
	review.POST("/moderation/user/test-account", userhandler.CreateTestAccount).Bind(apis.BodyLimit(16 << 10))
	review.GET("/moderation/user/reports", arcadeadmin.ListAllUserReports)
	review.PUT("/moderation/user/report", arcadeadmin.ReviewUserReport)
	review.GET("/moderation/user/bans", userhandler.ListUserBans)
	review.POST("/moderation/user/ban", userhandler.CreateOrUpdateUserBan)
	review.DELETE("/moderation/user/ban", userhandler.DeleteUserBan)
}
