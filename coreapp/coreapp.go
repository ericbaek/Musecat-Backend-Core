package coreapp

import (
	"crypto/subtle"
	usernotifications "github.com/ericbaek/musecat-backend-core/handlers/user/notifications"
	"net/http"
	"os"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"

	"github.com/ericbaek/musecat-backend-core/connections"
	"github.com/ericbaek/musecat-backend-core/delegation"
	"github.com/ericbaek/musecat-backend-core/docs"
	"github.com/ericbaek/musecat-backend-core/geo"
	"github.com/ericbaek/musecat-backend-core/handlers"
	announcementhandler "github.com/ericbaek/musecat-backend-core/handlers/announcement"
	arcadeadmin "github.com/ericbaek/musecat-backend-core/handlers/arcade/admin"
	arcadeanalytics "github.com/ericbaek/musecat-backend-core/handlers/arcade/analytics"
	arcadebasic "github.com/ericbaek/musecat-backend-core/handlers/arcade/basic"
	arcadecampaign "github.com/ericbaek/musecat-backend-core/handlers/arcade/campaign"
	arcadechecknote "github.com/ericbaek/musecat-backend-core/handlers/arcade/checknote"
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
	connectionhandler "github.com/ericbaek/musecat-backend-core/handlers/connections"
	delegationhandler "github.com/ericbaek/musecat-backend-core/handlers/delegation"
	gamecataloghandler "github.com/ericbaek/musecat-backend-core/handlers/gamecatalog"
	jointhandler "github.com/ericbaek/musecat-backend-core/handlers/joint"
	messaginghandler "github.com/ericbaek/musecat-backend-core/handlers/messaging"
	partnerapphandler "github.com/ericbaek/musecat-backend-core/handlers/partnerapp"
	rankinghandler "github.com/ericbaek/musecat-backend-core/handlers/ranking"
	searchhandler "github.com/ericbaek/musecat-backend-core/handlers/search"
	statshandler "github.com/ericbaek/musecat-backend-core/handlers/stats"
	subwayhandler "github.com/ericbaek/musecat-backend-core/handlers/subway"
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/ericbaek/musecat-backend-core/nativeauth"
	"github.com/ericbaek/musecat-backend-core/partnerapp"
)

// Config contains dependencies selected by the executable. Configure never reads
// environment variables, registers migrations, or starts scheduled jobs.
// A nil GeoResolver deliberately disables geographic lookup (fail closed).
type Config struct {
	// NativeAuthClients is an exact first-party device client/callback allowlist. Empty disables all native routes.
	NativeAuthClients []nativeauth.Client
	NativeWebClients  []nativeauth.WebClient
	Delegation        *delegation.Manager
	Connections       *connections.Manager
	// Joint enables reciprocal consent for registered partners.
	Joint *connections.JointManager
	// PartnerApps is the Developer Center service. Executables that register its
	// sync cron pass their instance; nil creates one bound to Delegation.
	PartnerApps           *partnerapp.Service
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
	arcadeversion.RegisterHooks(app)
	arcadequery.RegisterCandidateSnapshotHooks(app)
	arcadeflag.RegisterAutoSolveReactionCreateHook(app)
	userhandler.RegisterHooks(app)

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.Bind(clientIPForwarding(config.ClientIPForwardSecret))
		configureDefaultRateLimits(se.App)
		RegisterDocumentationRoutes(se, config.Documentation)
		RegisterAPIRoutes(se, config.Connections)
		RegisterNativeAPIRoutes(se, config.NativeAuthClients, config.NativeWebClients...)
		partnerApps := config.PartnerApps
		if partnerApps == nil {
			partnerApps = partnerapp.NewService(config.Delegation, config.Connections, config.Joint, "")
		}
		se.App.OnRecordAfterUpdateSuccess("user").BindFunc(func(e *core.RecordEvent) error {
			if e.Record.GetBool("withdrawn") {
				if err := partnerApps.Sync(e.App); err != nil {
					e.App.Logger().Error("Failed to synchronize applications after owner withdrawal")
				}
			}
			return e.Next()
		})
		// Load approved Developer Center clients on top of the static set, then
		// synchronize registration epochs (disabling anything no longer configured).
		if err := partnerApps.Sync(se.App); err != nil {
			return err
		}
		if config.Delegation == nil {
			if err := delegation.SynchronizeClients(se.App, nil); err != nil {
				return err
			}
		}
		partnerapphandler.RegisterRoutes(se, partnerApps)
		partnerapphandler.RegisterRecordsInvalidationRoutes(se, partnerApps, config.Connections)
		jointhandler.RegisterRoutes(se, config.Joint)
		delegationhandler.RegisterRoutes(se, config.Delegation)
		RegisterAccountDelegationRoutes(se, config.Delegation)
		return se.Next()
	})
}

