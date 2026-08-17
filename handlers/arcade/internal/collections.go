package arcadeinternal

// Centralized collection names to avoid scattering string literals.
const (
	CollectionArcade         = "arcade"
	CollectionArcadeBasic    = "arcade_basic"
	CollectionArcadeHour     = "arcade_hour"
	CollectionArcadeSNS      = "arcade_sns"
	CollectionArcadeSNSAtoms = "arcade_sns_atoms"
	CollectionArcadeGTK      = "arcade_gtk"
	CollectionArcadeGTKAtoms = "arcade_gtk_atoms"
	// Game entry is the durable installation identity. Batches and revisions are
	// immutable snapshots selected by arcade.game_v2.
	CollectionArcadeGameEntry          = "arcade_game_id"
	CollectionArcadeGameRevisionBatch  = "arcade_game_history_batch"
	CollectionArcadeGameRevision       = "arcade_game_history"
	CollectionArcadePhoto              = "arcade_photo"
	CollectionArcadePhotoAtoms         = "arcade_photo_atoms"
	CollectionArcadeFlag               = "arcade_flag"
	CollectionArcadeFlagReaction       = "arcade_flag_reaction"
	CollectionArcadeNotice             = "arcade_notice"
	CollectionArcadeMemo               = "arcade_memo"
	CollectionArcadeTicket             = "arcade_ticket_request"
	CollectionArcadeRequestAdmin       = "arcade_request_admin"
	CollectionArcadeAnalyticsEvent     = "arcade_analytics_event"
	CollectionSupporterRequest         = "supporter_request"
	CollectionSupportFeedback          = "support_feedback"
	CollectionArcadeChangelog          = "arcade_changelog"
	CollectionGameSeriesVersion        = "game_series_version"
	CollectionGameSeries               = "game_series"
	CollectionGameCabinet              = "game_cabinet"
	CollectionGameSeriesVersionCabinet = "game_series_version_cabinet"
)
