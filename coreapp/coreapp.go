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
	arcadebasic "github.com/ericbaek/musecat-backend-core/handlers/arcade/basic"
	arcadeflag "github.com/ericbaek/musecat-backend-core/handlers/arcade/flag"
	arcadegame "github.com/ericbaek/musecat-backend-core/handlers/arcade/game"
	arcadegtk "github.com/ericbaek/musecat-backend-core/handlers/arcade/gtk"
	arcadehour "github.com/ericbaek/musecat-backend-core/handlers/arcade/hour"
	arcadenotice "github.com/ericbaek/musecat-backend-core/handlers/arcade/notice"
	arcadephoto "github.com/ericbaek/musecat-backend-core/handlers/arcade/photo"
	arcadepublic "github.com/ericbaek/musecat-backend-core/handlers/arcade/public"
	arcadequery "github.com/ericbaek/musecat-backend-core/handlers/arcade/query"
	arcadesns "github.com/ericbaek/musecat-backend-core/handlers/arcade/sns"
	arcadeversion "github.com/ericbaek/musecat-backend-core/handlers/arcade/version"
	searchhandler "github.com/ericbaek/musecat-backend-core/handlers/search"
	statshandler "github.com/ericbaek/musecat-backend-core/handlers/stats"
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

const (
	documentationSpecPath = "docs/openapi.yaml"
	documentationSiteDir  = "docs-site"
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
		// Public read endpoint: returns current relation ids for the arcade
		se.Router.GET("/arcade", arcadequery.GetArcadeValues)
		// Public read endpoint: list all arcades with basic info + gameSeries ids
		se.Router.GET("/arcades", arcadequery.ListArcades)
		se.Router.GET("/arcades/updates", arcadequery.ListArcadeUpdates)
		// Public read endpoint: list arcades by game series near a location with pagination
		se.Router.GET("/arcades/nearby", arcadequery.ListArcadesBySeriesAndLocation)
		se.Router.GET("/arcade/visits", userhandler.GetArcadeVisitStats)
		// Public read endpoint: list machine rows filtered by country, game series, and version
		se.Router.GET("/arcade/games", arcadequery.ListArcadeGames).Bind(
			apis.RequireAuth("user"),
			arcadequery.RequireModeratorAccess(),
		)
		// Public read endpoint: returns game_series_version and its series
		se.Router.GET("/game_series_version", arcadequery.GetGameSeriesVersion)
		se.Router.POST("/game_series_version", arcadequery.CreateGameSeriesVersion).Bind(
			apis.RequireAuth("user"),
			arcadequery.RequireModeratorAccess(),
		)
		se.Router.PUT("/game_series_version", arcadequery.UpdateGameSeriesVersion).Bind(
			apis.RequireAuth("user"),
			arcadequery.RequireModeratorAccess(),
		)
		// Public user profile read endpoint
		se.Router.GET("/user", userhandler.GetUserByID)
		se.Router.GET("/user/activity", userhandler.GetUserActivity)
		se.Router.GET("/support_feedback", arcadeadmin.ListSupportFeedback)
		se.Router.POST("/support_feedback", arcadeadmin.CreateSupportFeedback)

		authArcade := se.Router.Group("/arcade").Bind(
			apis.RequireAuth("user"),
			userhandler.RequireActiveUser(),
		)
		authArcade.POST("/new", arcadebasic.NewArcade)
		authArcade.GET("/request_admin", arcadeadmin.ListArcadeRequestAdmin)
		authArcade.POST("/request_admin", arcadeadmin.CreateArcadeRequestAdmin)
		authArcade.POST("/rollback", arcadeadmin.RollbackArcadePart)
		authArcade.POST("/game/bulk_version", arcadeadmin.BulkUpdateArcadeGameVersion).Bind(arcadequery.RequireModeratorAccess())
		authArcade.POST("/game/rollback", arcadeadmin.RollbackArcadeGameUncertain)
		authArcade.POST("/game/confirm", arcadeadmin.ConfirmArcadeGameUncertain)
		authArcade.POST("/game/information/confirm", arcadegame.ConfirmArcadeGameInformation)
		authArcade.PUT("/basic", arcadebasic.UpdateArcadeBasic)
		authArcade.PUT("/public", arcadepublic.RequestPublicArcade)
		authArcade.PUT("/gtk", arcadegtk.UpdateArcadeGTK)
		authArcade.PUT("/sns", arcadesns.UpdateArcadeSNS)
		authArcade.PUT("/hour", arcadehour.UpdateArcadeHour)
		authArcade.PUT("/game", arcadegame.UpdateArcadeGame)
		authArcade.PUT("/photo", arcadephoto.UpdateArcadePhoto)
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

		authUser := se.Router.Group("/user").Bind(apis.RequireAuth("user"))
		authUser.GET("/me", userhandler.GetMe)
		authUser.POST("/signup", userhandler.SignUp)
		authUser.POST("/check-in", userhandler.CheckIn).Bind(userhandler.RequireActiveUser())
		authUser.GET("/visits", userhandler.GetMyVisits).Bind(userhandler.RequireActiveUser())
		authUser.PUT("/visit-visibility", userhandler.UpdateVisitVisibility).Bind(userhandler.RequireActiveUser())
		authUser.POST("/withdraw", userhandler.Withdraw)
		authUser.GET("/report", arcadeadmin.ListUserReport).Bind(userhandler.RequireActiveUser())
		authUser.POST("/report", arcadeadmin.CreateUserReport).Bind(userhandler.RequireActiveUser())

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

		spec, err := os.ReadFile(documentationSpecPath)
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
