package coreapp

import (
	"github.com/ericbaek/musecat-backend-core/handlers"
	arcadeanalytics "github.com/ericbaek/musecat-backend-core/handlers/arcade/analytics"
	arcadebasic "github.com/ericbaek/musecat-backend-core/handlers/arcade/basic"
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
	messaginghandler "github.com/ericbaek/musecat-backend-core/handlers/messaging"
	search "github.com/ericbaek/musecat-backend-core/handlers/search"
	user "github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/ericbaek/musecat-backend-core/service/botaccess"
	"github.com/ericbaek/musecat-backend-core/service/contribution"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

// The automation namespace explicitly lists arcade and official-account operations.
// Registering a web/partner route never automatically exposes it to API keys.
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
	api.GET("/arcade/analytics", arcadeanalytics.GetArcadeAnalytics)
	api.GET("/arcade/memo", arcadememo.GetArcadeMemo)
	api.PUT("/arcade/memo", arcadememo.UpdateArcadeMemo)
	api.GET("/arcade/notice", arcadenotice.ListArcadeNotice)
	api.POST("/arcade/notice", arcadenotice.CreateArcadeNotice).Bind(apis.BodyLimit(arcadenotice.MaxNoticeBodyBytes))
	api.PUT("/arcade/notice", arcadenotice.UpdateArcadeNotice).Bind(apis.BodyLimit(arcadenotice.MaxNoticeBodyBytes))
	api.DELETE("/arcade/notice", arcadenotice.DeleteArcadeNotice)
	api.GET("/arcade/report-settings", arcadereporting.GetSettings)
	api.PUT("/arcade/report-settings", arcadereporting.UpdateSettings).Bind(apis.BodyLimit(64 << 10))
	api.PUT("/arcade/inquiry-settings", arcadereporting.UpdateInquirySettings).Bind(apis.BodyLimit(64 << 10))
	api.GET("/user/arcade-inquiry-onboarding", arcadereporting.InquiryOnboarding)
	api.POST("/arcade/flag/reaction", arcadeflag.UpdateArcadeFlagReaction)
	api.GET("/user/inquiry-bots/arcade/{id}", messaginghandler.GetArcadeBot)
	api.PUT("/user/inquiry-bots/arcade/{id}", messaginghandler.SetArcadeBot).Bind(apis.BodyLimit(64 << 10))
	api.GET("/user/conversations", messaginghandler.List)
	api.GET("/user/conversations/{id}", messaginghandler.Get)
	api.GET("/user/conversations/{id}/messages", messaginghandler.History)
	api.POST("/user/conversations/{id}/messages", messaginghandler.Send).Bind(apis.BodyLimit(messaginghandler.MaxBodyBytes))
	api.PUT("/user/conversations/{id}/read", messaginghandler.Read).Bind(apis.BodyLimit(4096))
	api.PATCH("/user/conversations/{id}/inquiry", messaginghandler.Status).Bind(apis.BodyLimit(4096))
	api.GET("/user/conversations/{id}/attachments/{messageId}/{file}", messaginghandler.Photo)
}
