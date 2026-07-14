package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

const (
	telegramNotifyHookHandlerID       = "__telegramNotifyOnRecordCreate__"
	telegramArcadePublicHookHandlerID = "__telegramNotifyOnArcadePublicUpdate__"
	collectionUser                    = "user"
	collectionUserBan                 = "user_ban"
	collectionUserReport              = "user_report"
	collectionArcadeRequestAtom       = "arcade_request_atom"
)

var telegramNotifyCollections = []string{
	arcadeinternal.CollectionArcadeRequestAdmin,
	collectionUserBan,
	collectionUserReport,
	collectionArcadeRequestAtom,
	arcadeinternal.CollectionSupportFeedback,
	arcadeinternal.CollectionSupporterRequest,
}

type TelegramSenderFunc func(ctx context.Context, message string) error
type DiscordSenderFunc func(ctx context.Context, message string) error

var (
	telegramSenderMu sync.RWMutex
	telegramSender   TelegramSenderFunc = sendTelegramText
	discordSenderMu  sync.RWMutex
	discordSender    DiscordSenderFunc = sendDiscordText
)

func RegisterTelegramNotifyHooks(app core.App) {
	app.OnRecordAfterCreateSuccess(telegramNotifyCollections...).Bind(&hook.Handler[*core.RecordEvent]{
		Id: telegramNotifyHookHandlerID,
		Func: func(e *core.RecordEvent) error {
			if err := e.Next(); err != nil {
				return err
			}

			rec := e.Record
			if rec == nil || rec.Collection() == nil {
				return nil
			}

			message, ok := buildCollectionCreateTelegramMessage(e.App, rec)
			if !ok || strings.TrimSpace(message) == "" {
				return nil
			}

			if err := notifyTelegram(context.Background(), message); err != nil {
				e.App.Logger().Warn("telegram notification on record create failed",
					slog.String("collection", rec.Collection().Name),
					slog.String("recordId", rec.Id),
					slog.String("error", err.Error()),
				)
			}
			return nil
		},
	})

	app.OnRecordAfterUpdateSuccess(arcadeinternal.CollectionArcade).Bind(&hook.Handler[*core.RecordEvent]{
		Id: telegramArcadePublicHookHandlerID,
		Func: func(e *core.RecordEvent) error {
			if err := e.Next(); err != nil {
				return err
			}

			rec := e.Record
			if rec == nil || rec.Collection() == nil {
				return nil
			}
			if !isArcadePublicTransition(rec) {
				return nil
			}

			message := buildArcadePublicTelegramMessage(e.App, rec)
			if strings.TrimSpace(message) == "" {
				return nil
			}

			if err := notifyTelegram(context.Background(), message); err != nil {
				e.App.Logger().Warn("telegram notification on arcade public update failed",
					slog.String("recordId", rec.Id),
					slog.String("error", err.Error()),
				)
			}
			return nil
		},
	})
}

func SetTelegramSenderForTest(sender TelegramSenderFunc) func() {
	telegramSenderMu.Lock()
	prev := telegramSender
	if sender == nil {
		sender = sendTelegramText
	}
	telegramSender = sender
	telegramSenderMu.Unlock()

	return func() {
		telegramSenderMu.Lock()
		telegramSender = prev
		telegramSenderMu.Unlock()
	}
}

func SetDiscordSenderForTest(sender DiscordSenderFunc) func() {
	discordSenderMu.Lock()
	prev := discordSender
	if sender == nil {
		sender = sendDiscordText
	}
	discordSender = sender
	discordSenderMu.Unlock()

	return func() {
		discordSenderMu.Lock()
		discordSender = prev
		discordSenderMu.Unlock()
	}
}

type userSummary struct {
	ID       string
	Username string
	Email    string
}

type arcadeBasicSummary struct {
	ID      string
	Name    string
	Address string
}

func loadUserSummary(app core.App, userID string) userSummary {
	userID = strings.TrimSpace(userID)
	if app == nil || userID == "" {
		return userSummary{}
	}

	rec, err := app.FindRecordById(collectionUser, userID)
	if err != nil || rec == nil {
		return userSummary{ID: userID}
	}

	return userSummary{
		ID:       userID,
		Username: strings.TrimSpace(rec.GetString("username")),
		Email:    strings.TrimSpace(rec.Email()),
	}
}

func loadArcadeBasicSummary(app core.App, basicID string) arcadeBasicSummary {
	basicID = strings.TrimSpace(basicID)
	if app == nil || basicID == "" {
		return arcadeBasicSummary{ID: basicID}
	}

	rec, err := app.FindRecordById(arcadeinternal.CollectionArcadeBasic, basicID)
	if err != nil || rec == nil {
		return arcadeBasicSummary{ID: basicID}
	}

	return arcadeBasicSummary{
		ID:      basicID,
		Name:    strings.TrimSpace(rec.GetString("name")),
		Address: strings.TrimSpace(rec.GetString("address")),
	}
}

