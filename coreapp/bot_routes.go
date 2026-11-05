package coreapp

import (
	"github.com/ericbaek/musecat-backend-core/handlers"
	arcadebasic "github.com/ericbaek/musecat-backend-core/handlers/arcade/basic"
	arcadegame "github.com/ericbaek/musecat-backend-core/handlers/arcade/game"
	arcadegtk "github.com/ericbaek/musecat-backend-core/handlers/arcade/gtk"
	arcadehour "github.com/ericbaek/musecat-backend-core/handlers/arcade/hour"
	arcadephoto "github.com/ericbaek/musecat-backend-core/handlers/arcade/photo"
	arcadepublic "github.com/ericbaek/musecat-backend-core/handlers/arcade/public"
	arcadequery "github.com/ericbaek/musecat-backend-core/handlers/arcade/query"
	arcadesns "github.com/ericbaek/musecat-backend-core/handlers/arcade/sns"
	search "github.com/ericbaek/musecat-backend-core/handlers/search"
	user "github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/ericbaek/musecat-backend-core/service/botaccess"
	"github.com/ericbaek/musecat-backend-core/service/contribution"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

// The bot namespace is deliberately limited to arcade creation and information
// edits. Registering a web/partner route never automatically exposes it to bots.
func registerBotRoutes(se *core.ServeEvent) {
	// Raw photo metadata is publicly readable. Stored attribution must only be
	// serialized through the viewer-aware custom history endpoints.
	se.App.OnRecordEnrich("arcade", "arcade_changelog", "arcade_photo_atoms").Bind(&hook.Handler[*core.RecordEnrichEvent]{
		Id: "__botContributionPrivacy__",
		Func: func(e *core.RecordEnrichEvent) error {
			e.Record.Hide(contribution.Field)
			return e.Next()
		},
	})
	keys := se.Router.Group("/user/bot-keys").Bind(apis.RequireAuth("user"), user.RequireActiveUser(), apis.BodyLimit(4096))
	keys.GET("", botaccess.List)
	keys.POST("", botaccess.Create)
	keys.DELETE("/{id}", botaccess.Revoke)

	api := se.Router.Group("/bot/v1").Bind(botaccess.RequireKey(), user.RequireActiveUser(), apis.BodyLimit(1<<20))
	api.GET("/search", search.Search)
	api.GET("/geocode", handlers.GeocodeHandler)
	api.GET("/reverse_geocode", handlers.ReverseGeocodeHandler)
	api.GET("/game/catalog", arcadequery.GetGameCatalog)
	api.GET("/arcades/nearby", arcadequery.ListArcadesBySeriesAndLocation)
	api.GET("/arcade", arcadequery.GetArcadeValues)
	api.GET("/arcade/draft", arcadequery.GetArcadeDraft)
	api.GET("/arcade/drafts", arcadequery.ListMyArcadeDrafts)
	api.GET("/arcade/changelog", arcadequery.ListArcadeChangelog)
	api.GET("/arcade/public", arcadepublic.PreviewPublicArcade)
	api.GET("/arcade/photo/atoms", arcadephoto.ListArcadePhotoAtoms)
	api.GET("/arcade/photo/file", arcadephoto.DownloadArcadePhotoAtom)
	api.POST("/arcade/new", arcadebasic.NewArcade)
	api.PUT("/arcade/basic", arcadebasic.UpdateArcadeBasic)
	api.PUT("/arcade/hour", arcadehour.UpdateArcadeHour)
	api.PUT("/arcade/gtk", arcadegtk.UpdateArcadeGTK)
	api.PUT("/arcade/sns", arcadesns.UpdateArcadeSNS)
	api.PUT("/arcade/game", arcadegame.UpdateArcadeGame)
	api.PUT("/arcade/photo", arcadephoto.UpdateArcadePhoto)
	api.POST("/arcade/photo/upload", arcadephoto.UploadArcadePhotos).Bind(apis.BodyLimit(220 << 20))
	api.PUT("/arcade/public", arcadepublic.RequestPublicArcade)
}
