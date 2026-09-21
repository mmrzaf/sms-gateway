package httpx

import "time"

// Time marshals as an RFC 3339 UTC timestamp with millisecond precision,
// for example "2026-09-21T10:15:30.123Z".
type Time time.Time

// TimeLayout is the timestamp format of every API response.
const TimeLayout = "2006-01-02T15:04:05.000Z"

// MarshalJSON implements json.Marshaler.
func (t Time) MarshalJSON() ([]byte, error) {
	return []byte(`"` + time.Time(t).UTC().Format(TimeLayout) + `"`), nil
}

// TimePtr converts an optional time; nil stays nil and encodes as null.
func TimePtr(t *time.Time) *Time {
	if t == nil {
		return nil
	}
	v := Time(*t)
	return &v
}
