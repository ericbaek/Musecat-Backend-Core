package messaging

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/supportfeedback"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ericbaek/musecat-backend-core/service/arcadereport"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

type BotChoice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Next  string `json:"next"`
}
type BotNode struct {
	ID      string      `json:"id"`
	Text    string      `json:"text"`
	Choices []BotChoice `json:"choices"`
}
type BotConfig struct {
	Enabled bool      `json:"enabled"`
	Start   string    `json:"start"`
	Nodes   []BotNode `json:"nodes"`
}
type BotSelection struct {
	MessageID string `json:"messageId"`
	ChoiceID  string `json:"choiceId"`
}
type botState struct {
	Config    BotConfig `json:"config"`
	Node      string    `json:"node"`
	MessageID string    `json:"messageId"`
	Active    bool      `json:"active"`
}

func (b BotConfig) Validate() error {
	encoded, _ := json.Marshal(b)
	if len(encoded) > 60000 {
		return apis.NewBadRequestError("Automatic replies are too large.", nil)
	}
	if len(b.Nodes) > 16 {
		return apis.NewBadRequestError("At most 16 automatic replies are allowed.", nil)
	}
	nodes := map[string]bool{}
	for _, n := range b.Nodes {
		if strings.TrimSpace(n.ID) == "" || len(n.ID) > 64 || nodes[n.ID] || strings.TrimSpace(n.Text) == "" || utf8.RuneCountInString(n.Text) > 2000 || len(n.Choices) > 6 {
			return apis.NewBadRequestError("Invalid automatic reply.", nil)
		}
		nodes[n.ID] = true
		choices := map[string]bool{}
		for _, c := range n.Choices {
			if c.ID == "__restart" || c.ID == "" || len(c.ID) > 64 || choices[c.ID] || strings.TrimSpace(c.Label) == "" || utf8.RuneCountInString(c.Label) > 80 {
				return apis.NewBadRequestError("Invalid reply choice.", nil)
			}
			choices[c.ID] = true
		}
	}
	if b.Enabled && !nodes[b.Start] {
		return apis.NewBadRequestError("Choose an initial automatic reply.", nil)
	}
	for _, n := range b.Nodes {
		for _, c := range n.Choices {
			if !nodes[c.Next] {
				return apis.NewBadRequestError("Reply choice must target an existing reply.", nil)
			}
		}
	}
	return nil
}
func GetArcadeBot(app core.App, user, id string) (BotConfig, error) {
	a, err := app.FindRecordById("arcade", id)
	if err != nil {
		return BotConfig{}, apis.NewNotFoundError("Arcade not found.", nil)
	}
	u, err := app.FindRecordById("user", user)
	if err != nil || !arcadereport.CanManage(u, a) {
		return BotConfig{}, apis.NewForbiddenError("Arcade management required.", nil)
	}
	var cfg BotConfig
	_ = json.Unmarshal([]byte(a.GetString("inquiry_bot")), &cfg)
	if cfg.Nodes == nil {
		cfg.Nodes = []BotNode{}
	}
	return cfg, nil
}
func SetArcadeBot(app core.App, user, id string, cfg BotConfig) (BotConfig, error) {
	if err := cfg.Validate(); err != nil {
		return BotConfig{}, err
	}
	err := app.RunInTransaction(func(tx core.App) error {
		if _, err := GetArcadeBot(tx, user, id); err != nil {
			return err
		}
		a, err := tx.FindRecordById("arcade", id)
		if err != nil {
			return err
		}
		a.Set("inquiry_bot", cfg)
		return tx.Save(a)
	})
	return cfg, err
}
func verificationBot(context string) (BotConfig, error) {
	texts := map[string]string{
		"arcadeOfficial.ko-KR": "안녕하세요, 뮤즈캣 팀입니다.\n오락실 공식 계정 인증을 위해 운영 중인 오락실 이름과 관리자임을 확인할 수 있는 자료를 보내 주세요.\n\n공식 X 또는 유튜브 계정의 소유를 보여 주는 자료나 게임제공업 등록증 등을 사용할 수 있습니다. 사진은 입력창의 추가 버튼으로 첨부할 수 있어요.",
		"arcadeOfficial.en-US": "Hello from the Musecat Team.\nTo verify your official arcade account, please send your arcade's name and evidence that you manage it.\n\nYou can use proof of ownership of its official X or YouTube account, or an arcade operating license. Attach photos using the add button in the message field.",
		"arcadeOfficial.ja-JP": "こんにちは、Musecatチームです。\n公式アカウントの認証のため、運営中の店舗名と管理者であることを確認できる資料をお送りください。\n\n公式XまたはYouTubeアカウントの所有を示す資料や、営業許可証などをご利用いただけます。写真は入力欄の追加ボタンから添付できます。",
	}
	text, ok := texts[context]
	if !ok {
		return BotConfig{}, apis.NewBadRequestError("Invalid inquiry context.", nil)
	}
	return BotConfig{Enabled: true, Start: "welcome", Nodes: []BotNode{{ID: "welcome", Text: text, Choices: []BotChoice{}}}}, nil
}
func inquiryBot(app core.App, in CreateInput) (BotConfig, error) {
	if in.InquiryContext == "" {
		return BotConfig{}, nil
	}
	if in.Kind != "inquiry" {
		return BotConfig{}, apis.NewBadRequestError("Automation is only available for inquiries.", nil)
	}
	if in.InquiryContext == "arcade" {
		if in.Inbox.Type != "arcade" {
			return BotConfig{}, apis.NewBadRequestError("Arcade context requires an arcade inbox.", nil)
		}
		a, err := app.FindRecordById("arcade", in.Inbox.ID)
		if err != nil {
			return BotConfig{}, apis.NewNotFoundError("Arcade not found.", nil)
		}
		var cfg BotConfig
		if err = json.Unmarshal([]byte(a.GetString("inquiry_bot")), &cfg); err != nil || !cfg.Enabled {
			return BotConfig{}, apis.NewBadRequestError("Automatic replies are not enabled.", nil)
		}
		return cfg, cfg.Validate()
	}
	if in.Inbox.Type != "team" || in.Inbox.ID != "musecat" {
		return BotConfig{}, apis.NewBadRequestError("Verification context requires the Musecat team inbox.", nil)
	}
	return verificationBot(in.InquiryContext)
}
func botRequestKey(user, key string) string {
	sum := sha256.Sum256([]byte(user + "\x00" + key))
	return "bot:" + hex.EncodeToString(sum[:])
}
func appendBotReply(tx core.App, c, q *core.Record, cfg BotConfig, nodeID, key, hash string) error {
	var node *BotNode
	for i := range cfg.Nodes {
		if cfg.Nodes[i].ID == nodeID {
			node = &cfg.Nodes[i]
			break
		}
	}
	if node == nil {
		return fmt.Errorf("automatic reply node not found")
	}
	if key == "" {
		key = fmt.Sprintf("bot:%s:%d", c.Id, c.GetInt("last_sequence")+1)
	}
	m, err := record(tx, Messages, map[string]any{"conversation": c.Id, "sequence": c.GetInt("last_sequence") + 1, "acting_type": q.GetString("inbox_type"), "acting_id": q.GetString("inbox_id"), "client_message_id": key, "payload_hash": hash, "text": node.Text, "automated": true, "bot_choices": node.Choices, "mentions": []string{}})
	if err != nil {
		return err
	}
	c.Set("last_sequence", m.GetInt("sequence"))
	c.Set("last_message", m.Id)
	c.Set("last_message_at", m.GetString("created"))
	if err = tx.Save(c); err != nil {
		return err
	}
	q.Set("bot_state", botState{Config: cfg, Node: nodeID, MessageID: m.Id, Active: len(node.Choices) > 0})
	if err = tx.Save(q); err != nil {
		return err
	}
	audience, err := conversationAudience(tx, c)
	if err != nil {
		return err
	}
	afterCommit(tx, Event{Name: "conversation.changed", ID: c.Id, LatestSequence: m.GetInt("sequence"), Reason: "message", Audience: audience})
	return nil
}
func botTransition(c *core.Record, q *core.Record, u *participant, in SendInput) (*botState, string, error) {
	var state botState
	_ = json.Unmarshal([]byte(q.GetString("bot_state")), &state)
	if in.BotChoice == nil {
		return &state, "", nil
	}
	if in.BotChoice.ChoiceID == "__restart" {
		validLabel := in.Text == "처음으로 돌아가기" || in.Text == "Back to the start" || in.Text == "最初に戻る"
		if !u.requests(q) || !canRestartBot(c, q) || state.MessageID != in.BotChoice.MessageID || !validLabel || len(in.Photos) > 0 {
			return nil, "", apis.NewBadRequestError("This reply choice is no longer available.", nil)
		}
		return &state, state.Config.Start, nil
	}
	if !u.requests(q) || !state.Active || state.MessageID != in.BotChoice.MessageID {
		return nil, "", apis.NewBadRequestError("This reply choice is no longer available.", nil)
	}
	for _, n := range state.Config.Nodes {
		if n.ID == state.Node {
			for _, choice := range n.Choices {
				if choice.ID == in.BotChoice.ChoiceID && strings.TrimSpace(choice.Label) == in.Text && len(in.Photos) == 0 {
					return &state, choice.Next, nil
				}
			}
		}
	}
	return nil, "", apis.NewBadRequestError("Invalid automatic reply choice.", nil)
}
func EnsureBotSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		for _, spec := range []struct {
			name, field string
			size        int
		}{{"arcade", "inquiry_bot", 65536}, {Inquiries, "bot_state", 65536}, {Messages, "bot_choices", 8192}} {
			col, err := tx.FindCollectionByNameOrId(spec.name)
			if err != nil {
				return err
			}
			if col.Fields.GetByName(spec.field) == nil {
				col.Fields.Add(&core.JSONField{Name: spec.field, MaxSize: int64(spec.size)})
				if err = tx.Save(col); err != nil {
					return err
				}
			}
		}
		col, err := tx.FindCollectionByNameOrId(Messages)
		if err != nil {
			return err
		}
		if col.Fields.GetByName("automated") == nil {
			col.Fields.Add(&core.BoolField{Name: "automated"})
			return tx.Save(col)
		}
		return nil
	})
}