// RegisterNativeAPIRoutes reuses canonical reads and inbox read state behind active opaque device sessions.
// Domain handlers retain their existing visibility and account ownership policies.
func RegisterNativeAPIRoutes(se *core.ServeEvent, clients []nativeauth.Client, webClients ...nativeauth.WebClient) *router.RouterGroup[*core.RequestEvent] {
	secured := nativeauth.RegisterRoutes(se, clients, webClients...)
	if secured == nil {
		return nil
	}
	secured.GET("/user/visits", userhandler.GetMyVisits)
	secured.GET("/user/passport", userhandler.GetMyPassport)
	secured.GET("/user/passport/stamps", userhandler.GetMyPassportStamps)
	secured.GET("/rankings", rankinghandler.List)
	secured.GET("/user/notifications", usernotifications.List)
	secured.POST("/user/notifications/read", usernotifications.Read)
	return secured
}

func configureDefaultRateLimits(app core.App) {
	if app == nil {
		return
	}
	settings := app.Settings()
	settings.RateLimits.Enabled = true
	defaults := []core.RateLimitRule{
		{Label: "GET /oauth/authorize", MaxRequests: 10, Duration: 600, Audience: core.RateLimitRuleAudienceAll},
		{Label: "POST /oauth/token", MaxRequests: 60, Duration: 600, Audience: core.RateLimitRuleAudienceAll},
		{Label: "POST /oauth/consent", MaxRequests: 20, Duration: 600, Audience: core.RateLimitRuleAudienceAll},
		{Label: "GET /oauth/consent", MaxRequests: 60, Duration: 600, Audience: core.RateLimitRuleAudienceAll},
		{Label: "POST /oauth/revoke", MaxRequests: 60, Duration: 600, Audience: core.RateLimitRuleAudienceAll},
		{Label: "/developer/v1/", MaxRequests: 120, Duration: 60, Audience: core.RateLimitRuleAudienceAll},
		{Label: "POST /user/connections/start", MaxRequests: 10, Duration: 600, Audience: core.RateLimitRuleAudienceAll},
		{Label: "POST /user/connections/complete", MaxRequests: 20, Duration: 600, Audience: core.RateLimitRuleAudienceAll},
		{Label: "GET /geocode", MaxRequests: 30, Duration: 60, Audience: core.RateLimitRuleAudienceAll},
		{Label: "GET /reverse_geocode", MaxRequests: 30, Duration: 60, Audience: core.RateLimitRuleAudienceAll},
		{Label: "GET /geo", MaxRequests: 60, Duration: 60, Audience: core.RateLimitRuleAudienceAll},
		{Label: "POST /guest/conversations", MaxRequests: 10, Duration: 600, Audience: core.RateLimitRuleAudienceAll},
		{Label: "POST /guest/feedback/thread", MaxRequests: 60, Duration: 600, Audience: core.RateLimitRuleAudienceAll},
		{Label: "POST /support_feedback", MaxRequests: 10, Duration: 600, Audience: core.RateLimitRuleAudienceAll},
		{Label: "POST /developer/apps", MaxRequests: 30, Duration: 600, Audience: core.RateLimitRuleAudienceAll},
		{Label: "PUT /developer/apps/", MaxRequests: 30, Duration: 600, Audience: core.RateLimitRuleAudienceAll},
		{Label: "POST /user/joint/lookup", MaxRequests: 20, Duration: 600, Audience: core.RateLimitRuleAudienceAll},
		{Label: "POST /user/joint/begin", MaxRequests: 20, Duration: 600, Audience: core.RateLimitRuleAudienceAll},
		{Label: "/partner/v1/", MaxRequests: 120, Duration: 60, Audience: core.RateLimitRuleAudienceAll},
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

// RegisterAPIRoutes binds first-party application API routes. Configure also
// registers the independent public OAuth/developer routing table.
// It is exported so test applications and integration suites can wire the exact
// same routing table without duplicating it.
func RegisterAPIRoutes(se *core.ServeEvent, managers ...*connections.Manager) {
	var manager *connections.Manager
	if len(managers) > 0 {
		manager = managers[0]
	}
	connectionhandler.RegisterRoutes(se, manager)
	announcementhandler.RegisterRoutes(se)
	messaginghandler.RegisterRoutes(se)
	se.Router.GET("/guest/feedback", arcadeadmin.ListGuestSupportFeedback).Bind(arcadeadmin.RequireGuestFeedback())
	se.Router.GET("/guest/feedback/thread", arcadeadmin.GetSupportFeedbackThread).Bind(arcadeadmin.RequireGuestFeedback())
	se.Router.GET("/guest/feedback/thread/photo", arcadeadmin.GetSupportFeedbackReplyPhoto).Bind(arcadeadmin.RequireGuestFeedback())
	se.Router.POST("/guest/feedback/thread", arcadeadmin.ReplySupportFeedback).Bind(arcadeadmin.RequireGuestFeedback(), apis.BodyLimit(arcadeadmin.MaxSupportFeedbackBodyBytes))
	se.Router.PATCH("/guest/feedback/thread", arcadeadmin.UpdateSupportFeedbackThreadStatus).Bind(arcadeadmin.RequireGuestFeedback(), apis.BodyLimit(4096))
	registerSessionRoutes(se.Router.RouterGroup)
	registerAccountRoutes(se.Router.RouterGroup)
}

// AccountResource rejects management and unknown routes before any domain handler.
// New features need an explicit entry in delegation.AccountRoutePatterns.
func RegisterAccountDelegationRoutes(se *core.ServeEvent, manager *delegation.Manager) {
	api := se.Router.Group("/developer/v1/account").Bind(delegationhandler.AccountResource(manager))
	registerAccountRoutes(api)
}
func registerSessionRoutes(api *router.RouterGroup[*core.RequestEvent]) {
	auth := api.Group("/user").Bind(apis.RequireAuth("user"))
	auth.POST("/signup", userhandler.SignUp)
	auth.POST("/withdraw", userhandler.Withdraw)
	auth.PUT("/arcade-account/credentials", userhandler.UpdateArcadeCredentials).Bind(userhandler.RequireActiveUser(), apis.BodyLimit(16<<10))
	review := api.Group("/moderation/user").Bind(apis.RequireAuth("user"), userhandler.RequireActiveUser(), arcadequery.RequireStrictReviewerAccess())
	review.POST("/test-account", userhandler.CreateTestAccount).Bind(apis.BodyLimit(16 << 10))
	creation := api.Group("/moderation/user").Bind(apis.RequireAuth("user"), userhandler.RequireActiveUser())
	creation.POST("/arcade-account", userhandler.CreateArcadeAccount).Bind(apis.BodyLimit(16 << 10))
}
func registerAccountRoutes(api *router.RouterGroup[*core.RequestEvent]) {
	api.GET("/hello", handlers.HelloHandler)
	api.GET("/geo", handlers.GeoLookupHandler)
	api.GET("/geocode", handlers.GeocodeHandler)
	api.GET("/reverse_geocode", handlers.ReverseGeocodeHandler)
	api.GET("/search", searchhandler.Search)
	api.GET("/stats", statshandler.GetStats)
	api.GET("/rankings", rankinghandler.List)
	api.GET("/arcade/ranking", rankinghandler.ArcadeVisitRanking)
	api.GET("/subway/map", subwayhandler.GetMap)
	api.GET("/subway/map/file", subwayhandler.DownloadMapFile)
	api.POST("/subway/map", subwayhandler.CreateMap).Bind(
		apis.BodyLimit(24<<20),
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireAdminAccess(),
	)
	api.PUT("/subway/map", subwayhandler.UpdateMap).Bind(
		apis.BodyLimit(24<<20),
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireAdminAccess(),
	)
	api.DELETE("/subway/map", subwayhandler.DeleteMap).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireAdminAccess(),
	)
	api.GET("/subway/map/history", subwayhandler.ListMapHistory).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireAdminAccess(),
	)
	// Public read endpoint: returns current relation ids for the arcade
	api.GET("/arcade", arcadequery.GetArcadeValues)
	api.GET("/arcade/analytics", arcadeanalytics.GetArcadeAnalytics)
	api.POST("/arcade/analytics/event", arcadeanalytics.RecordDirectionClick)
	// Detailed changelog rows require an active user through this custom API.
	api.GET("/arcade/changelog", arcadequery.ListArcadeChangelog).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
	)
	api.GET("/arcade/memo", arcadememo.GetArcadeMemo)
	api.GET("/arcade/check-notes", arcadechecknote.List)
	api.GET("/arcade/photo/file", arcadephoto.DownloadArcadePhotoAtom)
	// Public read endpoint: list all arcades with basic info + gameSeries ids
	api.GET("/arcades", arcadequery.ListArcades)
	api.GET("/arcades/updates", arcadequery.ListArcadeUpdates)
	api.GET("/arcades/updates/history", arcadequery.ListArcadeUpdateHistory).Bind(apis.RequireAuth("user"))
	api.GET("/campaigns", arcadecampaign.ListCampaigns)
	api.GET("/campaign", arcadecampaign.GetCampaign)
	api.GET("/campaign/photo", arcadecampaign.ListPhotoCampaign)
	api.GET("/arcade/campaigns/presence", arcadecampaign.ListArcadeCampaignPresence)
	api.GET("/arcade/campaigns", arcadecampaign.ListArcadeCampaigns).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
	)
	api.POST("/campaign", arcadecampaign.CreateCampaign).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireAdminAccess(),
	)
	api.PUT("/campaign", arcadecampaign.UpdateCampaign).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireAdminAccess(),
	)
	api.POST("/campaign/end", arcadecampaign.EndCampaign).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireAdminAccess(),
	)
	// Public read endpoint: list arcades by game series near a location with pagination
	api.GET("/arcades/nearby", arcadequery.ListArcadesBySeriesAndLocation)
	api.GET("/arcade/visits", userhandler.GetArcadeVisitStats)
	// Public read endpoint: list machine rows filtered by country, game series, and version
	api.GET("/arcade/games", arcadequery.ListArcadeGames).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireGameToolsAccess(),
	)
	// Public read endpoint: returns game_series_version and its series
	api.GET("/game_series_version", arcadequery.GetGameSeriesVersion)
	// Public read endpoint: locale-localized version/cabinet compatibility catalog.
	api.GET("/game/catalog", arcadequery.GetGameCatalog)
	catalogManagement := api.Group("/moderation/game").Bind(
		apis.BodyLimit(128<<10),
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireGameToolsAccess(),
	)
	catalogManagement.GET("/catalog", gamecataloghandler.GetCatalog)
	catalogManagement.POST("/catalog", gamecataloghandler.Create)
	catalogManagement.PUT("/catalog", gamecataloghandler.Update)
	catalogManagement.DELETE("/catalog", gamecataloghandler.Archive)
	catalogManagement.POST("/catalog/restore", gamecataloghandler.Restore)
	catalogManagement.POST("/catalog/compatibilities", gamecataloghandler.ReplaceCompatibilities)
	catalogManagement.GET("/catalog/changes", gamecataloghandler.ListChanges)
	catalogManagement.POST("/catalog/changes/revert", gamecataloghandler.Revert)
	// Public user profile read endpoint
	api.GET("/user", userhandler.GetUserByID)
	api.GET("/user/arcade-inquiry-onboarding", arcadereporting.InquiryOnboarding).Bind(apis.RequireAuth("user"), userhandler.RequireActiveUser())
	api.GET("/user/activity", userhandler.GetUserActivity)
	// Public user-scoped changelog read endpoint; private arcade rows are
	// returned only to their owner or strict reviewers.
	api.GET("/user/changelog", userhandler.GetUserChangelog)
	api.GET("/user/feedback", arcadeadmin.ListMySupportFeedback).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
	)
	api.GET("/user/feedback/thread/photo", arcadeadmin.GetSupportFeedbackReplyPhoto).Bind(apis.RequireAuth("user"), userhandler.RequireActiveUser())
	api.GET("/user/feedback/thread", arcadeadmin.GetSupportFeedbackThread).Bind(apis.RequireAuth("user"), userhandler.RequireActiveUser())
	api.POST("/user/feedback/thread", arcadeadmin.ReplySupportFeedback).Bind(apis.RequireAuth("user"), userhandler.RequireActiveUser(), apis.BodyLimit(arcadeadmin.MaxSupportFeedbackBodyBytes))
	api.PATCH("/user/feedback/thread", arcadeadmin.UpdateSupportFeedbackThreadStatus).Bind(apis.RequireAuth("user"), userhandler.RequireActiveUser(), apis.BodyLimit(4096))
	api.GET("/support_feedback", arcadeadmin.ListSupportFeedback).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireStrictReviewerAccess(),
	)
	api.PUT("/support_feedback", arcadeadmin.ReviewSupportFeedback).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireStrictReviewerAccess(),
	)
	api.POST("/support_feedback", arcadeadmin.CreateSupportFeedback).Bind(apis.BodyLimit(arcadeadmin.MaxSupportFeedbackBodyBytes))
	communityhandler.RegisterAccountRoutes(api)

	authArcade := api.Group("/arcade").Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
	)
	authArcade.POST("/new", arcadebasic.NewArcade)
	authArcade.GET("/draft", arcadequery.GetArcadeDraft)
	authArcade.GET("/drafts", arcadequery.ListMyArcadeDrafts)
	authArcade.DELETE("/draft", arcadequery.DeleteMyArcadeDraft)
	authArcade.GET("/public", arcadepublic.PreviewPublicArcade)
	authArcade.GET("/request_admin", arcadeadmin.ListArcadeRequestAdmin)
	authArcade.POST("/request_admin", arcadeadmin.CreateArcadeRequestAdmin)
	authArcade.POST("/edit_report", arcadeadmin.CreateArcadeEditReport)
	authArcade.POST("/rollback", arcadeadmin.RollbackArcadePart)
	authArcade.POST("/game/bulk_version", arcadeadmin.BulkUpdateArcadeGameVersion).Bind(arcadequery.RequireAdminAccess())
	authArcade.PUT("/basic", arcadebasic.UpdateArcadeBasic)
	authArcade.PUT("/public", arcadepublic.RequestPublicArcade)
	authArcade.POST("/location-verification", arcadepublic.VerifyArcadeLocation)
	authArcade.PUT("/gtk", arcadegtk.UpdateArcadeGTK)
	authArcade.PUT("/sns", arcadesns.UpdateArcadeSNS)
	authArcade.PUT("/hour", arcadehour.UpdateArcadeHour)
	authArcade.PUT("/game", arcadegame.UpdateArcadeGame)
	authArcade.PUT("/photo", arcadephoto.UpdateArcadePhoto)
	authArcade.PUT("/memo", arcadememo.UpdateArcadeMemo)
	authArcade.POST("/check-notes", arcadechecknote.Mutate).Bind(apis.BodyLimit(16384))
	authArcade.PUT("/check-notes", arcadechecknote.Mutate).Bind(apis.BodyLimit(16384))
	authArcade.DELETE("/check-notes", arcadechecknote.Mutate).Bind(apis.BodyLimit(16384))
	authArcade.GET("/photo/atoms", arcadephoto.ListArcadePhotoAtoms)
	authArcade.DELETE("/photo/atom", arcadephoto.DeleteArcadePhotoAtom)
	// Allow up to 10 * 20MB photo files (+multipart overhead) in a single request.
	authArcade.POST("/photo/upload", arcadephoto.UploadArcadePhotos).Bind(apis.BodyLimit(220 << 20))
	authArcade.GET("/report-settings", arcadereporting.GetSettings)
	authArcade.PUT("/report-settings", arcadereporting.UpdateSettings).Bind(apis.BodyLimit(64 << 10))
	authArcade.PUT("/inquiry-settings", arcadereporting.UpdateInquirySettings).Bind(apis.BodyLimit(64 << 10))
	authArcade.POST("/report-subscription/status", arcadereporting.SubscriptionStatus).Bind(apis.BodyLimit(4 << 10))
	authArcade.PUT("/report-subscription", arcadereporting.SaveSubscription).Bind(apis.BodyLimit(16 << 10))
	authArcade.DELETE("/report-subscription", arcadereporting.DeleteSubscription).Bind(apis.BodyLimit(4 << 10))
	authArcade.POST("/flag", arcadeflag.CreateArcadeFlag).Bind(apis.BodyLimit(arcadeflag.MaxFlagBodyBytes))
	authArcade.POST("/flag/delete", arcadeflag.DeleteArcadeFlag)
	authArcade.POST("/flag/reaction", arcadeflag.UpdateArcadeFlagReaction)
	api.GET("/arcade/notice", arcadenotice.ListArcadeNotice)
	authArcade.POST("/notice", arcadenotice.CreateArcadeNotice).Bind(apis.BodyLimit(arcadenotice.MaxNoticeBodyBytes))
	authArcade.PUT("/notice", arcadenotice.UpdateArcadeNotice).Bind(apis.BodyLimit(arcadenotice.MaxNoticeBodyBytes))
	authArcade.DELETE("/notice", arcadenotice.DeleteArcadeNotice)
	authArcade.POST("/visit", userhandler.VisitArcade)
	authArcade.PUT("/favorite", userhandler.UpdateArcadeFavorite)
	authArcade.PUT("/favorite/order", userhandler.UpdateArcadeFavoriteOrder)
	api.POST("/campaign/check", arcadecampaign.CheckCampaign).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
	)

	authUser := api.Group("/user").Bind(apis.RequireAuth("user"))

	authNotifications := api.Group("/user/notifications").Bind(apis.RequireAuth("user"), userhandler.RequireActiveUser())
	authNotifications.GET("", usernotifications.List)
	authNotifications.POST("/read", usernotifications.Read).Bind(apis.BodyLimit(4096))
	authNotifications.POST("/device", usernotifications.DeviceStatus).Bind(apis.BodyLimit(4096))
	authNotifications.PATCH("/device", usernotifications.SyncDeviceLocale).Bind(apis.BodyLimit(4096))
	authNotifications.PUT("/device", usernotifications.SaveDevice).Bind(apis.BodyLimit(16384))
	authNotifications.DELETE("/device", usernotifications.DeleteDevice).Bind(apis.BodyLimit(4096))
	authUser.GET("/me", userhandler.GetMe)
	authUser.PUT("/profile", userhandler.UpdateProfile).Bind(userhandler.RequireActiveUser(), apis.BodyLimit(35<<20))
	authUser.PUT("/game-preferences", userhandler.UpdateGamePreferences).Bind(userhandler.RequireActiveUser(), apis.BodyLimit(128<<10))
	authUser.POST("/check-in", userhandler.CheckIn).Bind(userhandler.RequireActiveUser())
	api.GET("/user/passport", userhandler.GetMyPassport)
	api.GET("/user/passport/stamps", userhandler.GetMyPassportStamps)
	authUser.GET("/visits", userhandler.GetMyVisits).Bind(userhandler.RequireActiveUser())
	authUser.PUT("/countries", userhandler.UpdateCountries).Bind(userhandler.RequireActiveUser())
	authUser.PUT("/visit-visibility", userhandler.UpdateVisitVisibility).Bind(userhandler.RequireActiveUser())
	authUser.PUT("/favorite-visibility", userhandler.UpdateFavoriteVisibility).Bind(userhandler.RequireActiveUser())
	authUser.GET("/report", arcadeadmin.ListUserReport).Bind(userhandler.RequireActiveUser())
	authUser.POST("/report", arcadeadmin.CreateUserReport).Bind(userhandler.RequireActiveUser())

	reviewQueue := api.Group("/moderation/arcade").Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireStrictReviewerAccess(),
	)
	supporterReview := api.Group("/moderation/supporter").Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireStrictReviewerAccess(),
	)
	supporterReview.GET("/requests", arcadeadmin.ListSupporterRequests)
	supporterReview.GET("/request", arcadeadmin.GetSupporterRequest)
	supporterReview.PUT("/request", arcadeadmin.ReviewSupporterRequest)
	reviewQueue.GET("/requests", arcadeadmin.ListModerationRequests)
	reviewQueue.PUT("/request", arcadeadmin.ReviewModerationRequest)
	reviewQueue.GET("/status", arcadeadmin.GetArcadeStatus)
	reviewQueue.PUT("/status", arcadeadmin.UpdateArcadeStatus)
	reviewQueue.GET("/edit-reports", arcadeadmin.ListArcadeEditReports)
	reviewQueue.PUT("/edit-report", arcadeadmin.ReviewArcadeEditReport)

	userReview := api.Group("/moderation/user").Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireStrictReviewerAccess(),
	)
	userReview.GET("/reports", arcadeadmin.ListAllUserReports)
	userReview.PUT("/report", arcadeadmin.ReviewUserReport)
	userReview.GET("/bans", userhandler.ListUserBans)
	userReview.POST("/ban", userhandler.CreateOrUpdateUserBan)
	userReview.DELETE("/ban", userhandler.DeleteUserBan)

	authSupporter := api.Group("/supporter").Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
	)
	authSupporter.GET("/score", arcadeadmin.GetSupporterScore)
	authSupporter.POST("/request", arcadeadmin.CreateSupporterRequest)
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
