package arcadeinternal

import (
	"fmt"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// ParsePhotoCaptureTime uses the venue timezone for cameras without an EXIF offset.
func ParsePhotoCaptureTime(raw, timezone string, now time.Time) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		location := time.UTC
		if timezone != "" {
			location, err = time.LoadLocation(timezone)
			if err != nil {
				return time.Time{}, fmt.Errorf("invalid arcade timezone")
			}
		}
		at, err = time.ParseInLocation("2006-01-02T15:04:05", raw, location)
	}
	if err != nil || at.IsZero() || at.After(now) {
		return time.Time{}, fmt.Errorf("captured_at must be a valid capture date no later than now")
	}
	return at.UTC(), nil
}

// PhotoTimestamp never uses updated: publishing or maintenance is not a new photo.
func PhotoTimestamp(atom *core.Record) time.Time {
	if at := atom.GetDateTime("captured_at").Time(); !at.IsZero() {
		return at.UTC()
	}
	return atom.GetDateTime("created").Time().UTC()
}
