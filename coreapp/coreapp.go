package coreapp

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/plugins/migratecmd"

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

const (
	documentationSpecPath    = "docs/openapi.yaml"
	documentationSpecPathEnv = "MUSECAT_OPENAPI_SPEC_PATH"
	documentationSiteDir     = "docs-site"
)

func Configure(app *pocketbase.PocketBase, autoMigrate bool) {
	migratecmd.MustRegister(app, app.RootCmd, migratecmd.Config{
		// enable auto creation of migration files when making collection changes in the Dashboard
		// (production environment keeps this off unless explicitly overridden)
		Automigrate: autoMigrate,
	})
	arcadeversion.RegisterHooks(app)
	arcadequery.RegisterCandidateSnapshotHooks(app)
	arcadeflag.RegisterAutoSolveCron(app)
	arcadeflag.RegisterAutoSolveReactionCreateHook(app)
	communityhandler.RegisterTranslationCron(app)
	userhandler.RegisterHooks(app)
	// arcade.RegisterArcadeChangelogHook(app)

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		RegisterDocumentationRoutes(se)

		se.Router.GET("/hello", handlers.HelloHandler)
		se.Router.GET("/geo", handlers.GeoLookupHandler)
		se.Router.GET("/geocode", handlers.GeocodeHandler)
		se.Router.GET("/reverse_geocode", handlers.ReverseGeocodeHandler)
		se.Router.GET("/search", searchhandler.Search)
		se.Router.GET("/stats", statshandler.GetStats)
		se.Router.GET("/rankings", rankinghandler.List)
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
		se.Router.GET("/arcade/campaigns", arcadecampaign.ListArcadeCampaigns)
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
		catalogManagement.GET("/catalog/changes", gamecataloghandler.ListChanges)
		catalogManagement.POST("/catalog/changes/revert", gamecataloghandler.Revert)
		// Public user profile read endpoint
		se.Router.GET("/user", userhandler.GetUserByID)
		se.Router.GET("/user/activity", userhandler.GetUserActivity)
		// Public user-scoped changelog read endpoint; private arcade rows are
		// returned only to their owner or strict reviewers.
		se.Router.GET("/user/changelog", userhandler.GetUserChangelog)
		se.Router.GET("/support_feedback", arcadeadmin.ListSupportFeedback)
		se.Router.POST("/support_feedback", arcadeadmin.CreateSupportFeedback)
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
		authArcade.POST("/game/bulk_version", arcadeadmin.BulkUpdateArcadeGameVersion).Bind(arcadequery.RequireGameToolsAccess())
		authArcade.PUT("/basic", arcadebasic.UpdateArcadeBasic)
		authArcade.PUT("/public", arcadepublic.RequestPublicArcade)
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
		authArcade.POST("/flag", arcadeflag.CreateArcadeFlag)
		authArcade.POST("/flag/delete", arcadeflag.DeleteArcadeFlag)
		authArcade.POST("/flag/reaction", arcadeflag.UpdateArcadeFlagReaction)
		se.Router.GET("/arcade/notice", arcadenotice.ListArcadeNotice)
		authArcade.POST("/notice", arcadenotice.CreateArcadeNotice)
		authArcade.PUT("/notice", arcadenotice.UpdateArcadeNotice)
		authArcade.DELETE("/notice", arcadenotice.DeleteArcadeNotice)
		authArcade.POST("/nearby", nil)
		authArcade.POST("/visit", userhandler.VisitArcade)
		se.Router.POST("/campaign/check", arcadecampaign.CheckCampaign).Bind(
			apis.RequireAuth("user"),
			userhandler.RequireActiveUser(),
		)

		authUser := se.Router.Group("/user").Bind(apis.RequireAuth("user"))
		authUser.GET("/me", userhandler.GetMe)
		authUser.POST("/signup", userhandler.SignUp)
		authUser.POST("/check-in", userhandler.CheckIn).Bind(userhandler.RequireActiveUser())
		authUser.GET("/visits", userhandler.GetMyVisits).Bind(userhandler.RequireActiveUser())
		authUser.PUT("/visit-visibility", userhandler.UpdateVisitVisibility).Bind(userhandler.RequireActiveUser())
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

		return se.Next()
	})

}

func RegisterDocumentationRoutes(se *core.ServeEvent) {
	docsAuth := docsBasicAuthConfigFromEnv()

	se.Router.GET("/openapi.yaml", func(re *core.RequestEvent) error {
		if err := docsAuth.authorize(re); err != nil {
			return err
		}

		spec, err := os.ReadFile(resolveDocumentationSpecPath())
		if err != nil {
			return re.JSON(http.StatusInternalServerError, map[string]any{
				"error":   "failed to load OpenAPI spec",
				"details": err.Error(),
			})
		}

		return re.Blob(http.StatusOK, "application/yaml; charset=utf-8", spec)
	})

	se.Router.GET("/docs", func(re *core.RequestEvent) error {
		if err := docsAuth.authorize(re); err != nil {
			return err
		}

		return re.Redirect(http.StatusMovedPermanently, "/docs/")
	})

	se.Router.GET("/docs/{path...}", func(re *core.RequestEvent) error {
		if err := docsAuth.authorize(re); err != nil {
			return err
		}

		return apis.Static(os.DirFS(documentationSiteDir), true)(re)
	})
}

func resolveDocumentationSpecPath() string {
	if path := strings.TrimSpace(os.Getenv(documentationSpecPathEnv)); path != "" {
		return path
	}

	return documentationSpecPath
}

type docsBasicAuthConfig struct {
	enabled  bool
	username string
	password string
}

func docsBasicAuthConfigFromEnv() docsBasicAuthConfig {
	username := strings.TrimSpace(os.Getenv("DOCS_BASIC_AUTH_USER"))
	password := strings.TrimSpace(os.Getenv("DOCS_BASIC_AUTH_PASS"))

	return docsBasicAuthConfig{
		enabled:  username != "" && password != "",
		username: username,
		password: password,
	}
}

func (c docsBasicAuthConfig) authorize(re *core.RequestEvent) error {
	if !c.enabled {
		return nil
	}

	username, password, ok := re.Request.BasicAuth()
	if ok &&
		subtle.ConstantTimeCompare([]byte(username), []byte(c.username)) == 1 &&
		subtle.ConstantTimeCompare([]byte(password), []byte(c.password)) == 1 {
		return nil
	}

	re.Response.Header().Set("WWW-Authenticate", `Basic realm="Delta-DB Docs"`)
	return re.String(http.StatusUnauthorized, "Unauthorized")
}
