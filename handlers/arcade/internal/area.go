package arcadeinternal

import (
	"github.com/pocketbase/pocketbase/core"

	"github.com/ericbaek/musecat-backend-core/arcadearea"
)

// LoadArcadeArea returns the stored area of an arcade for write responses.
// A missing arcade yields an empty assignment; the write already succeeded.
func LoadArcadeArea(app core.App, arcadeID string) arcadearea.Assignment {
	rec, err := app.FindRecordById(CollectionArcade, arcadeID)
	if err != nil {
		return arcadearea.Assignment{}
	}
	return arcadearea.FromRecord(rec)
}
