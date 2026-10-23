package arcadereport

import (
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/pocketbase/pocketbase/core"
)

var ErrExternal = errors.New("this arcade accepts reports through its external contact")

type Settings struct {
	Mode         string `json:"mode"`
	ContactType  string `json:"contactType"`
	ContactValue string `json:"contactValue"`
	Guide        string `json:"guide"`
}

func Read(arcade *core.Record) Settings {
	s := Settings{Mode: "internal"}
	if arcade != nil {
		_ = json.Unmarshal([]byte(arcade.GetString("report_settings")), &s)
	}
	if s.Mode != "external" {
		s.Mode = "internal"
	}
	return s
}

func (s *Settings) Validate() error {
	s.ContactValue = strings.TrimSpace(s.ContactValue)
	s.Guide = strings.TrimSpace(s.Guide)
	if s.Mode != "internal" && s.Mode != "external" {
		return errors.New("invalid report mode")
	}
	if utf8.RuneCountInString(s.Guide) > 2000 || utf8.RuneCountInString(s.ContactValue) > 2048 {
		return errors.New("report contact or guide is too long")
	}
	if s.Mode == "internal" {
		return nil
	}
	switch s.ContactType {
	case "url":
		u, err := url.Parse(s.ContactValue)
		if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
			return errors.New("a valid HTTP(S) report URL is required")
		}
	case "phone":
		if !regexp.MustCompile(`^\+?[0-9][0-9 ()-]{4,29}$`).MatchString(s.ContactValue) {
			return errors.New("a valid report phone number is required")
		}
	default:
		return errors.New("external reports require a URL or phone")
	}
	return nil
}

func CanManage(user, arcade *core.Record) bool {
	if user == nil || arcade == nil || user.GetBool("withdrawn") {
		return false
	}
	tags := user.GetStringSlice("tags")
	if slices.Contains(tags, "developer") || slices.Contains(tags, "moderator") {
		return true
	}
	return slices.Contains(tags, "arcade_owner") && slices.Contains(user.GetStringSlice("owns"), arcade.Id)
}
