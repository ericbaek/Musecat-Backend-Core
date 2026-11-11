package usernotification

import (
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/communitymention"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"strings"
	"time"
)

const CommunityMention = "community_mention"

const CommunityLike = "community_like"
const CommunityComment = "community_comment"
const CommunityReply = "community_reply"

func IsCommunity(kind string) bool {
	return kind == CommunityMention || kind == CommunityLike || kind == CommunityComment || kind == CommunityReply
}

// CommunityLiveSQL excludes retracted events before grouping and pagination.
func communityLiveSQL(alias string) string {
	return fmt.Sprintf(`EXISTS (SELECT 1 FROM community_post p WHERE p.id=%[1]s.community_post AND p.status='active' AND p.flair='achievement') AND ((%[1]s.kind='community_mention' AND EXISTS (SELECT 1 FROM community_post mp,json_each(mp.mentions) m WHERE mp.id=%[1]s.community_post AND json_extract(m.value,'$.kind')='user' AND json_extract(m.value,'$.id')=%[1]s.user)) OR (%[1]s.kind='community_like' AND EXISTS (SELECT 1 FROM community_post_like l WHERE l.post=%[1]s.community_post AND l.author=%[1]s.actor)) OR (%[1]s.kind IN ('community_comment','community_reply') AND EXISTS (SELECT 1 FROM community_comment c WHERE c.id=%[1]s.community_comment AND c.post=%[1]s.community_post AND c.status='active' AND (%[1]s.kind!='community_reply' OR EXISTS(SELECT 1 FROM community_comment target WHERE target.id=%[1]s.community_thread AND target.status='active' AND target.author=%[1]s.user)))))`, alias)
}

// CommunityInboxFilter keeps only the latest live event in each logical inbox item.
// Event identities remain durable, so unlike/re-like never creates a new alert.
func CommunityInboxFilter() string {
	return `(user_notification.kind NOT IN ('community_like','community_comment','community_reply','community_mention') OR (` + communityLiveSQL("user_notification") + ` AND NOT EXISTS (SELECT 1 FROM user_notification newer WHERE newer.community_post!='' AND newer.user=user_notification.user AND newer.kind=user_notification.kind AND newer.community_post=user_notification.community_post AND newer.community_thread=user_notification.community_thread AND (newer.created>user_notification.created OR (newer.created=user_notification.created AND newer.id>user_notification.id)) AND ` + communityLiveSQL("newer") + `)))`
}

func CommunityViewing(app core.App, userID, postID string, now time.Time) (bool, error) {
	rows, err := app.FindRecordsByFilter(CommunityViews, "user={:user} && post={:post} && expires>{:now}", "", 1, 0, dbx.Params{"user": userID, "post": postID, "now": now.UTC()})
	return len(rows) > 0, err
}

// NotifyCommunityTx runs in the same transaction as the source mutation.
func NotifyCommunityTx(app core.App, post, comment *core.Record, actor string) error {
	if post.GetString("status") != "active" || post.GetString("flair") != "achievement" {
		return nil
	}
	kind, userID, thread, event := CommunityLike, post.GetString("author"), "", "community-like:"+post.Id+":"+actor
	if comment != nil {
		kind = CommunityComment
		event = "community-comment:" + comment.Id
		if target := comment.GetString("reply_to"); target != "" {
			parent, err := app.FindRecordById("community_comment", target)
			if err != nil {
				return err
			}
			kind = CommunityReply
			userID = parent.GetString("author")
			thread = target
		}
	}
	if userID == "" || userID == actor {
		return nil
	}
	user, err := app.FindRecordById("user", userID)
	if err != nil || user.GetBool("withdrawn") {
		return nil
	}
	old, err := app.FindRecordsByFilter(Inbox, "user={:user} && event_key={:event}", "", 1, 0, dbx.Params{"user": userID, "event": event})
	if err != nil || len(old) > 0 {
		return err
	}
	col, err := app.FindCollectionByNameOrId(Inbox)
	if err != nil {
		return err
	}
	row := core.NewRecord(col)
	row.Set("user", userID)
	row.Set("actor", actor)
	row.Set("kind", kind)
	row.Set("event_key", event)
	row.Set("community_post", post.Id)
	row.Set("community_thread", thread)
	if comment != nil {
		row.Set("community_comment", comment.Id)
	}
	viewing, err := CommunityViewing(app, userID, post.Id, time.Now().UTC())
	if err != nil {
		return err
	}
	row.Set("read", viewing)
	if err := app.Save(row); err != nil {
		return err
	}
	if kind == CommunityLike || viewing {
		return nil
	}
	return enqueueDevicesTx(app, row, userID)
}

func communityVisible(app core.App, user, row *core.Record) bool {
	if user == nil || user.GetBool("withdrawn") || row.GetString("user") != user.Id {
		return false
	}
	post, err := app.FindRecordById("community_post", row.GetString("community_post"))
	if err != nil || post.GetString("status") != "active" || post.GetString("flair") != "achievement" {
		return false
	}
	if row.GetString("kind") == CommunityReply {
		target, err := app.FindRecordById("community_comment", row.GetString("community_thread"))
		if err != nil || target.GetString("author") != user.Id || target.GetString("status") != "active" {
			return false
		}
	} else if row.GetString("kind") != CommunityMention && post.GetString("author") != user.Id {
		return false
	}
	rows := []*core.Record{}
	err = app.RecordQuery(Inbox).AndWhere(dbx.NewExp("user_notification.id={:id} AND "+communityLiveSQL("user_notification"), dbx.Params{"id": row.Id})).Limit(1).All(&rows)
	return err == nil && len(rows) > 0
}

