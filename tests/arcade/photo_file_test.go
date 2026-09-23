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
	_, owner := createAuthUser(t, app)
	arcadeID, _ := seedPublicArcade(t, app, owner.Id, arcadeSeed{
		Name:     "Thumbnail Arcade",
		Address:  "Thumbnail Street",
		Location: location{Lat: 37.5665, Lon: 126.978},
	})
	photoID := seedSizedPhotoAtom(t, app, arcadeID, owner.Id, 1200, 900)

	response := executeJSONRequest(t, app, http.MethodGet, "/arcade/photo/file?id="+photoID+"&thumb=680x0", "", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected thumbnail response 200, got %d", response.StatusCode)
	}
	decoded, _, err := image.Decode(response.Body)
	if err != nil {
		t.Fatalf("failed to decode thumbnail response: %v", err)
	}
	if got := decoded.Bounds().Size(); got.X != 680 || got.Y != 510 {
		t.Fatalf("expected 680x510 thumbnail, got %dx%d", got.X, got.Y)
	}

	unsupported := executeJSONRequest(t, app, http.MethodGet, "/arcade/photo/file?id="+photoID+"&thumb=48x0", "", nil)
	defer unsupported.Body.Close()
	if unsupported.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected unsupported thumbnail response 400, got %d", unsupported.StatusCode)
	}
}

func seedSizedPhotoAtom(tb testing.TB, app *tests.TestApp, arcadeID, createdBy string, width, height int) string {
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
	record.Set("public", true)
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
