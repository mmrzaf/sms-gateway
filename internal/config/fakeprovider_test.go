package config

import (
	"strings"
	"testing"
)

func TestLoadFakeProvider(t *testing.T) {
	c, err := LoadFakeProvider(MapLookup(map[string]string{
		"PROVIDER_NAME":    "A",
		"GATEWAY_DLR_URL":  "http://gateway-api:8081/internal/dlr",
		"PROVIDER_SECRET":  "secret",
		"SIM_OUTAGE":       "true",
		"SIM_FAILURE_RATE": "0.25",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":9001" || !c.Simulation.Outage || c.Simulation.FailureRate != 0.25 ||
		c.Simulation.DeliveryRatio != 0.95 || c.Simulation.DLRDelayMS != 1000 {
		t.Errorf("unexpected configuration: %+v", c)
	}
}

func TestLoadFakeProviderRejectsInvalidValues(t *testing.T) {
	_, err := LoadFakeProvider(MapLookup(map[string]string{
		"PROVIDER_NAME":   "A B",
		"GATEWAY_DLR_URL": "/internal/dlr",
		"SIM_OUTAGE":      "maybe",
		"SIM_REJECT_RATE": "2",
	}))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, key := range []string{"PROVIDER_NAME", "GATEWAY_DLR_URL", "PROVIDER_SECRET", "SIM_OUTAGE", "SIM_REJECT_RATE"} {
		if !strings.Contains(err.Error(), key+":") {
			t.Errorf("error does not mention %s:\n%v", key, err)
		}
	}
}
