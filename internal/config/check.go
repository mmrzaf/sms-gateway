package config

import "time"

// Check is the configuration of the invariant checker command. The lease
// and sweep settings set the thresholds of its time-based checks and must
// match the running workers.
type Check struct {
	Database      Database
	LeaseDuration time.Duration
	SweepInterval time.Duration
}

// LoadCheck reads the invariant checker configuration.
func LoadCheck(lookup LookupFunc) (Check, error) {
	r := newReader(lookup)
	c := Check{
		Database:      readDatabase(r),
		LeaseDuration: r.duration("LEASE_DURATION", 30*time.Second),
		SweepInterval: r.duration("SWEEP_INTERVAL", 10*time.Second),
	}
	return c, r.err()
}
