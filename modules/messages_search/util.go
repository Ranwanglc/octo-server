package messages_search

import (
	"time"
)

// userTimeZone is the location used for date-only inputs (YYYY-MM-DD). The
// product targets Asia/Shanghai users; once we have per-user TZ negotiation we
// can swap this for a context-derived value.
var userTimeZone = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}()

// parseSentAt accepts RFC3339 ("2026-06-01T08:00:00Z" / "...+08:00") and the
// looser YYYY-MM-DD form. For date-only inputs the start of day or end of day
// (23:59:59 in the user's timezone) is returned depending on `startOfDay`.
//
// Returns (epochSeconds, ok). A return value of (0, false) indicates a parse
// failure; callers translate this into a VALIDATION_ERROR.
func parseSentAt(s string, startOfDay bool) (int64, bool) {
	if s == "" {
		return 0, false
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Unix(), true
	}
	if t, err := time.ParseInLocation("2006-01-02", s, userTimeZone); err == nil {
		if startOfDay {
			return t.Unix(), true
		}
		return t.Add(24*time.Hour - time.Second).Unix(), true
	}
	return 0, false
}

// msToRFC3339 converts an epoch-seconds timestamp (the value indexed in OS) to
// the RFC3339 / ISO-8601 string the API surface returns. int64 to keep parity
// with Doc.Timestamp and avoid 2106 wrap-around (P2-9).
func msToRFC3339(ts int64) string {
	return time.Unix(ts, 0).UTC().Format(time.RFC3339)
}

// monthBucket renders the YYYY-MM bucket used by _search_media. Computed from
// the OS timestamp in the user's timezone so a 2026-01-01 00:30 Asia/Shanghai
// shot stays in 2026-01 even though UTC has it in 2025-12.
func monthBucket(ts int64) string {
	return time.Unix(ts, 0).In(userTimeZone).Format("2006-01")
}