func isArcadePublicTransition(rec *core.Record) bool {
	if rec == nil || !rec.GetBool("public") {
		return false
	}

	original := rec.Original()
	if original == nil {
		return false
	}

	return !original.GetBool("public")
}

func buildCollectionCreateTelegramMessage(app core.App, rec *core.Record) (string, bool) {
	collectionName := rec.Collection().Name
	switch collectionName {
	case arcadeinternal.CollectionArcadeRequestAdmin, collectionArcadeRequestAtom:
		return buildArcadeRequestTelegramMessage(app, rec, collectionName), true
	case collectionUserBan:
		return buildUserBanTelegramMessage(rec), true
	case collectionUserReport:
		return buildUserReportTelegramMessage(app, rec), true
	case arcadeinternal.CollectionSupportFeedback:
		return buildSupportFeedbackTelegramMessage(app, rec), true
	case arcadeinternal.CollectionSupporterRequest:
		return buildSupporterRequestTelegramMessage(app, rec), true
	default:
		return "", false
	}
}

func buildArcadePublicTelegramMessage(app core.App, rec *core.Record) string {
	createdByID := strings.TrimSpace(rec.GetString("createdBy"))
	createdBy := loadUserSummary(app, createdByID)
	basic := loadArcadeBasicSummary(app, strings.TrimSpace(rec.GetString("basic")))

	var b strings.Builder
	b.WriteString("[arcade_public]\n")
	appendTelegramField(&b, "id", rec.Id)
	appendTelegramField(&b, "basic", basic.ID)
	appendTelegramField(&b, "name", basic.Name)
	appendTelegramField(&b, "address", basic.Address)
	appendTelegramField(&b, "country", strings.TrimSpace(rec.GetString("country")))
	appendTelegramField(&b, "timezone", strings.TrimSpace(rec.GetString("timezone")))
	appendTelegramField(&b, "public", "true")
	appendTelegramField(&b, "createdBy", createdByID)
	appendTelegramField(&b, "createdByUsername", createdBy.Username)
	appendTelegramField(&b, "createdByEmail", createdBy.Email)
	appendTelegramField(&b, "updated", strings.TrimSpace(rec.GetString("updated")))
	return b.String()
}

func buildArcadeRequestTelegramMessage(app core.App, rec *core.Record, collectionName string) string {
	createdByID := strings.TrimSpace(rec.GetString("createdBy"))
	createdBy := loadUserSummary(app, createdByID)

	var b strings.Builder
	b.WriteString("[")
	b.WriteString(collectionName)
	b.WriteString("]\n")
	appendTelegramField(&b, "id", rec.Id)
	appendTelegramField(&b, "arcade", strings.TrimSpace(rec.GetString("arcade")))
	appendTelegramField(&b, "urgency", strings.TrimSpace(rec.GetString("urgency")))
	appendTelegramField(&b, "status", strings.TrimSpace(rec.GetString("status")))
	appendTelegramField(&b, "createdBy", createdByID)
	appendTelegramField(&b, "createdByUsername", createdBy.Username)
	appendTelegramField(&b, "createdByEmail", createdBy.Email)
	appendTelegramField(&b, "created", strings.TrimSpace(rec.GetString("created")))

	body := strings.TrimSpace(rec.GetString("message"))
	if body == "" {
		body = strings.TrimSpace(rec.GetString("reason"))
	}
	if body != "" {
		b.WriteString("body:\n")
		b.WriteString(body)
	}

	return b.String()
}

func buildUserReportTelegramMessage(app core.App, rec *core.Record) string {
	createdByID := strings.TrimSpace(rec.GetString("createdBy"))
	targetUserID := strings.TrimSpace(rec.GetString("user"))
	createdBy := loadUserSummary(app, createdByID)
	target := loadUserSummary(app, targetUserID)

	var b strings.Builder
	b.WriteString("[")
	b.WriteString(collectionUserReport)
	b.WriteString("]\n")
	appendTelegramField(&b, "id", rec.Id)
	appendTelegramField(&b, "user", targetUserID)
	appendTelegramField(&b, "userUsername", target.Username)
	appendTelegramField(&b, "userEmail", target.Email)
	appendTelegramField(&b, "status", strings.TrimSpace(rec.GetString("status")))
	appendTelegramField(&b, "createdBy", createdByID)
	appendTelegramField(&b, "createdByUsername", createdBy.Username)
	appendTelegramField(&b, "createdByEmail", createdBy.Email)
	appendTelegramField(&b, "created", strings.TrimSpace(rec.GetString("created")))

	reason := strings.TrimSpace(rec.GetString("reason"))
	if reason != "" {
		b.WriteString("reason:\n")
		b.WriteString(reason)
	}

	return b.String()
}

