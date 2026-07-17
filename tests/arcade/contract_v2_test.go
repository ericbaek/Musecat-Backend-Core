package arcade_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestContractV2_DraftAndHistoryCustomAPIs(t *testing.T) {
	app := newArcadeTestApp(t)
	ownerToken, owner := createAuthUser(t, app)
	otherToken, _ := createAuthUser(t, app)
	moderatorToken, _ := createAuthUserWithTags(t, app, []string{"moderator"})
	ownerHeaders := map[string]string{"Authorization": "Bearer " + ownerToken}
	otherHeaders := map[string]string{"Authorization": "Bearer " + otherToken}
	moderatorHeaders := map[string]string{"Authorization": "Bearer " + moderatorToken}

	draftID, _ := seedArcade(t, app, owner.Id, arcadeSeed{
		Name:     "Contract Draft",
		Address:  "Private Draft Street",
		Location: location{Lat: 37.5665, Lon: 126.978},
	})
	publicID, _ := seedPublicArcade(t, app, owner.Id, arcadeSeed{
		Name:     "Contract Public",
		Address:  "Public History Street",
		Location: location{Lat: 37.5675, Lon: 126.979},
	})

	assertContractStatus(t, executeJSONRequest(t, app, http.MethodGet, "/arcade?id="+draftID, "", nil), http.StatusNotFound)
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodGet, "/arcade/draft?id="+draftID, "", ownerHeaders), http.StatusOK)
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodGet, "/arcade/draft?id="+draftID, "", otherHeaders), http.StatusNotFound)
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodGet, "/arcade/draft?id="+draftID, "", moderatorHeaders), http.StatusOK)
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodGet, "/arcade/draft?id="+publicID, "", otherHeaders), http.StatusNotFound)

	drafts := decodeJSONMap(t, executeJSONRequest(t, app, http.MethodGet, "/arcade/drafts", "", ownerHeaders))
	if !contractItemsContainID(drafts["items"], draftID) {
		t.Fatalf("expected own private draft %q in custom draft list, got %#v", draftID, drafts["items"])
	}
	if contractItemsContainID(drafts["items"], publicID) {
		t.Fatalf("published arcade %q leaked into custom draft list", publicID)
	}

	assertContractStatus(t, executeJSONRequest(t, app, http.MethodDelete, "/arcade/draft?id="+draftID, "", otherHeaders), http.StatusForbidden)
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodDelete, "/arcade/draft?id="+publicID, "", ownerHeaders), http.StatusConflict)

	publicChangeID := seedArcadeChangelog(t, app, publicID, "basic", owner.Id, time.Now())
	publicHistory := decodeJSONMap(t, executeJSONRequest(t, app, http.MethodGet, "/arcade/changelog?arcade="+publicID+"&page=1&per_page=1", "", nil))
	if got := publicHistory["page"]; got != float64(1) {
		t.Fatalf("expected changelog page 1, got %v", got)
	}
	if got := publicHistory["per_page"]; got != float64(1) {
		t.Fatalf("expected changelog per_page 1, got %v", got)
	}
	if !contractItemsContainID(publicHistory["items"], publicChangeID) {
		t.Fatalf("expected public changelog %q, got %#v", publicChangeID, publicHistory["items"])
	}

	privateChangeID := seedArcadeChangelog(t, app, draftID, "basic", owner.Id, time.Now())
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodGet, "/arcade/changelog?arcade="+draftID, "", nil), http.StatusNotFound)
	privateHistory := decodeJSONMap(t, executeJSONRequest(t, app, http.MethodGet, "/arcade/changelog?arcade="+draftID, "", ownerHeaders))
	if !contractItemsContainID(privateHistory["items"], privateChangeID) {
		t.Fatalf("expected owner to read private changelog %q, got %#v", privateChangeID, privateHistory["items"])
	}

	assertContractStatus(t, executeJSONRequest(t, app, http.MethodDelete, "/arcade/draft?id="+draftID, "", ownerHeaders), http.StatusOK)
	if _, err := app.FindRecordById("arcade", draftID); err == nil {
		t.Fatalf("expected deleted draft %q to be absent", draftID)
	}
}

