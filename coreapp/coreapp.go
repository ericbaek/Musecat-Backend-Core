package coreapp

import (
	"context"
	"crypto/subtle"
	usernotifications "github.com/ericbaek/musecat-backend-core/handlers/user/notifications"
	"net/http"
	"os"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"

	"github.com/ericbaek/musecat-backend-core/arcadearea"
	"github.com/ericbaek/musecat-backend-core/docs"
	"github.com/ericbaek/musecat-backend-core/geo"
	"github.com/ericbaek/musecat-backend-core/handlers"
	announcementhandler "github.com/ericbaek/musecat-backend-core/handlers/announcement"
	arcadeadmin "github.com/ericbaek/musecat-backend-core/handlers/arcade/admin"
	arcadeanalytics "github.com/ericbaek/musecat-backend-core/handlers/arcade/analytics"
	arcadebasic "github.com/ericbaek/musecat-backend-core/handlers/arcade/basic"
	arcadecampaign "github.com/ericbaek/musecat-backend-core/handlers/arcade/campaign"
	arcadeflag "github.com/ericbaek/musecat-backend-core/handlers/arcade/flag"
	arcadegame "github.com/ericbaek/musecat-backend-core/handlers/arcade/game"
	arcadegtk "github.com/ericbaek/musecat-backend-core/handlers/arcade/gtk"
	arcadehour "github.com/ericbaek/musecat-backend-core/handlers/arcade/hour"
	arcadememo "github.com/ericbaek/musecat-backend-core/handlers/arcade/memo"
	arcadenotice "github.com/ericbaek/musecat-backend-core/handlers/arcade/notice"
	arcadephoto "github.com/ericbaek/musecat-backend-core/handlers/arcade/photo"
	arcadepublic "github.com/ericbaek/musecat-backend-core/handlers/arcade/public"
	arcadequery "github.com/ericbaek/musecat-backend-core/handlers/arcade/query"
	arcadereporting "github.com/ericbaek/musecat-backend-core/handlers/arcade/reporting"
	arcadesns "github.com/ericbaek/musecat-backend-core/handlers/arcade/sns"
	arcadeversion "github.com/ericbaek/musecat-backend-core/handlers/arcade/version"
	communityhandler "github.com/ericbaek/musecat-backend-core/handlers/community"
	gamecataloghandler "github.com/ericbaek/musecat-backend-core/handlers/gamecatalog"
	messaginghandler "github.com/ericbaek/musecat-backend-core/handlers/messaging"
	rankinghandler "github.com/ericbaek/musecat-backend-core/handlers/ranking"
	searchhandler "github.com/ericbaek/musecat-backend-core/handlers/search"
	statshandler "github.com/ericbaek/musecat-backend-core/handlers/stats"
	subwayhandler "github.com/ericbaek/musecat-backend-core/handlers/subway"
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/ericbaek/musecat-backend-core/nativeauth"
)

// Config contains dependencies selected by the executable. Configure never reads
// environment variables, registers migrations, or starts scheduled jobs.
// A nil GeoResolver deliberately disables geographic lookup (fail closed).
type Config struct {
	// NativeAuthClients is an exact first-party device client/callback allowlist. Empty disables all native routes.
	NativeAuthClients []nativeauth.Client
	NativeWebClients  []nativeauth.WebClient

	GeoResolver           *geo.OfflineResolver
	Documentation         DocumentationConfig
	ClientIPForwardSecret string
}

// DocumentationConfig defaults to Core's embedded contract and docs-site UI.
// SpecPath is an explicit development override; deployments should leave it empty.
type DocumentationConfig struct {
	SpecPath string
	SiteDir  string
	Username string
	Password string
}

