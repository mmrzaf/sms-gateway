package config

import "net/url"

// FakeProvider is the configuration of the fake provider binary.
type FakeProvider struct {
	Name          string
	Addr          string
	GatewayDLRURL string
	Secret        string
	LogLevel      string
	Simulation    Simulation
}

// Simulation holds the fake provider's initial simulation settings. They can
// be changed at runtime through the provider's admin API.
type Simulation struct {
	LatencyMS     int
	JitterMS      int
	FailureRate   float64
	TimeoutRate   float64
	RejectRate    float64
	Outage        bool
	DeliveryRatio float64
	DLRDelayMS    int
	DLRJitterMS   int
}

// LoadFakeProvider reads and validates the fake provider configuration.
func LoadFakeProvider(lookup LookupFunc) (FakeProvider, error) {
	r := newReader(lookup)
	c := FakeProvider{
		Name:          r.required("PROVIDER_NAME"),
		Addr:          r.string("PROVIDER_ADDR", ":9001"),
		GatewayDLRURL: r.required("GATEWAY_DLR_URL"),
		Secret:        r.required("PROVIDER_SECRET"),
		LogLevel:      r.oneOf("LOG_LEVEL", "info", "debug", "info", "warn", "error"),
		Simulation: Simulation{
			LatencyMS:     r.int("SIM_LATENCY_MS", 50, 0, 60000),
			JitterMS:      r.int("SIM_JITTER_MS", 50, 0, 60000),
			FailureRate:   r.float("SIM_FAILURE_RATE", 0, 0, 1),
			TimeoutRate:   r.float("SIM_TIMEOUT_RATE", 0, 0, 1),
			RejectRate:    r.float("SIM_REJECT_RATE", 0, 0, 1),
			Outage:        r.bool("SIM_OUTAGE", false),
			DeliveryRatio: r.float("SIM_DELIVERY_RATIO", 0.95, 0, 1),
			DLRDelayMS:    r.int("SIM_DLR_DELAY_MS", 1000, 0, 60000),
			DLRJitterMS:   r.int("SIM_DLR_JITTER_MS", 500, 0, 60000),
		},
	}
	if c.Name != "" && !providerNamePattern.MatchString(c.Name) {
		r.fail("PROVIDER_NAME", "must match %s", providerNamePattern)
	}
	if c.GatewayDLRURL != "" {
		u, err := url.Parse(c.GatewayDLRURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			r.fail("GATEWAY_DLR_URL", "must be an absolute http or https URL")
		}
	}
	return c, r.err()
}