func TestContractV2_ClosedPublicVisibilityAndListPagination(t *testing.T) {
	app := newArcadeTestApp(t)
	_, owner := createAuthUser(t, app)
	activeOneID, _ := seedPublicArcade(t, app, owner.Id, arcadeSeed{
		Name:     "V2 Active One",
		Address:  "Active One Street",
		Location: location{Lat: 37.5665, Lon: 126.978},
	})
	activeTwoID, _ := seedPublicArcade(t, app, owner.Id, arcadeSeed{
		Name:     "V2 Active Two",
		Address:  "Active Two Street",
		Location: location{Lat: 37.5666, Lon: 126.979},
	})
	closedID, _ := seedPublicArcade(t, app, owner.Id, arcadeSeed{
		Name:     "V2 Closed Searchable",
		Address:  "Closed Street",
		Location: location{Lat: 37.5667, Lon: 126.980},
	})
	setArcadeVisibility(t, app, closedID, true, true)
	privateID, _ := seedArcade(t, app, owner.Id, arcadeSeed{
		Name:     "V2 Private Hidden",
		Address:  "Private Street",
		Location: location{Lat: 37.5668, Lon: 126.981},
	})

	assertContractStatus(t, executeJSONRequest(t, app, http.MethodGet, "/arcade?id="+closedID, "", nil), http.StatusOK)
	closedSearch := decodeSearchPayload(t, executeJSONRequest(t, app, http.MethodGet, "/search?q=closed%20searchable", "", nil))
	if !contractItemsContainID(contractMapsToAny(decodeArcades(t, closedSearch)), closedID) {
		t.Fatalf("expected closed public arcade %q in search, got %#v", closedID, closedSearch["arcades"])
	}

	pageOne := decodeJSONMap(t, executeJSONRequest(t, app, http.MethodGet, "/arcades?page=1&per_page=1", "", nil))
	if got := pageOne["page"]; got != float64(1) || pageOne["per_page"] != float64(1) {
		t.Fatalf("expected pagination envelope, got %#v", pageOne)
	}
	if got := pageOne["total"]; got != float64(2) {
		t.Fatalf("expected only two operating public arcades, got %v", got)
	}
	if pageOne["last_page"] != float64(2) {
		t.Fatalf("expected last_page=2, got %v", pageOne["last_page"])
	}
	if contractItemsContainID(pageOne["items"], closedID) || contractItemsContainID(pageOne["items"], privateID) {
		t.Fatalf("closed or private arcade leaked into /arcades: %#v", pageOne["items"])
	}
	pageTwo := decodeJSONMap(t, executeJSONRequest(t, app, http.MethodGet, "/arcades?page=2&per_page=1", "", nil))
	if !contractItemsContainID(pageOne["items"], activeOneID) && !contractItemsContainID(pageOne["items"], activeTwoID) {
		t.Fatalf("expected first page to contain an active arcade, got %#v", pageOne["items"])
	}
	if !contractItemsContainID(pageTwo["items"], activeOneID) && !contractItemsContainID(pageTwo["items"], activeTwoID) {
		t.Fatalf("expected second page to contain an active arcade, got %#v", pageTwo["items"])
	}
	if contractSameFirstID(pageOne["items"], pageTwo["items"]) {
		t.Fatalf("expected distinct items across list pages: first=%#v second=%#v", pageOne["items"], pageTwo["items"])
	}
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodGet, "/arcades?closed=true", "", nil), http.StatusBadRequest)
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodGet, "/arcades?public=false", "", nil), http.StatusBadRequest)

	nearby := decodeJSONMap(t, executeJSONRequest(t, app, http.MethodGet, "/arcades/nearby?lat=37.5665&lon=126.978", "", nil))
	if contractItemsContainID(nearby["items"], closedID) || contractItemsContainID(nearby["items"], privateID) {
		t.Fatalf("closed or private arcade leaked into nearby: %#v", nearby["items"])
	}
}

