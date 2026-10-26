package messaging

import "github.com/pocketbase/pocketbase/tools/filesystem"

type Party struct {
	Type      string   `json:"type"`
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	AvatarURL *string  `json:"avatarUrl"`
	RoleTags  []string `json:"roleTags"`
}
type Identity struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}
type Attachment struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}
type Message struct {
	ID              string       `json:"id"`
	ConversationID  string       `json:"conversationId"`
	Sequence        int          `json:"sequence"`
	SenderUserID    string       `json:"senderUserId"`
	Sender          Party        `json:"sender"`
	ActingAs        Party        `json:"actingAs"`
	ClientMessageID string       `json:"clientMessageId"`
	Text            string       `json:"text"`
	Mentions        []string     `json:"mentions"`
	Photos          []Attachment `json:"photos"`
	CreatedAt       string       `json:"createdAt"`
}
type Summary struct {
	ID                       string     `json:"id"`
	Kind                     string     `json:"kind"`
	Counterpart              Party      `json:"counterpart"`
	Inbox                    *Party     `json:"inbox"`
	Requester                *Party     `json:"requester"`
	Status                   *string    `json:"status"`
	GuestChat                bool       `json:"guestChat"`
	LegacyFeedbackID         *string    `json:"legacyFeedbackId"`
	LatestSequence           int        `json:"latestSequence"`
	LastMessage              *Message   `json:"lastMessage"`
	LastMessageAt            *string    `json:"lastMessageAt"`
	UnreadCount              int        `json:"unreadCount"`
	LastReadSequence         int        `json:"lastReadSequence"`
	CanSend                  bool       `json:"canSend"`
	CanManageStatus          bool       `json:"canManageStatus"`
	AllowedSenderIdentities  []Identity `json:"allowedSenderIdentities"`
	AllowedStatusTransitions []string   `json:"allowedStatusTransitions"`
	CreatedAt                string     `json:"createdAt"`
}
type List struct {
	Items      []Summary `json:"items"`
	NextCursor *string   `json:"nextCursor"`
}
type MessagePage struct {
	Items              []Message `json:"items"`
	LatestSequence     int       `json:"latestSequence"`
	NextBeforeSequence *int      `json:"nextBeforeSequence"`
	NextAfterSequence  *int      `json:"nextAfterSequence"`
}
type SendInput struct {
	LegacyOriginal  bool               `json:"-"`
	Text            string             `json:"text"`
	Mentions        []string           `json:"mentions,omitempty"`
	ClientMessageID string             `json:"clientMessageId"`
	ActingAs        *Identity          `json:"actingAs,omitempty"`
	Photos          []*filesystem.File `json:"-"`
}
type CreateInput struct {
	Kind           string    `json:"kind"`
	PeerUserID     string    `json:"peerUserId"`
	Inbox          Identity  `json:"inbox"`
	InitialMessage SendInput `json:"initialMessage"`
}

func ptr[T any](v T) *T { return &v }
