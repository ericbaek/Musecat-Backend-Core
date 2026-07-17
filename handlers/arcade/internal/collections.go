package arcadeinternal

// Centralized collection names to avoid scattering string literals.
const (
	CollectionArcade          = "arcade"
	CollectionArcadeBasic     = "arcade_basic"
	CollectionArcadeHour      = "arcade_hour"
	CollectionArcadeSNS       = "arcade_sns"
	CollectionArcadeSNSAtoms  = "arcade_sns_atoms"
	CollectionArcadeGTK       = "arcade_gtk"
	CollectionArcadeGTKAtoms  = "arcade_gtk_atoms"
	CollectionArcadeGame      = "arcade_game"
	CollectionArcadeGameAtoms = "arcade_game_atoms"
	// Game entry is the durable installation identity. Batches and revisions are
	// immutable snapshots selected by arcade.game_state.
	CollectionArcadeGameEntry          = "arcade_game_entry"
	CollectionArcadeGameRevisionBatch  = "arcade_game_revision_batch"
	CollectionArcadeGameRevision       = "arcade_game_revision"
	CollectionArcadeGameLegacyMap      = "arcade_game_legacy_map"
	CollectionArcadeGameMigrationIssue = "arcade_game_migration_issue"
	CollectionArcadePhoto              = "arcade_photo"
	CollectionArcadePhotoAtoms         = "arcade_photo_atoms"
	CollectionArcadeFlag               = "arcade_flag"
	CollectionArcadeFlagReaction       = "arcade_flag_reaction"
	CollectionArcadeNotice             = "arcade_notice"
	CollectionArcadeTicket             = "arcade_ticket_request"
	CollectionArcadeRequestAdmin       = "arcade_request_admin"
	CollectionSupporterRequest         = "supporter_request"
	CollectionSupportFeedback          = "support_feedback"
	CollectionArcadeChangelog          = "arcade_changelog"
	CollectionGameSeriesVersion        = "game_series_version"
	CollectionGameSeries               = "game_series"
)