func Configure(app *pocketbase.PocketBase, config Config) {
	geo.SetOfflineResolver(config.GeoResolver, true)
	RegisterDomainHooks(app)
	startArcadeAreaRefresh(app)
	arcadeflag.RegisterAutoSolveReactionCreateHook(app)

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.Bind(clientIPForwarding(config.ClientIPForwardSecret))
		configureDefaultRateLimits(se.App)
		RegisterDocumentationRoutes(se, config.Documentation)
		registerAPIRoutes(se, config.NativeAuthClients)
		nativeauth.RegisterRoutes(se, config.NativeAuthClients, config.NativeWebClients...)
		return se.Next()
	})
}

func configureDefaultRateLimits(app core.App) {
	if app == nil {
		return
	}
	settings := app.Settings()
	settings.RateLimits.Enabled = true
	defaults := []core.RateLimitRule{
		{Label: "/bot/v1/", MaxRequests: 120, Duration: 60, Audience: core.RateLimitRuleAudienceAll},
		{Label: "POST /user/bot-keys", MaxRequests: 10, Duration: 600, Audience: core.RateLimitRuleAudienceAll},
		{Label: "GET /geocode", MaxRequests: 30, Duration: 60, Audience: core.RateLimitRuleAudienceAll},
		{Label: "GET /reverse_geocode", MaxRequests: 30, Duration: 60, Audience: core.RateLimitRuleAudienceAll},
		{Label: "GET /geo", MaxRequests: 60, Duration: 60, Audience: core.RateLimitRuleAudienceAll},
		{Label: "POST /guest/conversations", MaxRequests: 10, Duration: 600, Audience: core.RateLimitRuleAudienceAll},
		{Label: "POST /guest/feedback/thread", MaxRequests: 60, Duration: 600, Audience: core.RateLimitRuleAudienceAll},
		{Label: "POST /support_feedback", MaxRequests: 10, Duration: 600, Audience: core.RateLimitRuleAudienceAll},
		{Label: "POST /user/feedback/thread", MaxRequests: 60, Duration: 600, Audience: core.RateLimitRuleAudienceAll},
	}
	for _, rule := range defaults {
		found := false
		for _, existing := range settings.RateLimits.Rules {
			if existing.Label == rule.Label && existing.Audience == rule.Audience {
				found = true
				break
			}
		}
		if !found {
			settings.RateLimits.Rules = append(settings.RateLimits.Rules, rule)
		}
	}
}

// RegisterAPIRoutes binds all application API routes to the ServeEvent router.
// It is exported so test applications and integration suites can wire the exact
// same routing table without duplicating it.
func RegisterAPIRoutes(se *core.ServeEvent) {
	registerAPIRoutes(se, nil)
}

