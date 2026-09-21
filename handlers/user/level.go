package user

import (
	"time"

	"github.com/ericbaek/musecat-backend-core/service/xp"
)

type UserLevelState = xp.UserLevelState
type ExpFeedback = xp.ExpFeedback
type ArcadePublicExpPreview = xp.ArcadePublicExpPreview

func attendanceNow() time.Time {
	return xp.AttendanceNow()
}

var (
	SetAttendanceNowForTest     = xp.SetAttendanceNowForTest
	LevelFromExp                = xp.LevelFromExp
	NextLevelExp                = xp.NextLevelExp
	LevelBaseExp                = xp.LevelBaseExp
	ExpToNextLevel              = xp.ExpToNextLevel
	BuildExpFeedback            = xp.BuildExpFeedback
	CheckInKind                 = xp.CheckInKind
	ArcadePublicKind            = xp.ArcadePublicKind
	ArcadeEditKind              = xp.ArcadeEditKind
	ArcadePublicBackfillKind    = xp.ArcadePublicBackfillKind
	ArcadeEditGrantKind         = xp.ArcadeEditGrantKind
	ArcadeGameEditGrantKind     = xp.ArcadeGameEditGrantKind
	ArcadeGameEditExp           = xp.ArcadeGameEditExp
	ArcadePhotoSubmissionKind   = xp.ArcadePhotoSubmissionKind
	ArcadePhotoGrantKind        = xp.ArcadePhotoGrantKind
	ArcadeVisitKind             = xp.ArcadeVisitKind
	FlagKind                    = xp.FlagKind
	FlagReactionKind            = xp.FlagReactionKind
	KSTDay                      = xp.KSTDay
	LoadCurrentExp              = xp.LoadCurrentExp
	IsExpEligible               = xp.IsExpEligible
	HasLevelLogKind             = xp.HasLevelLogKind
	LoadUserLevelState          = xp.LoadUserLevelState
	AwardExpTx                  = xp.AwardExpTx
	AwardArcadeEditExpTx        = xp.AwardArcadeEditExpTx
	AwardArcadeGameEditExpTx    = xp.AwardArcadeGameEditExpTx
	AwardArcadePhotoExpTx       = xp.AwardArcadePhotoExpTx
	PreviewArcadePublicExp      = xp.PreviewArcadePublicExp
	GrantArcadePublicBackfillTx = xp.GrantArcadePublicBackfillTx
	LoadAttendanceExp           = xp.LoadAttendanceExp
	parseLevelLogCreated        = xp.ParseLevelLogCreated
)
