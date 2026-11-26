package usernotification

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/pocketbase/pocketbase/core"
)

// The one-off 2026-11 ALL.Net recovery alert is retired. Only the rendering of
// historic aime_recovery inbox rows remains; those rows follow the shared inbox
// lifecycle and must not be deleted to retire the feature.
const AimeKind = "aime_recovery"

// Collection names of the retired feature, kept for Full's historic migrations.
const AimeState = "aime_status"
const AimeProbes = "aime_probe"
const AimeSubscriptions = "aime_recovery_subscription"

// AimeStatusUpdateEvent identifies the dated 2026-11-08 notice, rendered apart
// from the terminal recovery event.
const AimeStatusUpdateEvent = "aime:status:20261108"

// EnsureAimeSchema is retained only so Full's immutable 1795100000/1795200000
// migrations keep compiling and replaying in order. Full's retirement migration
// drops these collections afterwards; nothing at runtime reads them anymore.
func EnsureAimeSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		user, err := tx.FindCollectionByNameOrId("user")
		if err != nil {
			return err
		}
		definitions := []struct {
			name   string
			fields []core.Field
		}{
			{AimeProbes, []core.Field{&core.DateField{Name: "checked_at", Required: true}, &core.TextField{Name: "status", Required: true, Max: 20}, &core.NumberField{Name: "http_status", OnlyInt: true}}},
			{AimeState, []core.Field{&core.TextField{Name: "status", Max: 20}, &core.NumberField{Name: "http_status", OnlyInt: true}, &core.DateField{Name: "checked_at"}, &core.TextField{Name: "incident", Max: 40}, &core.BoolField{Name: "recovered"}, &core.NumberField{Name: "confirmations", OnlyInt: true}, &core.DateField{Name: "confirmation_at"}, &core.DateField{Name: "ops_candidate_at"}, &core.BoolField{Name: "ops_telegram_sent"}, &core.BoolField{Name: "ops_discord_sent"}}},
			{AimeSubscriptions, []core.Field{&core.RelationField{Name: "user", CollectionId: user.Id, Required: true, MaxSelect: 1, CascadeDelete: true}, &core.TextField{Name: "incident", Required: true, Max: 40}}},
		}
		for _, d := range definitions {
			col, err := tx.FindCollectionByNameOrId(d.name)
			if errors.Is(err, sql.ErrNoRows) {
				col = core.NewBaseCollection(d.name)
			} else if err != nil {
				return err
			} else if !col.IsBase() || col.System {
				return fmt.Errorf("incompatible %s", d.name)
			}
			for _, field := range d.fields {
				old := col.Fields.GetByName(field.GetName())
				if old == nil {
					col.Fields.Add(field)
				} else if old.Type() != field.Type() {
					return fmt.Errorf("incompatible %s.%s", d.name, field.GetName())
				}
				if want, ok := field.(*core.RelationField); ok && old != nil {
					got := old.(*core.RelationField)
					if got.CollectionId != want.CollectionId || !got.Required || !got.CascadeDelete || got.MaxSelect != 1 {
						return fmt.Errorf("incompatible %s.user", d.name)
					}
				}
			}
			col.ListRule = nil
			col.ViewRule = nil
			col.CreateRule = nil
			col.UpdateRule = nil
			col.DeleteRule = nil
			if d.name == AimeProbes {
				col.AddIndex("idx_aime_probe_checked_at", false, "checked_at", "")
			}
			if d.name == AimeSubscriptions {
				col.AddIndex("idx_aime_subscriber", true, "user", "")
			}
			if err := tx.Save(col); err != nil {
				return err
			}
		}
		return nil
	})
}

func AimeItem(row *core.Record, locale string) map[string]any {
	if row.GetString("event_key") == AimeStatusUpdateEvent {
		return aimeStatusUpdateItem(row, locale)
	}
	title, body := "게임 NET 웹 접속이 복구되었습니다", "마이넷과 츄니넷에서 웹 점검 안내가 더 이상 감지되지 않습니다."
	if locale == "en" {
		title, body = "Game NET web access is restored", "Web maintenance is no longer detected on maimai DX NET or CHUNITHM-NET."
	} else if locale == "ja" {
		title, body = "ゲームNETへのWeb接続が復旧しました", "maimai DX NETとCHUNITHM-NETでWebメンテナンス案内が検出されなくなりました。"
	}
	return map[string]any{"id": row.Id, "kind": AimeKind, "read": row.GetBool("read"), "created": row.GetString("created"), "title": title, "message": body, "arcadeName": "", "gameName": "", "reaction": "", "url": "/"}
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