// registerAPIRoutes binds the same routes and additionally accepts first-party
// Native session tokens on shared routes when nativeClients is non-empty.
func registerAPIRoutes(se *core.ServeEvent, nativeClients []nativeauth.Client) {
	var firstPartyAuth []*hook.Handler[*core.RequestEvent]
	if len(nativeClients) > 0 {
		firstPartyAuth = append(firstPartyAuth, nativeauth.FirstPartyAuthentication(nativeClients))
	}
	registerBotRoutes(se, nativeClients...)
	announcementhandler.RegisterRoutes(se, nativeClients...)
	messaginghandler.RegisterRoutes(se, nativeClients...)
	se.Router.GET("/guest/feedback", arcadeadmin.ListGuestSupportFeedback).Bind(arcadeadmin.RequireGuestFeedback())
	se.Router.GET("/guest/feedback/thread", arcadeadmin.GetSupportFeedbackThread).Bind(arcadeadmin.RequireGuestFeedback())
	se.Router.GET("/guest/feedback/thread/photo", arcadeadmin.GetSupportFeedbackReplyPhoto).Bind(arcadeadmin.RequireGuestFeedback())
	se.Router.POST("/guest/feedback/thread", arcadeadmin.ReplySupportFeedback).Bind(arcadeadmin.RequireGuestFeedback(), apis.BodyLimit(arcadeadmin.MaxSupportFeedbackBodyBytes))
	se.Router.PATCH("/guest/feedback/thread", arcadeadmin.UpdateSupportFeedbackThreadStatus).Bind(arcadeadmin.RequireGuestFeedback(), apis.BodyLimit(4096))
	se.Router.GET("/hello", handlers.HelloHandler)
	se.Router.GET("/geo", handlers.GeoLookupHandler)
	se.Router.GET("/geocode", handlers.GeocodeHandler)
	se.Router.GET("/reverse_geocode", handlers.ReverseGeocodeHandler)
	se.Router.GET("/search", searchhandler.Search)
	se.Router.GET("/stats", statshandler.GetStats)
	se.Router.GET("/rankings", rankinghandler.List).Bind(firstPartyAuth...)
	se.Router.GET("/arcade/ranking", rankinghandler.ArcadeVisitRanking)
	se.Router.GET("/subway/map", subwayhandler.GetMap)
	se.Router.GET("/subway/map/file", subwayhandler.DownloadMapFile)
	se.Router.POST("/subway/map", subwayhandler.CreateMap).Bind(firstPartyAuth...).Bind(
		apis.BodyLimit(24<<20),
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireAdminAccess(),
	)
	se.Router.PUT("/subway/map", subwayhandler.UpdateMap).Bind(firstPartyAuth...).Bind(
		apis.BodyLimit(24<<20),
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireAdminAccess(),
	)
	se.Router.DELETE("/subway/map", subwayhandler.DeleteMap).Bind(firstPartyAuth...).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireAdminAccess(),
	)
	se.Router.GET("/subway/map/history", subwayhandler.ListMapHistory).Bind(firstPartyAuth...).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireAdminAccess(),
	)
	// Public read endpoint: returns current relation ids for the arcade
	se.Router.GET("/arcade", arcadequery.GetArcadeValues).Bind(firstPartyAuth...)
	se.Router.GET("/arcade/analytics", arcadeanalytics.GetArcadeAnalytics).Bind(firstPartyAuth...)
	se.Router.POST("/arcade/analytics/event", arcadeanalytics.RecordDirectionClick)
	// Detailed changelog rows require an active user through this custom API.
	se.Router.GET("/arcade/changelog", arcadequery.ListArcadeChangelog).Bind(firstPartyAuth...).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
	)
	se.Router.GET("/arcade/memo", arcadememo.GetArcadeMemo).Bind(firstPartyAuth...)
	se.Router.GET("/arcade/photo/file", arcadephoto.DownloadArcadePhotoAtom).Bind(firstPartyAuth...)
	// Public read endpoint: list all arcades with basic info + gameSeries ids
	se.Router.GET("/arcades", arcadequery.ListArcades)
	se.Router.GET("/arcades/updates", arcadequery.ListArcadeUpdates)
	se.Router.GET("/arcades/updates/history", arcadequery.ListArcadeUpdateHistory).Bind(firstPartyAuth...).Bind(apis.RequireAuth("user"))
	se.Router.GET("/campaigns", arcadecampaign.ListCampaigns)
	se.Router.GET("/campaign", arcadecampaign.GetCampaign)
	se.Router.GET("/campaign/photo", arcadecampaign.ListPhotoCampaign)
	se.Router.GET("/arcade/campaigns/presence", arcadecampaign.ListArcadeCampaignPresence)
	se.Router.GET("/arcade/campaigns", arcadecampaign.ListArcadeCampaigns).Bind(firstPartyAuth...).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
	)
	se.Router.GET("/admin/arcade/areas", arcadeadmin.ListArcadeAreas).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireAdminAccess(),
	)
	se.Router.PUT("/admin/arcade/area", arcadeadmin.UpdateArcadeArea).Bind(
		apis.BodyLimit(4096),
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireAdminAccess(),
	)
	se.Router.POST("/campaign", arcadecampaign.CreateCampaign).Bind(firstPartyAuth...).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireAdminAccess(),
	)
	se.Router.PUT("/campaign", arcadecampaign.UpdateCampaign).Bind(firstPartyAuth...).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireAdminAccess(),
	)
	se.Router.POST("/campaign/end", arcadecampaign.EndCampaign).Bind(firstPartyAuth...).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireAdminAccess(),
	)
	// Public read endpoint: list arcades by game series near a location with pagination
	se.Router.GET("/arcades/nearby", arcadequery.ListArcadesBySeriesAndLocation).Bind(firstPartyAuth...)
	se.Router.GET("/arcade/visits", userhandler.GetArcadeVisitStats)
	// Public read endpoint: list machine rows filtered by country, game series, and version
	se.Router.GET("/arcade/games", arcadequery.ListArcadeGames).Bind(firstPartyAuth...).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireGameToolsAccess(),
	)
	// Public read endpoint: returns game_series_version and its series
	se.Router.GET("/game_series_version", arcadequery.GetGameSeriesVersion)
	// Public read endpoint: locale-localized version/cabinet compatibility catalog.
	se.Router.GET("/game/catalog", arcadequery.GetGameCatalog)
	catalogManagement := se.Router.Group("/moderation/game").Bind(
		apis.BodyLimit(128<<10),
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireGameToolsAccess(),
	)
	catalogManagement.GET("/catalog", gamecataloghandler.GetCatalog).Bind(firstPartyAuth...)
	catalogManagement.POST("/catalog", gamecataloghandler.Create).Bind(firstPartyAuth...)
	catalogManagement.PUT("/catalog", gamecataloghandler.Update).Bind(firstPartyAuth...)
	catalogManagement.DELETE("/catalog", gamecataloghandler.Archive).Bind(firstPartyAuth...)
	catalogManagement.POST("/catalog/restore", gamecataloghandler.Restore).Bind(firstPartyAuth...)
	catalogManagement.POST("/catalog/compatibilities", gamecataloghandler.ReplaceCompatibilities).Bind(firstPartyAuth...)
	catalogManagement.GET("/catalog/changes", gamecataloghandler.ListChanges).Bind(firstPartyAuth...)
	catalogManagement.POST("/catalog/changes/revert", gamecataloghandler.Revert).Bind(firstPartyAuth...)
	// Public user profile read endpoint
	se.Router.GET("/user", userhandler.GetUserByID).Bind(firstPartyAuth...)
	se.Router.GET("/user/arcade-inquiry-onboarding", arcadereporting.InquiryOnboarding).Bind(firstPartyAuth...).Bind(apis.RequireAuth("user"), userhandler.RequireActiveUser())
	se.Router.GET("/user/activity", userhandler.GetUserActivity).Bind(firstPartyAuth...)
	// Public user-scoped changelog read endpoint; private arcade rows are
	// returned only to their owner or strict reviewers.
	se.Router.GET("/user/changelog", userhandler.GetUserChangelog).Bind(firstPartyAuth...)
	se.Router.GET("/user/feedback", arcadeadmin.ListMySupportFeedback).Bind(firstPartyAuth...).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
	)
	se.Router.GET("/user/feedback/thread/photo", arcadeadmin.GetSupportFeedbackReplyPhoto).Bind(firstPartyAuth...).Bind(apis.RequireAuth("user"), userhandler.RequireActiveUser())
	se.Router.GET("/user/feedback/thread", arcadeadmin.GetSupportFeedbackThread).Bind(firstPartyAuth...).Bind(apis.RequireAuth("user"), userhandler.RequireActiveUser())
	se.Router.POST("/user/feedback/thread", arcadeadmin.ReplySupportFeedback).Bind(firstPartyAuth...).Bind(apis.RequireAuth("user"), userhandler.RequireActiveUser(), apis.BodyLimit(arcadeadmin.MaxSupportFeedbackBodyBytes))
	se.Router.PATCH("/user/feedback/thread", arcadeadmin.UpdateSupportFeedbackThreadStatus).Bind(firstPartyAuth...).Bind(apis.RequireAuth("user"), userhandler.RequireActiveUser(), apis.BodyLimit(4096))
	se.Router.GET("/support_feedback", arcadeadmin.ListSupportFeedback).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireStrictReviewerAccess(),
	)
	se.Router.PUT("/support_feedback", arcadeadmin.ReviewSupportFeedback).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireStrictReviewerAccess(),
	)
	se.Router.POST("/support_feedback", arcadeadmin.CreateSupportFeedback).Bind(firstPartyAuth...).Bind(apis.BodyLimit(arcadeadmin.MaxSupportFeedbackBodyBytes))
	communityhandler.RegisterAccountRoutes(se.Router.RouterGroup, firstPartyAuth...)

	authArcade := se.Router.Group("/arcade").Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
	)
	authArcade.POST("/new", arcadebasic.NewArcade).Bind(firstPartyAuth...)
	authArcade.GET("/draft", arcadequery.GetArcadeDraft).Bind(firstPartyAuth...)
	authArcade.GET("/drafts", arcadequery.ListMyArcadeDrafts).Bind(firstPartyAuth...)
	authArcade.DELETE("/draft", arcadequery.DeleteMyArcadeDraft).Bind(firstPartyAuth...)
	authArcade.GET("/public", arcadepublic.PreviewPublicArcade).Bind(firstPartyAuth...)
	authArcade.GET("/request_admin", arcadeadmin.ListArcadeRequestAdmin).Bind(firstPartyAuth...)
	authArcade.POST("/request_admin", arcadeadmin.CreateArcadeRequestAdmin).Bind(firstPartyAuth...)
	authArcade.POST("/edit_report", arcadeadmin.CreateArcadeEditReport).Bind(firstPartyAuth...)
	authArcade.POST("/rollback", arcadeadmin.RollbackArcadePart).Bind(firstPartyAuth...)
	authArcade.POST("/game/bulk_version", arcadeadmin.BulkUpdateArcadeGameVersion).Bind(firstPartyAuth...).Bind(arcadequery.RequireAdminAccess())
	authArcade.PUT("/basic", arcadebasic.UpdateArcadeBasic).Bind(firstPartyAuth...)
	authArcade.PUT("/public", arcadepublic.RequestPublicArcade).Bind(firstPartyAuth...)
	authArcade.POST("/location-verification", arcadepublic.VerifyArcadeLocation).Bind(firstPartyAuth...)
	authArcade.PUT("/gtk", arcadegtk.UpdateArcadeGTK).Bind(firstPartyAuth...)
	authArcade.PUT("/sns", arcadesns.UpdateArcadeSNS).Bind(firstPartyAuth...)
	authArcade.PUT("/hour", arcadehour.UpdateArcadeHour).Bind(firstPartyAuth...)
	authArcade.PUT("/game", arcadegame.UpdateArcadeGame).Bind(firstPartyAuth...)
	authArcade.PUT("/photo", arcadephoto.UpdateArcadePhoto).Bind(firstPartyAuth...)
	authArcade.PUT("/memo", arcadememo.UpdateArcadeMemo).Bind(firstPartyAuth...)
	authArcade.GET("/photo/atoms", arcadephoto.ListArcadePhotoAtoms).Bind(firstPartyAuth...)
	authArcade.DELETE("/photo/atom", arcadephoto.DeleteArcadePhotoAtom).Bind(firstPartyAuth...)
	// Allow up to 10 * 20MB photo files (+multipart overhead) in a single request.
	authArcade.POST("/photo/upload", arcadephoto.UploadArcadePhotos).Bind(firstPartyAuth...).Bind(apis.BodyLimit(220 << 20))
	authArcade.GET("/report-settings", arcadereporting.GetSettings).Bind(firstPartyAuth...)
	authArcade.PUT("/report-settings", arcadereporting.UpdateSettings).Bind(firstPartyAuth...).Bind(apis.BodyLimit(64 << 10))
	authArcade.PUT("/inquiry-settings", arcadereporting.UpdateInquirySettings).Bind(firstPartyAuth...).Bind(apis.BodyLimit(64 << 10))
	authArcade.POST("/report-subscription/status", arcadereporting.SubscriptionStatus).Bind(apis.BodyLimit(4 << 10))
	authArcade.PUT("/report-subscription", arcadereporting.SaveSubscription).Bind(apis.BodyLimit(16 << 10))
	authArcade.DELETE("/report-subscription", arcadereporting.DeleteSubscription).Bind(apis.BodyLimit(4 << 10))
	authArcade.POST("/flag", arcadeflag.CreateArcadeFlag).Bind(firstPartyAuth...).Bind(apis.BodyLimit(arcadeflag.MaxFlagBodyBytes))
	authArcade.POST("/flag/delete", arcadeflag.DeleteArcadeFlag).Bind(firstPartyAuth...)
	authArcade.POST("/flag/reaction", arcadeflag.UpdateArcadeFlagReaction).Bind(firstPartyAuth...)
	se.Router.GET("/arcade/notice", arcadenotice.ListArcadeNotice).Bind(firstPartyAuth...)
	authArcade.POST("/notice", arcadenotice.CreateArcadeNotice).Bind(firstPartyAuth...).Bind(apis.BodyLimit(arcadenotice.MaxNoticeBodyBytes))
	authArcade.PUT("/notice", arcadenotice.UpdateArcadeNotice).Bind(firstPartyAuth...).Bind(apis.BodyLimit(arcadenotice.MaxNoticeBodyBytes))
	authArcade.DELETE("/notice", arcadenotice.DeleteArcadeNotice).Bind(firstPartyAuth...)
	authArcade.POST("/visit", userhandler.VisitArcade).Bind(firstPartyAuth...)
	authArcade.PUT("/favorite", userhandler.UpdateArcadeFavorite).Bind(firstPartyAuth...)
	authArcade.PUT("/favorite/order", userhandler.UpdateArcadeFavoriteOrder).Bind(firstPartyAuth...)
	se.Router.POST("/campaign/check", arcadecampaign.CheckCampaign).Bind(firstPartyAuth...).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
	)

	authUser := se.Router.Group("/user").Bind(apis.RequireAuth("user"))

	authNotifications := se.Router.Group("/user/notifications").Bind(apis.RequireAuth("user"), userhandler.RequireActiveUser())
	authNotifications.GET("", usernotifications.List).Bind(firstPartyAuth...)
	authNotifications.POST("/read", usernotifications.Read).Bind(firstPartyAuth...).Bind(apis.BodyLimit(4096))
	authNotifications.POST("/device", usernotifications.DeviceStatus).Bind(apis.BodyLimit(4096))
	authNotifications.PATCH("/device", usernotifications.SyncDeviceLocale).Bind(apis.BodyLimit(4096))
	authNotifications.PUT("/device", usernotifications.SaveDevice).Bind(apis.BodyLimit(16384))
	authNotifications.DELETE("/device", usernotifications.DeleteDevice).Bind(apis.BodyLimit(4096))
	authUser.GET("/me", userhandler.GetMe)
	authUser.PUT("/profile", userhandler.UpdateProfile).Bind(firstPartyAuth...).Bind(userhandler.RequireActiveUser(), apis.BodyLimit(35<<20))
	authUser.PUT("/game-preferences", userhandler.UpdateGamePreferences).Bind(firstPartyAuth...).Bind(userhandler.RequireActiveUser(), apis.BodyLimit(128<<10))
	authUser.POST("/signup", userhandler.SignUp)
	authUser.POST("/check-in", userhandler.CheckIn).Bind(firstPartyAuth...).Bind(userhandler.RequireActiveUser())
	se.Router.GET("/user/passport", userhandler.GetMyPassport).Bind(firstPartyAuth...)
	se.Router.GET("/user/passport/stamps", userhandler.GetMyPassportStamps).Bind(firstPartyAuth...)
	authUser.GET("/visits", userhandler.GetMyVisits).Bind(firstPartyAuth...).Bind(userhandler.RequireActiveUser())
	authUser.PUT("/countries", userhandler.UpdateCountries).Bind(firstPartyAuth...).Bind(userhandler.RequireActiveUser())
	authUser.PUT("/visit-visibility", userhandler.UpdateVisitVisibility).Bind(firstPartyAuth...).Bind(userhandler.RequireActiveUser())
	authUser.PUT("/favorite-visibility", userhandler.UpdateFavoriteVisibility).Bind(firstPartyAuth...).Bind(userhandler.RequireActiveUser())
	authUser.POST("/withdraw", userhandler.Withdraw).Bind(nativeauth.FirstPartyAccountClosureAuthentication(nativeClients))
	authUser.PUT("/arcade-account/credentials", userhandler.UpdateArcadeCredentials).Bind(firstPartyAuth...).Bind(userhandler.RequireActiveUser(), apis.BodyLimit(16<<10))
	authUser.GET("/report", arcadeadmin.ListUserReport).Bind(firstPartyAuth...).Bind(userhandler.RequireActiveUser())
	authUser.POST("/report", arcadeadmin.CreateUserReport).Bind(firstPartyAuth...).Bind(userhandler.RequireActiveUser())

	reviewQueue := se.Router.Group("/moderation/arcade").Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireStrictReviewerAccess(),
	)
	supporterReview := se.Router.Group("/moderation/supporter").Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireStrictReviewerAccess(),
	)
	supporterReview.GET("/requests", arcadeadmin.ListSupporterRequests).Bind(firstPartyAuth...)
	supporterReview.GET("/request", arcadeadmin.GetSupporterRequest).Bind(firstPartyAuth...)
	supporterReview.PUT("/request", arcadeadmin.ReviewSupporterRequest).Bind(firstPartyAuth...)
	reviewQueue.GET("/requests", arcadeadmin.ListModerationRequests).Bind(firstPartyAuth...)
	reviewQueue.PUT("/request", arcadeadmin.ReviewModerationRequest).Bind(firstPartyAuth...)
	// Status tools are authorized per arcade inside the handler: staff, or the
	// same supporter/owner rule used for notices.
	arcadeStatus := se.Router.Group("/moderation/arcade").Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
	)
	arcadeStatus.GET("/status", arcadeadmin.GetArcadeStatus).Bind(firstPartyAuth...)
	arcadeStatus.PUT("/status", arcadeadmin.UpdateArcadeStatus).Bind(firstPartyAuth...)
	reviewQueue.GET("/edit-reports", arcadeadmin.ListArcadeEditReports)
	reviewQueue.PUT("/edit-report", arcadeadmin.ReviewArcadeEditReport)

	userReview := se.Router.Group("/moderation/user").Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireStrictReviewerAccess(),
	)
	userReview.POST("/test-account", userhandler.CreateTestAccount).Bind(firstPartyAuth...).Bind(apis.BodyLimit(16 << 10))
	userReview.GET("/reports", arcadeadmin.ListAllUserReports).Bind(firstPartyAuth...)
	userReview.PUT("/report", arcadeadmin.ReviewUserReport).Bind(firstPartyAuth...)
	userReview.GET("/bans", userhandler.ListUserBans).Bind(firstPartyAuth...)
	userReview.POST("/ban", userhandler.CreateOrUpdateUserBan).Bind(firstPartyAuth...)
	userReview.DELETE("/ban", userhandler.DeleteUserBan).Bind(firstPartyAuth...)
	arcadeAccountCreation := se.Router.Group("/moderation/user").Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
	)
	arcadeAccountCreation.POST("/arcade-account", userhandler.CreateArcadeAccount).Bind(firstPartyAuth...).Bind(apis.BodyLimit(16 << 10))

	authSupporter := se.Router.Group("/supporter").Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
	)
	authSupporter.GET("/score", arcadeadmin.GetSupporterScore).Bind(firstPartyAuth...)
	authSupporter.POST("/request", arcadeadmin.CreateSupporterRequest).Bind(firstPartyAuth...)
}