// CommunityPushDeliverable rechecks reads and live viewing immediately before delivery.
func CommunityPushDeliverable(app core.App, user, row *core.Record, now time.Time) (bool, error) {
	if row.GetString("kind") == CommunityLike || row.GetBool("read") || !communityVisible(app, user, row) {
		return false, nil
	}
	viewing, err := CommunityViewing(app, user.Id, row.GetString("community_post"), now)
	return !viewing, err
}

func communityItem(app core.App, row *core.Record, locale string) map[string]any {
	name := ""
	if actor, err := app.FindRecordById("user", row.GetString("actor")); err == nil {
		name = actor.GetString("username")
	}
	var count struct {
		Total int `db:"total"`
	}
	_ = app.DB().NewQuery(`SELECT count(*) total FROM user_notification n WHERE n.user={:user} AND n.kind={:kind} AND n.community_post={:post} AND n.community_thread={:thread} AND ` + communityLiveSQL("n")).Bind(dbx.Params{"user": row.GetString("user"), "kind": row.GetString("kind"), "post": row.GetString("community_post"), "thread": row.GetString("community_thread")}).One(&count)
	title, message := "성과", name+"님이 내 성과에 댓글을 남겼습니다."
	kind := row.GetString("kind")
	if kind == CommunityLike {
		message = name + "님이 내 성과를 좋아합니다."
		if count.Total > 1 {
			message = fmt.Sprintf("%s님 외 %d명이 내 성과를 좋아합니다.", name, count.Total-1)
		}
	} else if kind == CommunityReply {
		message = name + "님이 내 댓글에 답글을 남겼습니다."
	}
	if locale == "en" {
		title = "Achievements"
		message = name + " commented on your achievement."
		if kind == CommunityReply {
			message = name + " replied to your comment."
		}
		if kind == CommunityLike {
			message = name + " liked your achievement."
			if count.Total > 1 {
				message = fmt.Sprintf("%s and %d others liked your achievement.", name, count.Total-1)
			}
		}
	} else if locale == "ja" {
		title = "実績"
		message = name + "さんが実績にコメントしました。"
		if kind == CommunityReply {
			message = name + "さんがコメントに返信しました。"
		}
		if kind == CommunityLike {
			message = name + "さんが実績にいいねしました。"
			if count.Total > 1 {
				message = fmt.Sprintf("%sさんと他%d人が実績にいいねしました。", name, count.Total-1)
			}
		}
	}
	if kind != CommunityLike && count.Total > 1 {
		if locale == "en" {
			message += fmt.Sprintf(" (%d comments)", count.Total)
		} else if locale == "ja" {
			message += fmt.Sprintf("（%d件）", count.Total)
		} else {
			message += fmt.Sprintf(" (%d개)", count.Total)
		}
	}
	if kind == CommunityMention {
		switch locale {
		case "en":
			message = name + " mentioned you in an achievement."
		case "ja":
			message = name + "さんが実績であなたをメンションしました。"
		default:
			message = name + "님이 성과에서 나를 멘션했습니다."
		}
	}
	link := "/community/" + row.GetString("community_post")
	if comment := row.GetString("community_comment"); comment != "" {
		link += "?comment=" + comment + "#comment-" + comment
	}
	return map[string]any{"groupKey": kind + ":" + row.GetString("community_post") + ":" + row.GetString("community_thread"), "id": row.Id, "kind": kind, "read": row.GetBool("read"), "created": row.GetString("created"), "title": title, "message": strings.TrimSpace(message), "arcadeName": "", "gameName": "", "reaction": "", "url": link}
}

// RetractCommunityLikeTx retires the original alert without removing its durable
// identity. Re-adding an unread-then-canceled like cannot resurrect an unread badge.
func RetractCommunityLikeTx(app core.App, postID, actor string) error {
	rows, err := app.FindRecordsByFilter(Inbox, "kind='community_like' && community_post={:post} && actor={:actor} && read=false", "", 1, 0, dbx.Params{"post": postID, "actor": actor})
	if err != nil {
		return err
	}
	for _, row := range rows {
		row.Set("read", true)
		if err := app.Save(row); err != nil {
			return err
		}
	}
	return nil
}

// NotifyMentionsTx shares the source transaction and keeps one durable event per post/user.
func NotifyMentionsTx(app core.App, post *core.Record, actor string) error {
	if post.GetString("status") != "active" || post.GetString("flair") != "achievement" {
		return nil
	}
	seen := map[string]bool{}
	for _, mention := range communitymention.Read(post) {
		if mention.Kind != "user" || mention.ID == actor || seen[mention.ID] {
			continue
		}
		seen[mention.ID] = true
		user, err := app.FindRecordById("user", mention.ID)
		if err != nil || user.GetBool("withdrawn") {
			continue
		}
		event := "community-mention:" + post.Id + ":" + mention.ID
		old, err := app.FindRecordsByFilter(Inbox, "user={:u} && event_key={:e}", "", 1, 0, dbx.Params{"u": mention.ID, "e": event})
		if err != nil {
			return err
		}
		if len(old) > 0 {
			continue
		}
		col, err := app.FindCollectionByNameOrId(Inbox)
		if err != nil {
			return err
		}
		row := core.NewRecord(col)
		row.Set("user", mention.ID)
		row.Set("actor", actor)
		row.Set("kind", CommunityMention)
		row.Set("event_key", event)
		row.Set("community_post", post.Id)
		viewing, err := CommunityViewing(app, mention.ID, post.Id, time.Now().UTC())
		if err != nil {
			return err
		}
		row.Set("read", viewing)
		if err := app.Save(row); err != nil {
			return err
		}
		if !viewing {
			if err := enqueueDevicesTx(app, row, mention.ID); err != nil {
				return err
			}
		}
	}
	return nil
}