func TestContractV2_CustomPhotoAtomsAndRawRESTBoundary(t *testing.T) {
	app := newArcadeTestApp(t)
	ownerToken, owner := createAuthUser(t, app)
	otherToken, _ := createAuthUser(t, app)
	ownerHeaders := map[string]string{"Authorization": "Bearer " + ownerToken}
	otherHeaders := map[string]string{"Authorization": "Bearer " + otherToken}

	publicID, _ := seedPublicArcade(t, app, owner.Id, arcadeSeed{
		Name:     "Photo Contract Public",
		Address:  "Public Photo Street",
		Location: location{Lat: 37.5665, Lon: 126.978},
	})
	privateID, _ := seedArcade(t, app, owner.Id, arcadeSeed{
		Name:     "Photo Contract Draft",
		Address:  "Private Photo Street",
		Location: location{Lat: 37.5666, Lon: 126.978},
	})
	pendingID := seedPhotoAtom(t, app, publicID, owner.Id, false)
	publishedID := seedPhotoAtom(t, app, publicID, owner.Id, true)
	privatePhotoID := seedPhotoAtom(t, app, privateID, owner.Id, true)

	photoList := decodeJSONMap(t, executeJSONRequest(t, app, http.MethodGet, "/arcade/photo/atoms?arcade="+publicID, "", otherHeaders))
	if !contractItemsContainID(photoList["items"], pendingID) || !contractItemsContainID(photoList["items"], publishedID) {
		t.Fatalf("expected custom photo list to include public arcade atoms, got %#v", photoList["items"])
	}
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodDelete, "/arcade/photo/atom?id="+pendingID, "", otherHeaders), http.StatusForbidden)
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodDelete, "/arcade/photo/atom?id="+publishedID, "", ownerHeaders), http.StatusConflict)
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodGet, "/arcade/photo/atoms?arcade="+privateID, "", otherHeaders), http.StatusNotFound)
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodGet, "/arcade/photo/atoms?arcade="+privateID, "", ownerHeaders), http.StatusOK)
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodDelete, "/arcade/photo/atom?id="+pendingID, "", ownerHeaders), http.StatusOK)

	arcades, err := app.FindCollectionByNameOrId("arcade")
	if err != nil {
		t.Fatalf("failed to load arcade collection: %v", err)
	}
	if arcades.ListRule != nil || arcades.CreateRule != nil || arcades.UpdateRule != nil || arcades.DeleteRule != nil || arcades.ViewRule != nil {
		t.Fatalf("expected all raw arcade CRUD rules to be locked")
	}
	photoAtoms, err := app.FindCollectionByNameOrId("arcade_photo_atoms")
	if err != nil {
		t.Fatalf("failed to load photo atom collection: %v", err)
	}
	if photoAtoms.ListRule != nil || photoAtoms.CreateRule != nil || photoAtoms.UpdateRule != nil || photoAtoms.DeleteRule != nil {
		t.Fatalf("expected raw photo atom list and mutations to be locked")
	}
	if photoAtoms.ViewRule == nil || strings.TrimSpace(*photoAtoms.ViewRule) != "public = true && arcade.public = true" {
		t.Fatalf("expected narrow public photo view rule, got %#v", photoAtoms.ViewRule)
	}
	if field, ok := photoAtoms.Fields.GetByName("photo").(*core.FileField); !ok || !field.Protected {
		t.Fatalf("expected photo atom files to be protected by the view rule")
	}

	assertContractStatus(t, executeJSONRequest(t, app, http.MethodGet, "/api/collections/arcade/records", "", nil), http.StatusForbidden)
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodPost, "/api/collections/arcade/records", `{"createdBy":"`+owner.Id+`"}`, ownerHeaders), http.StatusForbidden)
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodDelete, "/api/collections/arcade/records/"+publicID, "", ownerHeaders), http.StatusForbidden)
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodPost, "/api/collections/arcade_photo_atoms/records", `{"arcade":"`+publicID+`"}`, ownerHeaders), http.StatusForbidden)
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodGet, "/api/collections/arcade_photo_atoms/records/"+publishedID, "", nil), http.StatusOK)
	assertContractStatusNotOK(t, executeJSONRequest(t, app, http.MethodGet, "/api/collections/arcade_photo_atoms/records/"+privatePhotoID, "", nil))

	published := mustFindRecord(t, app, "arcade_photo_atoms", publishedID)
	filename := published.GetString("photo")
	if filename == "" {
		t.Fatal("expected public photo atom to have a file")
	}
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodGet, "/api/files/arcade_photo_atoms/"+publishedID+"/"+url.PathEscape(filename), "", nil), http.StatusOK)
	privatePhoto := mustFindRecord(t, app, "arcade_photo_atoms", privatePhotoID)
	assertContractStatusNotOK(t, executeJSONRequest(t, app, http.MethodGet, "/api/files/arcade_photo_atoms/"+privatePhotoID+"/"+url.PathEscape(privatePhoto.GetString("photo")), "", nil))
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodGet, "/arcade/photo/file?id="+privatePhotoID, "", ownerHeaders), http.StatusOK)
}