func RegisterDocumentationRoutes(se *core.ServeEvent, config DocumentationConfig) {
	if config.SiteDir == "" {
		config.SiteDir = "docs-site"
	}

	se.Router.GET("/openapi.yaml", func(re *core.RequestEvent) error {
		if err := config.authorize(re); err != nil {
			return err
		}

		spec, err := loadDocumentationSpec(config.SpecPath)
		if err != nil {
			return re.JSON(http.StatusInternalServerError, map[string]any{
				"error":   "failed to load OpenAPI spec",
				"details": err.Error(),
			})
		}

		return re.Blob(http.StatusOK, "application/yaml; charset=utf-8", spec)
	})

	se.Router.GET("/docs", func(re *core.RequestEvent) error {
		if err := config.authorize(re); err != nil {
			return err
		}

		return re.Redirect(http.StatusMovedPermanently, "/docs/")
	})

	se.Router.GET("/docs/{path...}", func(re *core.RequestEvent) error {
		if err := config.authorize(re); err != nil {
			return err
		}

		return apis.Static(os.DirFS(config.SiteDir), true)(re)
	})
}

func loadDocumentationSpec(path string) ([]byte, error) {
	if path != "" {
		return os.ReadFile(path)
	}
	return apidocs.OpenAPISpec(), nil
}

