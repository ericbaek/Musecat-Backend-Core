package coreapp

import (
	"crypto/subtle"
	"net/http"
	"os"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"

	"github.com/ericbaek/musecat-backend-core/docs"
	"github.com/ericbaek/musecat-backend-core/geo"
	"github.com/ericbaek/musecat-backend-core/handlers"
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
	arcadesns "github.com/ericbaek/musecat-backend-core/handlers/arcade/sns"
	arcadeversion "github.com/ericbaek/musecat-backend-core/handlers/arcade/version"
	communityhandler "github.com/ericbaek/musecat-backend-core/handlers/community"
	gamecataloghandler "github.com/ericbaek/musecat-backend-core/handlers/gamecatalog"
	rankinghandler "github.com/ericbaek/musecat-backend-core/handlers/ranking"
	searchhandler "github.com/ericbaek/musecat-backend-core/handlers/search"
	statshandler "github.com/ericbaek/musecat-backend-core/handlers/stats"
	subwayhandler "github.com/ericbaek/musecat-backend-core/handlers/subway"
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

// Config contains dependencies selected by the executable. Configure never reads
// environment variables, registers migrations, or starts scheduled jobs.
// A nil GeoResolver deliberately disables geographic lookup (fail closed).
type Config struct {
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
		RegisterAPIRoutes(se)
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
		{Label: "GET /geocode", MaxRequests: 30, Duration: 60, Audience: core.RateLimitRuleAudienceAll},
		{Label: "GET /reverse_geocode", MaxRequests: 30, Duration: 60, Audience: core.RateLimitRuleAudienceAll},
		{Label: "GET /geo", MaxRequests: 60, Duration: 60, Audience: core.RateLimitRuleAudienceAll},
		{Label: "POST /support_feedback", MaxRequests: 10, Duration: 600, Audience: core.RateLimitRuleAudienceAll},
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
	se.Router.GET("/hello", handlers.HelloHandler)
	se.Router.GET("/geo", handlers.GeoLookupHandler)
	se.Router.GET("/geocode", handlers.GeocodeHandler)
	se.Router.GET("/reverse_geocode", handlers.ReverseGeocodeHandler)
	se.Router.GET("/search", searchhandler.Search)
	se.Router.GET("/stats", statshandler.GetStats)
	se.Router.GET("/rankings", rankinghandler.List)
	se.Router.GET("/arcade/ranking", rankinghandler.ArcadeVisitRanking)
	se.Router.GET("/subway/map", subwayhandler.GetMap)
	se.Router.GET("/subway/map/file", subwayhandler.DownloadMapFile)
	se.Router.POST("/subway/map", subwayhandler.CreateMap).Bind(
		apis.BodyLimit(24<<20),
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireAdminAccess(),
	)
	se.Router.PUT("/subway/map", subwayhandler.UpdateMap).Bind(
		apis.BodyLimit(24<<20),
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireAdminAccess(),
	)
	se.Router.DELETE("/subway/map", subwayhandler.DeleteMap).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireAdminAccess(),
	)
	// Public read endpoint: returns current relation ids for the arcade
	se.Router.GET("/arcade", arcadequery.GetArcadeValues)
	se.Router.GET("/arcade/analytics", arcadeanalytics.GetArcadeAnalytics)
	se.Router.POST("/arcade/analytics/event", arcadeanalytics.RecordDirectionClick)
	// Public changelog rows are exposed only through this custom API.
	se.Router.GET("/arcade/changelog", arcadequery.ListArcadeChangelog)
	se.Router.GET("/arcade/memo", arcadememo.GetArcadeMemo)
	se.Router.GET("/arcade/photo/file", arcadephoto.DownloadArcadePhotoAtom)
	// Public read endpoint: list all arcades with basic info + gameSeries ids
	se.Router.GET("/arcades", arcadequery.ListArcades)
	se.Router.GET("/arcades/updates", arcadequery.ListArcadeUpdates)
	se.Router.GET("/campaigns", arcadecampaign.ListCampaigns)
	se.Router.GET("/campaign", arcadecampaign.GetCampaign)
	se.Router.GET("/campaign/photo", arcadecampaign.ListPhotoCampaign)
	se.Router.GET("/arcade/campaigns/presence", arcadecampaign.ListArcadeCampaignPresence)
	se.Router.GET("/arcade/campaigns", arcadecampaign.ListArcadeCampaigns).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
	)
	se.Router.POST("/campaign", arcadecampaign.CreateCampaign).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireAdminAccess(),
	)
	se.Router.PUT("/campaign", arcadecampaign.UpdateCampaign).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireAdminAccess(),
	)
	se.Router.POST("/campaign/end", arcadecampaign.EndCampaign).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireAdminAccess(),
	)
	// Public read endpoint: list arcades by game series near a location with pagination
	se.Router.GET("/arcades/nearby", arcadequery.ListArcadesBySeriesAndLocation)
	se.Router.GET("/arcade/visits", userhandler.GetArcadeVisitStats)
	// Public read endpoint: list machine rows filtered by country, game series, and version
	se.Router.GET("/arcade/games", arcadequery.ListArcadeGames).Bind(
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
	catalogManagement.GET("/catalog", gamecataloghandler.GetCatalog)
	catalogManagement.POST("/catalog", gamecataloghandler.Create)
	catalogManagement.PUT("/catalog", gamecataloghandler.Update)
	catalogManagement.DELETE("/catalog", gamecataloghandler.Archive)
	catalogManagement.POST("/catalog/restore", gamecataloghandler.Restore)
	catalogManagement.POST("/catalog/compatibilities", gamecataloghandler.ReplaceCompatibilities)
	catalogManagement.GET("/catalog/changes", gamecataloghandler.ListChanges)
	catalogManagement.POST("/catalog/changes/revert", gamecataloghandler.Revert)
	// Public user profile read endpoint
	se.Router.GET("/user", userhandler.GetUserByID)
	se.Router.GET("/user/activity", userhandler.GetUserActivity)
	// Public user-scoped changelog read endpoint; private arcade rows are
	// returned only to their owner or strict reviewers.
	se.Router.GET("/user/changelog", userhandler.GetUserChangelog)
	se.Router.GET("/user/feedback", arcadeadmin.ListMySupportFeedback).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
	)
	se.Router.GET("/support_feedback", arcadeadmin.ListSupportFeedback).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireStrictReviewerAccess(),
	)
	se.Router.POST("/support_feedback", arcadeadmin.CreateSupportFeedback).Bind(apis.BodyLimit(arcadeadmin.MaxSupportFeedbackBodyBytes))
	communityhandler.RegisterRoutes(se)

	authArcade := se.Router.Group("/arcade").Bind(
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
	authArcade.GET("/photo/atoms", arcadephoto.ListArcadePhotoAtoms)
	authArcade.DELETE("/photo/atom", arcadephoto.DeleteArcadePhotoAtom)
	// Allow up to 10 * 20MB photo files (+multipart overhead) in a single request.
	authArcade.POST("/photo/upload", arcadephoto.UploadArcadePhotos).Bind(apis.BodyLimit(220 << 20))
	authArcade.POST("/flag", arcadeflag.CreateArcadeFlag).Bind(apis.BodyLimit(arcadeflag.MaxFlagBodyBytes))
	authArcade.POST("/flag/delete", arcadeflag.DeleteArcadeFlag)
	authArcade.POST("/flag/reaction", arcadeflag.UpdateArcadeFlagReaction)
	se.Router.GET("/arcade/notice", arcadenotice.ListArcadeNotice)
	authArcade.POST("/notice", arcadenotice.CreateArcadeNotice).Bind(apis.BodyLimit(arcadenotice.MaxNoticeBodyBytes))
	authArcade.PUT("/notice", arcadenotice.UpdateArcadeNotice).Bind(apis.BodyLimit(arcadenotice.MaxNoticeBodyBytes))
	authArcade.DELETE("/notice", arcadenotice.DeleteArcadeNotice)
	authArcade.POST("/visit", userhandler.VisitArcade)
	authArcade.PUT("/favorite", userhandler.UpdateArcadeFavorite)
	authArcade.PUT("/favorite/order", userhandler.UpdateArcadeFavoriteOrder)
	se.Router.POST("/campaign/check", arcadecampaign.CheckCampaign).Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
	)

	authUser := se.Router.Group("/user").Bind(apis.RequireAuth("user"))
	authUser.GET("/me", userhandler.GetMe)
	authUser.PUT("/profile", userhandler.UpdateProfile).Bind(userhandler.RequireActiveUser(), apis.BodyLimit(35<<20))
	authUser.PUT("/game-preferences", userhandler.UpdateGamePreferences).Bind(userhandler.RequireActiveUser(), apis.BodyLimit(128<<10))
	authUser.POST("/signup", userhandler.SignUp)
	authUser.POST("/check-in", userhandler.CheckIn).Bind(userhandler.RequireActiveUser())
	se.Router.GET("/user/passport", userhandler.GetMyPassport)
	se.Router.GET("/user/passport/stamps", userhandler.GetMyPassportStamps)
	authUser.GET("/visits", userhandler.GetMyVisits).Bind(userhandler.RequireActiveUser())
	authUser.PUT("/countries", userhandler.UpdateCountries).Bind(userhandler.RequireActiveUser())
	authUser.PUT("/visit-visibility", userhandler.UpdateVisitVisibility).Bind(userhandler.RequireActiveUser())
	authUser.PUT("/favorite-visibility", userhandler.UpdateFavoriteVisibility).Bind(userhandler.RequireActiveUser())
	authUser.POST("/withdraw", userhandler.Withdraw)
	authUser.GET("/report", arcadeadmin.ListUserReport).Bind(userhandler.RequireActiveUser())
	authUser.POST("/report", arcadeadmin.CreateUserReport).Bind(userhandler.RequireActiveUser())

	reviewQueue := se.Router.Group("/moderation/arcade").Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
		arcadequery.RequireStrictReviewerAccess(),
	)
	reviewQueue.GET("/edit-reports", arcadeadmin.ListArcadeEditReports)
	reviewQueue.PUT("/edit-report", arcadeadmin.ReviewArcadeEditReport)

	authSupporter := se.Router.Group("/supporter").Bind(
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