func TestContractV2_EditReportReviewAndRollbackAtomicity(t *testing.T) {
	app := newArcadeTestApp(t)
	reporterToken, reporter := createAuthUser(t, app)
	_, editor := createAuthUser(t, app)
	moderatorToken, moderator := createAuthUserWithTags(t, app, []string{"moderator"})
	supporterToken, _ := createAuthUserWithTags(t, app, []string{"supporter"})
	reporterHeaders := map[string]string{"Authorization": "Bearer " + reporterToken}
	moderatorHeaders := map[string]string{"Authorization": "Bearer " + moderatorToken}
	supporterHeaders := map[string]string{"Authorization": "Bearer " + supporterToken}

	arcadeID, _ := seedPublicArcade(t, app, reporter.Id, arcadeSeed{
		Name:     "Report Contract Arcade",
		Address:  "Review Queue Street",
		Location: location{Lat: 37.5665, Lon: 126.978},
	})
	changeID := seedArcadeChangelog(t, app, arcadeID, "basic", editor.Id, time.Now())
	reportBody := fmt.Sprintf(`{"arcade":%q,"changelog":%q,"urgency":"medium","message":"incorrect address"}`, arcadeID, changeID)
	report := decodeJSONMap(t, executeJSONRequest(t, app, http.MethodPost, "/arcade/edit_report", reportBody, reporterHeaders))
	reportID, _ := report["id"].(string)
	if reportID == "" || report["kind"] != "edit_report" || report["reported_editor"] != editor.Id || report["status"] != "waiting" {
		t.Fatalf("unexpected edit report payload: %#v", report)
	}

	assertContractStatus(t, executeJSONRequest(t, app, http.MethodPost, "/arcade/edit_report", reportBody, reporterHeaders), http.StatusConflict)
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodGet, "/moderation/arcade/edit-reports", "", reporterHeaders), http.StatusForbidden)
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodGet, "/moderation/arcade/edit-reports", "", supporterHeaders), http.StatusForbidden)
	queue := decodeJSONMap(t, executeJSONRequest(t, app, http.MethodGet, "/moderation/arcade/edit-reports?status=waiting", "", moderatorHeaders))
	if !contractItemsContainID(queue["items"], reportID) {
		t.Fatalf("expected strict reviewer queue to include %q, got %#v", reportID, queue["items"])
	}

	reviewBody := fmt.Sprintf(`{"id":%q,"outcome":"upheld","note":"verified by moderator"}`, reportID)
	reviewed := decodeJSONMap(t, executeJSONRequest(t, app, http.MethodPut, "/moderation/arcade/edit-report", reviewBody, moderatorHeaders))
	if reviewed["status"] != "done" || reviewed["reviewed_by"] != moderator.Id || reviewed["review_outcome"] != "upheld" {
		t.Fatalf("unexpected reviewed report payload: %#v", reviewed)
	}
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodPut, "/moderation/arcade/edit-report", reviewBody, moderatorHeaders), http.StatusConflict)

	tooLongChangeID := seedArcadeChangelog(t, app, arcadeID, "hour", editor.Id, time.Now())
	tooLongBody := fmt.Sprintf(`{"arcade":%q,"changelog":%q,"message":%q}`, arcadeID, tooLongChangeID, strings.Repeat("x", 1201))
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodPost, "/arcade/edit_report", tooLongBody, reporterHeaders), http.StatusBadRequest)

	rollbackArcadeID, targetBasicID := seedArcade(t, app, reporter.Id, arcadeSeed{
		Name:     "Rollback Atomicity",
		Address:  "Rollback Street",
		Location: location{Lat: 37.5665, Lon: 126.978},
	})
	currentBasicID := seedBasicVersion(t, app, rollbackArcadeID, reporter.Id, "Rollback Current")
	rollbackArcade := mustFindRecord(t, app, "arcade", rollbackArcadeID)
	rollbackArcade.Set("basic", currentBasicID)
	if err := app.Save(rollbackArcade); err != nil {
		t.Fatalf("failed to set current rollback basic: %v", err)
	}
	rollbackChangeID := seedArcadeChangelog(t, app, rollbackArcadeID, "basic", reporter.Id, time.Now())
	duplicateBody := fmt.Sprintf(`{"arcade":%q,"changelog":%q,"message":"already reported"}`, rollbackArcadeID, rollbackChangeID)
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodPost, "/arcade/edit_report", duplicateBody, reporterHeaders), http.StatusOK)
	rollbackBody := fmt.Sprintf(`{"arcade":%q,"part":"basic","value":%q,"report":true,"changelog":%q,"report_message":"rollback report"}`, rollbackArcadeID, targetBasicID, rollbackChangeID)
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodPost, "/arcade/rollback", rollbackBody, reporterHeaders), http.StatusConflict)
	if got := mustFindRecord(t, app, "arcade", rollbackArcadeID).GetString("basic"); got != currentBasicID {
		t.Fatalf("duplicate rollback report must roll back the whole transaction: got basic=%q want %q", got, currentBasicID)
	}
}

