package usernotification

import (
	"database/sql"
	"errors"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// This dated notice is separate from the terminal recovery event.
const AimeStatusUpdateEvent = "aime:status:20261108"

// QueueAimeStatusUpdate snapshots the current incident's subscribers once. Inbox
// and device outbox writes are atomic; subscriptions and recovery state are read-only.
// Existing notice identities make retries a no-op, including after a restart.
func QueueAimeStatusUpdate(app core.App) (int, error) {
	count := 0
	err := app.RunInTransaction(func(tx core.App) error {
		previous, err := tx.FindRecordsByFilter(Inbox, "event_key={:event}", "", 1, 0, dbx.Params{"event": AimeStatusUpdateEvent})
		if err != nil || len(previous) > 0 {
			return err
		}
		state, err := tx.FindRecordById(AimeState, aimeStateID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if state.GetBool("recovered") || state.GetString("incident") == "" {
			return nil
		}
		subs, err := tx.FindRecordsByFilter(AimeSubscriptions, "incident={:incident}", "id", 0, 0, dbx.Params{"incident": state.GetString("incident")})
		if err != nil {
			return err
		}
		col, err := tx.FindCollectionByNameOrId(Inbox)
		if err != nil {
			return err
		}
		for _, sub := range subs {
			user, err := tx.FindRecordById("user", sub.GetString("user"))
			if err != nil {
				return err
			}
			if user.GetBool("withdrawn") {
				continue
			}
			notice := core.NewRecord(col)
			notice.Set("user", user.Id)
			notice.Set("kind", AimeKind)
			notice.Set("event_key", AimeStatusUpdateEvent)
			if err := tx.Save(notice); err != nil {
				return err
			}
			if err := enqueueDevicesTx(tx, notice, user.Id); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

func aimeStatusUpdateItem(row *core.Record, locale string) map[string]any {
	title := "ALL.Net 점검 현황 안내 (11월 8일)"
	body := "11월 8일 오전 9시(한국 시간) 공식 안내: 일부 반다이남코 대상 게임은 오후부터 순차적으로 시험 운영을 재개할 예정입니다. 세가 게임은 점검이 계속되며 복구 일정은 미정입니다. 다음 공식 안내는 11월 10일 오전 9시(한국 시간) 예정입니다. 복구 알림 신청은 그대로 유지됩니다."
	if locale == "en" {
		title = "ALL.Net maintenance update (Nov 8)"
		body = "Official update as of Nov 8, 09:00 JST: selected Bandai Namco games are scheduled to resume trial operation gradually from the afternoon. SEGA games remain under maintenance with no recovery date announced. The next official update is planned for Nov 10, 09:00 JST. Your recovery alert subscription remains active."
	} else if locale == "ja" {
		title = "ALL.Netメンテナンス状況（11月8日）"
		body = "11月8日9:00（日本時間）の公式案内：バンダイナムコ製品の対象タイトルは午後から順次、試験的に稼働を再開する予定です。セガ製品は引き続きメンテナンス中で、再開の見通しは立っていません。次回の公式報告は11月10日9:00（日本時間）の予定です。復旧通知の登録は継続されます。"
	}
	return map[string]any{"id": row.Id, "kind": AimeKind, "read": row.GetBool("read"), "created": row.GetString("created"), "title": title, "message": body, "arcadeName": "", "gameName": "", "reaction": "", "url": "/"}
}
