package arcade_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/pocketbase/pocketbase/tools/filesystem"
)

func TestDownloadArcadePhotoAtom_Thumbnail(t *testing.T) {
	app := newArcadeTestApp(t)
	ownerToken, owner := createAuthUser(t, app)
	arcadeID, _ := seedPublicArcade(t, app, owner.Id, arcadeSeed{
		Name:     "Thumbnail Arcade",
		Address:  "Thumbnail Street",
		Location: location{Lat: 37.5665, Lon: 126.978},
	})
	photoID := seedSizedPhotoAtom(t, app, arcadeID, owner.Id, true, 1200, 900)

	for _, test := range []struct {
		thumb  string
		width  int
		height int
	}{
		{thumb: "96x96", width: 96, height: 96},
		{thumb: "384x384", width: 384, height: 384},
		{thumb: "680x0", width: 680, height: 510},
	} {
		response := executeJSONRequest(t, app, http.MethodGet, "/arcade/photo/file?id="+photoID+"&thumb="+test.thumb, "", nil)
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			t.Fatalf("expected %s thumbnail response 200, got %d", test.thumb, response.StatusCode)
		}
		decoded, _, err := image.Decode(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatalf("failed to decode %s thumbnail response: %v", test.thumb, err)
		}
		if got := decoded.Bounds().Size(); got.X != test.width || got.Y != test.height {
			t.Fatalf("expected %dx%d thumbnail, got %dx%d", test.width, test.height, got.X, got.Y)
		}
		if got := response.Header.Get("Cache-Control"); got != "private, max-age=300" {
			t.Fatalf("public thumbnail cache-control=%q", got)
		}
	}

	unsupported := executeJSONRequest(t, app, http.MethodGet, "/arcade/photo/file?id="+photoID+"&thumb=48x0", "", nil)
	defer unsupported.Body.Close()
	if unsupported.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected unsupported thumbnail response 400, got %d", unsupported.StatusCode)
	}

	privateArcadeID, _ := seedArcade(t, app, owner.Id, arcadeSeed{
		Name:     "Private Thumbnail Arcade",
		Address:  "Private Thumbnail Street",
		Location: location{Lat: 37.5666, Lon: 126.978},
	})
	privatePhotoID := seedSizedPhotoAtom(t, app, privateArcadeID, owner.Id, false, 1200, 900)
	privateResponse := executeJSONRequest(
		t,
		app,
		http.MethodGet,
		"/arcade/photo/file?id="+privatePhotoID+"&thumb=96x96",
		"",
		map[string]string{"Authorization": "Bearer " + ownerToken},
	)
	defer privateResponse.Body.Close()
	if privateResponse.StatusCode != http.StatusOK {
		t.Fatalf("expected private thumbnail response 200, got %d", privateResponse.StatusCode)
	}
	if got := privateResponse.Header.Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("private thumbnail cache-control=%q", got)
	}
}

func seedSizedPhotoAtom(tb testing.TB, app *tests.TestApp, arcadeID, createdBy string, isPublic bool, width, height int) string {
	tb.Helper()

	var contents bytes.Buffer
	source := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			source.Set(x, y, color.RGBA{R: 32, G: 96, B: 160, A: 255})
		}
	}
	if err := png.Encode(&contents, source); err != nil {
		tb.Fatalf("failed to encode photo fixture: %v", err)
	}

	collection, err := app.FindCollectionByNameOrId("arcade_photo_atoms")
	if err != nil {
		tb.Fatalf("failed to load arcade_photo_atoms collection: %v", err)
	}
	record := core.NewRecord(collection)
	record.Set("arcade", arcadeID)
	record.Set("public", isPublic)
	record.Set("createdBy", createdBy)
	file, err := filesystem.NewFileFromBytes(contents.Bytes(), "thumbnail-source.png")
	if err != nil {
		tb.Fatalf("failed to create photo fixture: %v", err)
	}
	record.Set("photo", file)
	if err := app.Save(record); err != nil {
		tb.Fatalf("failed to save photo fixture: %v", err)
	}
	return record.Id
}