func buildUserBanTelegramMessage(rec *core.Record) string {
	var b strings.Builder
	b.WriteString("[")
	b.WriteString(collectionUserBan)
	b.WriteString("]\n")
	appendTelegramField(&b, "id", rec.Id)
	appendTelegramField(&b, "hashed_email", strings.TrimSpace(rec.GetString("hashed_email")))
	appendTelegramField(&b, "reason", strings.TrimSpace(rec.GetString("reason")))

	until := strings.TrimSpace(rec.GetString("until"))
	if until == "" {
		until = "permanent"
	}
	appendTelegramField(&b, "until", until)
	appendTelegramField(&b, "created", strings.TrimSpace(rec.GetString("created")))

	return b.String()
}

func buildSupportFeedbackTelegramMessage(app core.App, rec *core.Record) string {
	createdByID := strings.TrimSpace(rec.GetString("createdBy"))
	createdBy := loadUserSummary(app, createdByID)

	var b strings.Builder
	b.WriteString("[")
	b.WriteString(arcadeinternal.CollectionSupportFeedback)
	b.WriteString("]\n")
	appendTelegramField(&b, "id", rec.Id)
	appendTelegramField(&b, "status", strings.TrimSpace(rec.GetString("status")))
	appendTelegramField(&b, "createdBy", createdByID)
	appendTelegramField(&b, "createdByUsername", createdBy.Username)
	appendTelegramField(&b, "createdByEmail", createdBy.Email)
	appendTelegramField(&b, "created", strings.TrimSpace(rec.GetString("created")))

	message := strings.TrimSpace(rec.GetString("message"))
	if message != "" {
		b.WriteString("message:\n")
		b.WriteString(message)
	}

	return b.String()
}

func buildSupporterRequestTelegramMessage(app core.App, rec *core.Record) string {
	createdByID := strings.TrimSpace(rec.GetString("createdBy"))
	createdBy := loadUserSummary(app, createdByID)

	var snapshot arcadeinternal.SupporterScoreResponse
	if raw := rec.Get("score_breakdown"); raw != nil {
		buf, err := json.Marshal(raw)
		if err == nil {
			_ = json.Unmarshal(buf, &snapshot)
		}
	}

	var b strings.Builder
	b.WriteString("[")
	b.WriteString(arcadeinternal.CollectionSupporterRequest)
	b.WriteString("]\n")
	appendTelegramField(&b, "id", rec.Id)
	appendTelegramField(&b, "status", strings.TrimSpace(rec.GetString("status")))
	appendTelegramField(&b, "createdBy", createdByID)
	appendTelegramField(&b, "createdByUsername", createdBy.Username)
	appendTelegramField(&b, "createdByEmail", createdBy.Email)
	appendTelegramField(&b, "created", strings.TrimSpace(rec.GetString("created")))
	appendTelegramField(&b, "score_total", fmt.Sprintf("%d", rec.GetInt("score_total")))
	appendTelegramField(&b, "qualified", fmt.Sprintf("%t", rec.GetBool("qualified")))

	if len(snapshot.Entries) > 0 {
		limit := len(snapshot.Entries)
		if limit > 5 {
			limit = 5
		}
		b.WriteString("recent_ledger:\n")
		for i := len(snapshot.Entries) - 1; i >= len(snapshot.Entries)-limit; i-- {
			item := snapshot.Entries[i]
			b.WriteString("- ")
			b.WriteString(item.Source)
			if strings.TrimSpace(item.Action) != "" {
				b.WriteString(":")
				b.WriteString(item.Action)
			}
			if item.ArcadeName != "" {
				b.WriteString(" @ ")
				b.WriteString(item.ArcadeName)
			} else if item.ArcadeID != "" {
				b.WriteString(" @ ")
				b.WriteString(item.ArcadeID)
			} else if item.TargetID != "" {
				b.WriteString(" @ ")
				b.WriteString(item.TargetID)
			}
			b.WriteString(" (")
			b.WriteString(fmt.Sprintf("%+d", item.Exp))
			b.WriteString(")\n")
		}
		if len(snapshot.Entries) > limit {
			b.WriteString("... and ")
			b.WriteString(fmt.Sprintf("%d", len(snapshot.Entries)-limit))
			b.WriteString(" more\n")
		}
	}

	return b.String()
}

func appendTelegramField(b *strings.Builder, key, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteString("\n")
}

func notifyTelegram(ctx context.Context, message string) error {
	telegramSenderMu.RLock()
	tgSender := telegramSender
	telegramSenderMu.RUnlock()

	discordSenderMu.RLock()
	dcSender := discordSender
	discordSenderMu.RUnlock()

	var errs []string
	if err := tgSender(ctx, message); err != nil {
		errs = append(errs, "telegram: "+err.Error())
	}
	if err := dcSender(ctx, message); err != nil {
		errs = append(errs, "discord: "+err.Error())
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func sendTelegramText(context.Context, string) error {
	return nil
}

func sendDiscordText(context.Context, string) error {
	return nil
}
