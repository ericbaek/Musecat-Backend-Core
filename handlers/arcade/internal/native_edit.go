package arcadeinternal

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/pocketbase/pocketbase/core"
)

// NativeEditError is a client-facing native v1 failure. Decoders, validators and
// transaction bodies return it and never write the response themselves, so a
// non-nil error always means "stop handling"; only the handler's final
// RespondNativeEditError call writes the documented `{error}` envelope.
type NativeEditError struct {
	Status int
	Code   string
}

func (e *NativeEditError) Error() string { return e.Code }

// NativeBadRequest returns the 400 error for a stable native v1 code.
func NativeBadRequest(code string) *NativeEditError {
	return &NativeEditError{Status: 400, Code: code}
}

// RespondNativeEditError writes the `{error}` envelope for a NativeEditError and
// returns the JSON write result; any other error is returned unchanged for the
// router's default handling. Call it only as the handler's terminal statement.
func RespondNativeEditError(e *core.RequestEvent, err error) error {
	var nativeErr *NativeEditError
	if errors.As(err, &nativeErr) {
		return e.JSON(nativeErr.Status, map[string]string{"error": nativeErr.Code})
	}
	return err
}

// DecodeNativeEdit strictly decodes a native v1 body into out. It never writes
// to the response: a malformed, unknown-field or multi-document body yields a
// 400 `invalid_patch` NativeEditError and out must not be used.
//
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
		return NativeBadRequest("invalid_patch")
	}
	if d.Decode(new(any)) != io.EOF {
		return NativeBadRequest("invalid_patch")
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
