package delegation_test

import (
	"github.com/ericbaek/musecat-backend-core/delegation"
	"github.com/pocketbase/dbx"
	"testing"
)

func TestUsageCountsAttributedRequestsAndErrors(t *testing.T) {
	f := newFixture(t)
	tokens := f.exchange(f.authorize("arcade:read"), verifier, 200)
	f.access(tokens["access_token"].(string), "/developer/v1/arcades/nearby?lat=37.5&lon=127", 200)
	f.access(tokens["access_token"].(string), "/developer/v1/me", 403)
	f.access("unknown-token", "/developer/v1/arcades/nearby?lat=37.5&lon=127", 401)
	var rows []struct {
		Kind     string `db:"kind"`
		Requests int    `db:"requests"`
		Errors   int    `db:"errors"`
	}
	if err := f.app.DB().NewQuery("SELECT kind,requests,errors FROM oauth_client_usage WHERE client={:client} ORDER BY kind").Bind(dbx.Params{"client": "chuniinfo"}).All(&rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Kind != "api" || rows[0].Requests != 2 || rows[0].Errors != 1 || rows[1].Kind != "code" || rows[1].Requests != 1 {
		t.Fatalf("unexpected counters: %+v", rows)
	}
	client := f.m.AccessClient(f.app, tokens["access_token"].(string))
	if client != "chuniinfo" {
		t.Fatal(client)
	}
	accounts, total, err := f.m.ConnectedAccounts(f.app, client, 1)
	if err != nil || total != 1 || len(accounts) != 1 {
		t.Fatalf("accounts %v total %d err %v", accounts, total, err)
	}
	if accounts[0].Name != "" || accounts[0].Subject == f.user.Id {
		t.Fatal("non-profile grant leaked identity")
	}
	grants, err := f.m.ListGrants(f.app, f.user.Id)
	if err != nil || len(grants) != 1 {
		t.Fatal(grants, err)
	}
	if err := delegation.RevokeGrant(f.app, f.user.Id, grants[0].ID); err != nil {
		t.Fatal(err)
	}
	_, total, err = f.m.ConnectedAccounts(f.app, client, 1)
	if err != nil || total != 0 {
		t.Fatalf("revoked grant counted: %d %v", total, err)
	}
}
