package arcade_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// Optional base_*_id preconditions: a match writes, a stale value returns 409
// without touching the relation or changelog, and omission keeps last-writer-wins.
func TestArcadeSectionBaseRevisionPreconditions(t *testing.T) {
	for _, tc := range []struct {
		section, path, field, body string
	}{
		{"sns", "/arcade/sns", "base_sns_id", `"sns":[{"type":"twitter","link":"https://new.example/twitter","name":"Musecat"}]`},
		{"gtk", "/arcade/gtk", "base_gtk_id", `"gtk":[{"type":"FreeWifi","bool":true,"note":"wifi"}]`},
		{"hour", "/arcade/hour", "base_hour_id", `"Monday":{"start":1000,"end":2200}`},
	} {
		t.Run(tc.section, func(t *testing.T) {
			app := newArcadeTestApp(t)
			token, user := createAuthUser(t, app)
			arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{Name: "Base " + tc.section, Address: "Base Street", Location: location{Lat: 37.5, Lon: 127}})
			headers := map[string]string{"Authorization": "Bearer " + token}
			current := func() string {
				rec, err := app.FindRecordById("arcade", arcadeID)
				if err != nil {
					t.Fatal(err)
				}
				return rec.GetString(tc.section)
			}
			put := func(base *string, want int) {
				t.Helper()
				body := fmt.Sprintf(`{"arcade":%q,%s`, arcadeID, tc.body)
				if base != nil {
					raw, _ := json.Marshal(*base)
					body += fmt.Sprintf(`,%q:%s`, tc.field, raw)
				}
				res := executeJSONRequest(t, app, http.MethodPut, tc.path, body+"}", headers)
				res.Body.Close()
				if res.StatusCode != want {
					t.Fatalf("%s base=%v status=%d want=%d", tc.path, base, res.StatusCode, want)
				}
			}
			str := func(s string) *string { return &s }

			before, changes := current(), len(loadChangelogRecords(t, app, arcadeID, tc.section))
			put(str("stale0000000000"), http.StatusConflict)
			if current() != before || len(loadChangelogRecords(t, app, arcadeID, tc.section)) != changes {
				t.Fatal("stale base must not change the relation or changelog")
			}

			put(str(" "+before+" "), http.StatusOK) // matching base, surrounding whitespace ignored
			first := current()
			if first == before {
				t.Fatal("matching base must write a new section")
			}

			put(str(before), http.StatusConflict) // the old base is now stale
			if current() != first {
				t.Fatal("stale base after a write must not change the relation")
			}

			put(nil, http.StatusOK) // omitted base keeps last-writer-wins
			if current() == first {
				t.Fatal("omitted base must still write")
			}
		})
	}
}
