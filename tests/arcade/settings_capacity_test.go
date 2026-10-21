package arcade_test

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/ericbaek/musecat-backend-core/service/arcadereport"
	"github.com/ericbaek/musecat-backend-core/service/supportfeedback"
	"github.com/pocketbase/pocketbase/core"
)

func TestDestinationSettingsMaximumRuneRoundTrip(t *testing.T) {
	app := newArcadeTestApp(t)
	defer app.Cleanup()
	token, user := createAuthUserWithTags(t, app, []string{"developer"})
	arcadeID, _ := seedPublicArcade(t, app, user.Id, arcadeSeed{Name: "Settings capacity", Address: "Address", Location: location{Lat: 37.5, Lon: 127}})
	headers := map[string]string{"Authorization": "Bearer " + token}
	col, err := app.FindCollectionByNameOrId("arcade")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"report_settings", "inquiry_settings"} {
		field, ok := col.Fields.GetByName(name).(*core.JSONField)
		if !ok || field.MaxSize != arcadereport.SettingsMaxSize {
			t.Fatalf("fresh %s capacity: %#v", name, field)
		}
	}
	for _, character := range []string{"😀", "<", "\x00"} {
		for _, destination := range []string{"report", "inquiry"} {
			t.Run(destination+"/"+character, func(t *testing.T) {
				settings := supportfeedback.InquirySettingsData{Settings: arcadereport.Settings{Mode: "internal", ContactType: "url", ContactValue: strings.Repeat(character, 2048), Guide: strings.Repeat(character, 2000)}, LoginRequired: true}
				if err := settings.Validate(); err != nil {
					t.Fatal(err)
				}
				payload, err := json.Marshal(struct {
					Arcade string `json:"arcade"`
					supportfeedback.InquirySettingsData
				}{arcadeID, settings})
				if err != nil {
					t.Fatal(err)
				}
				if len(payload) <= arcadereport.LegacySettingsMaxSize {
					t.Fatal("fixture does not exercise old byte ceiling")
				}
				response := executeJSONRequest(t, app, "PUT", "/arcade/"+destination+"-settings", string(payload), headers)
				body, _ := io.ReadAll(response.Body)
				response.Body.Close()
				if response.StatusCode != 200 {
					t.Fatalf("save: %d %s", response.StatusCode, body)
				}
				saved, err := app.FindRecordById("arcade", arcadeID)
				if err != nil {
					t.Fatal(err)
				}
				got := arcadereport.Read(saved)
				if destination == "inquiry" {
					inquiry := supportfeedback.InquirySettings(saved)
					got = inquiry.Settings
					if !inquiry.LoginRequired {
						t.Fatal("lost login requirement")
					}
				}
				if got != settings.Settings {
					t.Fatal("maximum settings changed during save")
				}
				settings.Guide += character
				if settings.Validate() == nil {
					t.Fatal("guide above rune limit accepted")
				}
				settings.Guide = strings.Repeat(character, 2000)
				settings.ContactValue += character
				if settings.Validate() == nil {
					t.Fatal("contact above rune limit accepted")
				}
			})
		}
	}
}