func canRestartBot(c, q *core.Record) bool {
	var state botState
	_ = json.Unmarshal([]byte(q.GetString("bot_state")), &state)
	if state.MessageID == "" || c.GetString("last_message") != state.MessageID || state.Node == state.Config.Start {
		return false
	}
	for _, n := range state.Config.Nodes {
		if n.ID == state.Config.Start {
			return len(n.Choices) > 0
		}
	}
	return false
}

// PreviewInquiry reads an enabled entry flow without creating a conversation or a message.
type InquiryPreview struct {
	Text      string      `json:"text"`
	Choices   []BotChoice `json:"choices"`
	Revision  string      `json:"revision"`
	CreatedAt string      `json:"createdAt"`
}

func botRevision(cfg BotConfig) string {
	raw, _ := json.Marshal(cfg)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func PreviewInquiry(app core.App, viewer Principal, in CreateInput) (InquiryPreview, error) {
	u, err := viewer.resolve(app)
	if err != nil {
		return InquiryPreview{}, err
	}
	if in.InquiryContext == "" {
		return InquiryPreview{}, apis.NewBadRequestError("An explicit inquiry context is required.", nil)
	}
	if in.Inbox.Type == "arcade" {
		a, e := app.FindRecordById("arcade", in.Inbox.ID)
		if e != nil || (!a.GetBool("public") && (u.User == nil || a.GetString("createdBy") != u.ID) && !arcadereport.CanManage(u.User, a)) {
			return InquiryPreview{}, apis.NewNotFoundError("Arcade not found.", nil)
		}
		settings := supportfeedback.InquirySettings(a)
		if supportfeedback.HasOfficialAccount(app, a.Id) && settings.Mode != "internal" {
			return InquiryPreview{}, apis.NewBadRequestError("Internal inquiries are unavailable.", nil)
		}
		if u.Kind == "guest" && settings.LoginRequired {
			return InquiryPreview{}, apis.NewUnauthorizedError("Login required.", nil)
		}
	}
	cfg, err := inquiryBot(app, in)
	if err != nil {
		return InquiryPreview{}, err
	}
	for _, node := range cfg.Nodes {
		if node.ID == cfg.Start {
			return InquiryPreview{Text: node.Text, Choices: append([]BotChoice{}, node.Choices...), Revision: botRevision(cfg), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}, nil
		}
	}
	return InquiryPreview{}, apis.NewBadRequestError("Automatic replies are unavailable.", nil)
}
