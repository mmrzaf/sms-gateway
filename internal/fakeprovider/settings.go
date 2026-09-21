package fakeprovider

import (
	"fmt"

	"github.com/mmrzaf/sms-gatway/internal/httpx"
)

// Settings control the simulated behavior. Rates are probabilities between 0
// and 1; durations are milliseconds between 0 and 60,000.
type Settings struct {
	LatencyMS     int     `json:"latency_ms"`
	JitterMS      int     `json:"jitter_ms"`
	FailureRate   float64 `json:"failure_rate"`
	TimeoutRate   float64 `json:"timeout_rate"`
	RejectRate    float64 `json:"reject_rate"`
	Outage        bool    `json:"outage"`
	DeliveryRatio float64 `json:"delivery_ratio"`
	DLRDelayMS    int     `json:"dlr_delay_ms"`
	DLRJitterMS   int     `json:"dlr_jitter_ms"`
}

// DefaultSettings are the settings used when none are configured.
var DefaultSettings = Settings{
	LatencyMS:     50,
	JitterMS:      50,
	DeliveryRatio: 0.95,
	DLRDelayMS:    1000,
	DLRJitterMS:   500,
}

const maxDurationMS = 60_000

// SettingsUpdate changes some settings; nil fields keep their value.
type SettingsUpdate struct {
	LatencyMS     *int     `json:"latency_ms"`
	JitterMS      *int     `json:"jitter_ms"`
	FailureRate   *float64 `json:"failure_rate"`
	TimeoutRate   *float64 `json:"timeout_rate"`
	RejectRate    *float64 `json:"reject_rate"`
	Outage        *bool    `json:"outage"`
	DeliveryRatio *float64 `json:"delivery_ratio"`
	DLRDelayMS    *int     `json:"dlr_delay_ms"`
	DLRJitterMS   *int     `json:"dlr_jitter_ms"`
}

// Apply returns s with the update applied, or a validation error listing
// every out-of-range field.
func (u SettingsUpdate) Apply(s Settings) (Settings, error) {
	var problems []httpx.FieldError
	duration := func(field string, v *int, dst *int) {
		if v == nil {
			return
		}
		if *v < 0 || *v > maxDurationMS {
			problems = append(problems, httpx.FieldError{Field: field, Code: "out_of_range",
				Message: fmt.Sprintf("must be between 0 and %d", maxDurationMS)})
			return
		}
		*dst = *v
	}
	ratio := func(field string, v *float64, dst *float64) {
		if v == nil {
			return
		}
		if *v < 0 || *v > 1 {
			problems = append(problems, httpx.FieldError{Field: field, Code: "out_of_range",
				Message: "must be between 0 and 1"})
			return
		}
		*dst = *v
	}

	duration("latency_ms", u.LatencyMS, &s.LatencyMS)
	duration("jitter_ms", u.JitterMS, &s.JitterMS)
	ratio("failure_rate", u.FailureRate, &s.FailureRate)
	ratio("timeout_rate", u.TimeoutRate, &s.TimeoutRate)
	ratio("reject_rate", u.RejectRate, &s.RejectRate)
	if u.Outage != nil {
		s.Outage = *u.Outage
	}
	ratio("delivery_ratio", u.DeliveryRatio, &s.DeliveryRatio)
	duration("dlr_delay_ms", u.DLRDelayMS, &s.DLRDelayMS)
	duration("dlr_jitter_ms", u.DLRJitterMS, &s.DLRJitterMS)

	if len(problems) > 0 {
		return Settings{}, httpx.ValidationError(problems...)
	}
	return s, nil
}
