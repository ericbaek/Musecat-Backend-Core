package arcadeinternal

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/pocketbase/pocketbase/core"
)

type NativeEditError struct {
	Status int
	Code   string
}

func (e *NativeEditError) Error() string { return e.Code }

// Snapshot the bounded PocketBase body before checking trailing JSON; its reader
// rewinds at EOF, unlike a normal bytes.Reader.
func DecodeNativeEdit(e *core.RequestEvent, out any) error {
	data, err := io.ReadAll(e.Request.Body)
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return e.BadRequestError("invalid_patch", nil)
	}
	if d.Decode(new(any)) != io.EOF {
		return e.BadRequestError("invalid_patch", nil)
	}
	return nil
}

// A replacement revision retains server fields an older client cannot send.
// Identity, ownership and timestamps belong to the new revision, not its source.
func CopyRevisionContent(dst, src *core.Record) {
	if src == nil {
		return
	}
	for _, f := range src.Collection().Fields {
		name := f.GetName()
		switch name {
		case "id", "created", "updated", "createdBy", "arcade", "molecule":
			continue
		}
		dst.Set(name, src.Get(name))
	}
}