func assertContractStatus(tb testing.TB, res *http.Response, want int) {
	tb.Helper()
	defer res.Body.Close()
	if res.StatusCode != want {
		tb.Fatalf("expected status %d, got %d", want, res.StatusCode)
	}
}

func assertContractStatusNotOK(tb testing.TB, res *http.Response) {
	tb.Helper()
	defer res.Body.Close()
	if res.StatusCode == http.StatusOK {
		tb.Fatalf("expected blocked response, got %d", res.StatusCode)
	}
}

func contractItemsContainID(raw any, id string) bool {
	items, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if ok && item["id"] == id {
			return true
		}
	}
	return false
}

func contractMapsToAny(items []map[string]any) []any {
	out := make([]any, len(items))
	for i := range items {
		out[i] = items[i]
	}
	return out
}

func contractSameFirstID(left, right any) bool {
	leftItems, leftOK := left.([]any)
	rightItems, rightOK := right.([]any)
	if !leftOK || !rightOK || len(leftItems) == 0 || len(rightItems) == 0 {
		return false
	}
	leftItem, leftOK := leftItems[0].(map[string]any)
	rightItem, rightOK := rightItems[0].(map[string]any)
	return leftOK && rightOK && leftItem["id"] == rightItem["id"]
}

func mustFindRecord(tb testing.TB, app *tests.TestApp, collection, id string) *core.Record {
	tb.Helper()
	record, err := app.FindRecordById(collection, id)
	if err != nil {
		tb.Fatalf("failed to load %s %s: %v", collection, id, err)
	}
	return record
}
