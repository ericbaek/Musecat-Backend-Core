// Package botaccess implements personal, revocable arcade automation credentials.
package botaccess

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ericbaek/musecat-backend-core/service/contribution"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

const tokenPrefix = "musecat_bot_"

var errInvalidKey = errors.New("invalid bot key")
var errRateLimited = errors.New("bot key rate limited")

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func noStore(re *core.RequestEvent) {
	re.Response.Header().Set("Cache-Control", "private, no-store")
	re.Response.Header().Set("Referrer-Policy", "no-referrer")
}

func projection(record *core.Record) map[string]any {
	return map[string]any{"id": record.Id, "name": record.GetString("name"), "prefix": record.GetString("prefix"), "created": record.GetString("created"), "revoked": record.GetString("revoked"), "last_used": record.GetString("last_used")}
}

func List(re *core.RequestEvent) error {
	noStore(re)
	items, err := re.App.FindRecordsByFilter(Keys, "owner={:owner}", "revoked,-created", 0, 0, dbx.Params{"owner": re.Auth.Id})
	if err != nil {
		return re.InternalServerError("Failed to list bot keys.", err)
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, projection(item))
	}
	return re.JSON(http.StatusOK, map[string]any{"items": result})
}

func Create(re *core.RequestEvent) error {
	noStore(re)
	if !CanIssueKey(re.Auth) {
		return re.ForbiddenError("Founding supporter or higher access is required.", nil)
	}
	if re.Auth.GetString("username") == "" {
		return re.BadRequestError("Complete your profile before issuing a bot key.", nil)
	}
	var body struct {
		Name string `json:"name"`
	}
	raw, err := io.ReadAll(io.LimitReader(re.Request.Body, 4097))
	if err != nil || len(raw) > 4096 {
		return re.BadRequestError("Invalid request.", nil)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF {
		return re.BadRequestError("Invalid request.", nil)
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || len([]rune(body.Name)) > 60 {
		return re.BadRequestError("A bot name of at most 60 characters is required.", nil)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return re.InternalServerError("Failed to generate bot key.", err)
	}
	token := tokenPrefix + base64.RawURLEncoding.EncodeToString(secret)
	var record *core.Record
	err = re.App.RunInTransaction(func(tx core.App) error {
		active, err := tx.CountRecords(Keys, dbx.NewExp("owner={:owner} AND revoked=''", dbx.Params{"owner": re.Auth.Id}))
		if err != nil {
			return err
		}
		if active >= 5 {
			return errors.New("key_limit")
		}
		collection, err := tx.FindCollectionByNameOrId(Keys)
		if err != nil {
			return err
		}
		record = core.NewRecord(collection)
		record.Set("owner", re.Auth.Id)
		record.Set("name", body.Name)
		record.Set("key_hash", digest(token))
		record.Set("owner_key_hash", digest(re.Auth.TokenKey()))
		record.Set("prefix", token[:len(tokenPrefix)+8])
		return tx.Save(record)
	})
	if err != nil {
		if err.Error() == "key_limit" {
			return re.BadRequestError("At most five active bot keys are allowed.", nil)
		}
		return re.InternalServerError("Failed to create bot key.", err)
	}
	return re.JSON(http.StatusCreated, map[string]any{"key": projection(record), "token": token})
}

func Revoke(re *core.RequestEvent) error {
	noStore(re)
	err := re.App.RunInTransaction(func(tx core.App) error {
		record, err := tx.FindRecordById(Keys, re.Request.PathValue("id"))
		if err != nil || record.GetString("owner") != re.Auth.Id {
			return errInvalidKey
		}
		if record.GetString("revoked") != "" {
			return nil
		}
		record.Set("revoked", time.Now().UTC())
		return tx.Save(record)
	})
	if errors.Is(err, errInvalidKey) {
		return re.NotFoundError("Bot key not found.", nil)
	}
	if err != nil {
		return re.InternalServerError("Failed to revoke bot key.", err)
	}
	return re.NoContent(http.StatusNoContent)
}

// RequireKey authenticates only opaque bot keys. It never accepts web JWTs or
// creates a user session. Live account policy and credential rotation are checked each call.
func RequireKey() *hook.Handler[*core.RequestEvent] {
	return &hook.Handler[*core.RequestEvent]{Id: "musecatBotKey", Func: func(re *core.RequestEvent) error {
		noStore(re)
		parts := strings.Fields(re.Request.Header.Get("Authorization"))
		if len(re.Request.Header.Values("Authorization")) != 1 || len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || !strings.HasPrefix(parts[1], tokenPrefix) || len(parts[1]) != len(tokenPrefix)+43 {
			return re.UnauthorizedError("Invalid bot key.", nil)
		}
		var owner *core.Record
		var source contribution.Source
		err := re.App.RunInTransaction(func(tx core.App) error {
			record, err := tx.FindFirstRecordByData(Keys, "key_hash", digest(parts[1]))
			if err != nil || record.GetString("revoked") != "" {
				return errInvalidKey
			}
			owner, err = tx.FindRecordById("user", record.GetString("owner"))
			if err != nil || subtle.ConstantTimeCompare([]byte(record.GetString("owner_key_hash")), []byte(digest(owner.TokenKey()))) != 1 {
				return errInvalidKey
			}
			if owner.GetString("username") == "" || !CanIssueKey(owner) {
				return errInvalidKey
			}
			now := time.Now().UTC()
			count := record.GetInt("window_count")
			if now.Sub(record.GetDateTime("window_start").Time()) >= time.Minute {
				record.Set("window_start", now)
				count = 0
			}
			if count >= 60 {
				return errRateLimited
			}
			record.Set("window_count", count+1)
			record.Set("last_used", now)
			if err := tx.Save(record); err != nil {
				return err
			}
			source = contribution.Source{Kind: "bot", ID: record.Id, Name: record.GetString("name")}
			return nil
		})
		if errors.Is(err, errRateLimited) {
			re.Response.Header().Set("Retry-After", "60")
			return re.JSON(http.StatusTooManyRequests, map[string]string{"error": "rate_limit_exceeded"})
		}
		if errors.Is(err, errInvalidKey) {
			return re.UnauthorizedError("Invalid bot key.", nil)
		}
		if err != nil {
			return re.InternalServerError("Failed to authenticate bot key.", err)
		}
		re.Auth = owner
		re.App = contribution.WithSource(re.App, source)
		return re.Next()
	}}
}

// CanIssueKey is a role entitlement, independent of XP level. Official arcade
// ownership alone does not grant automation credentials.
func CanIssueKey(owner *core.Record) bool {
	if owner == nil {
		return false
	}
	for _, tags := range [][]string{owner.GetStringSlice("tag"), owner.GetStringSlice("tags")} {
		for _, tag := range tags {
			switch strings.ToLower(strings.TrimSpace(tag)) {
			case "founding_supporter", "supporter", "designer", "moderator", "developer":
				return true
			}
		}
	}
	return false
}