func (c DocumentationConfig) authorize(re *core.RequestEvent) error {
	if c.Username == "" && c.Password == "" {
		return nil
	}

	username, password, ok := re.Request.BasicAuth()
	if ok && c.Username != "" && c.Password != "" &&
		subtle.ConstantTimeCompare([]byte(username), []byte(c.Username)) == 1 &&
		subtle.ConstantTimeCompare([]byte(password), []byte(c.Password)) == 1 {
		return nil
	}

	re.Response.Header().Set("WWW-Authenticate", `Basic realm="Delta-DB Docs"`)
	return re.String(http.StatusUnauthorized, "Unauthorized")
}

// RegisterDomainHooks binds the record hooks that keep derived data
// consistent on every write path: arcade version pointers, stored admin areas,
// candidate snapshot invalidation and user hooks. Test apps that write arcades
// call it too, so they derive exactly what production does.
func RegisterDomainHooks(app core.App) {
	arcadeversion.RegisterHooks(app)
	arcadearea.RegisterHooks(app)
	arcadequery.RegisterCandidateSnapshotHooks(app)
	userhandler.RegisterHooks(app)
}

// startArcadeAreaRefresh decodes the embedded admin areas off the request path
// (~1s) once the server starts, then reclassifies arcades stored with an
// older dataset version in small batches. Each batch is one short write
// transaction, so SQLite's single writer is held only briefly and ordinary
// edits interleave; rows edited meanwhile are recomputed by their own save.
func startArcadeAreaRefresh(app *pocketbase.PocketBase) {
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		ctx, cancel := context.WithCancel(context.Background())
		se.App.OnTerminate().BindFunc(func(e *core.TerminateEvent) error {
			cancel()
			return e.Next()
		})
		go func() {
			if _, err := geo.EmbeddedAreas(); err != nil {
				se.App.Logger().Error("admin areas failed to load", "error", err)
				return
			}
			written, err := arcadearea.RecomputeStale(ctx, se.App, 100, 200*time.Millisecond, func() {
				arcadequery.InvalidateArcadeCandidateSnapshots(se.App)
			})
			if err != nil && ctx.Err() == nil {
				se.App.Logger().Error("arcade area refresh failed", "written", written, "error", err)
			} else if written > 0 {
				se.App.Logger().Info("arcade areas refreshed", "written", written)
			}
		}()
		return se.Next()
	})
}
