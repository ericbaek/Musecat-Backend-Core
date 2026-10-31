package messaging

import (
	"github.com/pocketbase/pocketbase/core"
	"slices"
	"sync"
)

type Event struct {
	Audience         []string `json:"-"`
	Name             string   `json:"-"`
	ID               string   `json:"id,omitempty"`
	LatestSequence   int      `json:"latestSequence,omitempty"`
	LastReadSequence int      `json:"lastReadSequence,omitempty"`
	Reason           string   `json:"reason,omitempty"`
	UserID           string   `json:"-"`
}
type Subscription struct {
	User     string
	Events   chan Event
	Overflow chan struct{}
}
type Hub struct {
	mu      sync.Mutex
	clients map[*Subscription]bool
}

var hubInit sync.Mutex

func Events(app core.App) *Hub {
	hubInit.Lock()
	defer hubInit.Unlock()
	if h, ok := app.Store().Get("messaging.events").(*Hub); ok {
		return h
	}
	h := &Hub{clients: map[*Subscription]bool{}}
	app.Store().Set("messaging.events", h)
	return h
}
func (h *Hub) Subscribe(user string) *Subscription {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for s := range h.clients {
		if s.User == user {
			n++
		}
	}
	if n >= 4 || len(h.clients) >= 1024 {
		return nil
	}
	s := &Subscription{User: user, Events: make(chan Event, 32), Overflow: make(chan struct{}, 1)}
	h.clients[s] = true
	return s
}
func (h *Hub) Unsubscribe(s *Subscription) { h.mu.Lock(); delete(h.clients, s); h.mu.Unlock() }
func (h *Hub) Publish(event Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.clients {
		if event.ID != "" && event.UserID == "" && !slices.Contains(event.Audience, s.User) {
			continue
		}
		if event.UserID != "" && event.UserID != s.User {
			continue
		}
		select {
		case s.Events <- event:
		default:
			select {
			case s.Overflow <- struct{}{}:
			default:
			}
		}
	}
}
func afterCommit(app core.App, event Event) {
	hub := Events(app)
	if tx := app.TxInfo(); tx != nil {
		tx.OnComplete(func(err error) error {
			if err == nil {
				hub.Publish(event)
			}
			return nil
		})
	} else {
		hub.Publish(event)
	}
}

// RegisterEvents only invalidates committed notification/account changes. It
// never manufactures notifications from raw message saves during Full backfill.
func RegisterEvents(app core.App) {
	publishNotification := func(e *core.RecordEvent) error {
		Events(e.App).Publish(Event{Name: "notifications.changed", UserID: e.Record.GetString("user")})
		return e.Next()
	}
	app.OnRecordAfterCreateSuccess("user_notification").BindFunc(publishNotification)
	app.OnRecordAfterUpdateSuccess("user_notification").BindFunc(publishNotification)
	for _, name := range []string{"user", "user_ban", "arcade"} {
		app.OnRecordAfterUpdateSuccess(name).BindFunc(func(e *core.RecordEvent) error { Events(e.App).Publish(Event{Name: "resync"}); return e.Next() })
		app.OnRecordAfterDeleteSuccess(name).BindFunc(func(e *core.RecordEvent) error { Events(e.App).Publish(Event{Name: "resync"}); return e.Next() })
	}
	app.OnRecordAfterCreateSuccess("user_ban").BindFunc(func(e *core.RecordEvent) error { Events(e.App).Publish(Event{Name: "resync"}); return e.Next() })
}
