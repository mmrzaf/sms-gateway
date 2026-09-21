// Package config loads process configuration from environment variables.
//
// Every variable is parsed and validated up front. Problems are collected
// rather than returned one at a time, so a misconfigured process reports all
// of its invalid variables in a single error.
package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// LookupFunc returns the value of an environment variable and whether it is set.
// os.LookupEnv satisfies it; tests pass a map-backed function.
type LookupFunc func(key string) (string, bool)

// MapLookup adapts a map to a LookupFunc.
func MapLookup(m map[string]string) LookupFunc {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

// Error lists every configuration problem found while loading.
type Error struct {
	Problems []string
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("invalid configuration:")
	for _, p := range e.Problems {
		b.WriteString("\n  ")
		b.WriteString(p)
	}
	return b.String()
}

// reader parses variables and records problems instead of failing fast.
type reader struct {
	lookup   LookupFunc
	problems []string
}

func newReader(lookup LookupFunc) *reader {
	return &reader{lookup: lookup}
}

func (r *reader) fail(key, format string, args ...any) {
	r.problems = append(r.problems, key+": "+fmt.Sprintf(format, args...))
}

// check records a problem that involves more than one variable.
func (r *reader) check(ok bool, key, format string, args ...any) {
	if !ok {
		r.fail(key, format, args...)
	}
}

func (r *reader) err() error {
	if len(r.problems) == 0 {
		return nil
	}
	sort.Strings(r.problems)
	return &Error{Problems: r.problems}
}

func (r *reader) value(key string) (string, bool) {
	v, ok := r.lookup(key)
	v = strings.TrimSpace(v)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

func (r *reader) required(key string) string {
	v, ok := r.value(key)
	if !ok {
		r.fail(key, "is required")
	}
	return v
}

func (r *reader) string(key, def string) string {
	if v, ok := r.value(key); ok {
		return v
	}
	return def
}

func (r *reader) oneOf(key, def string, allowed ...string) string {
	v := r.string(key, def)
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	r.fail(key, "must be one of %s, got %q", strings.Join(allowed, ", "), v)
	return def
}

func (r *reader) int(key string, def, min, max int) int {
	v, ok := r.value(key)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		r.fail(key, "must be an integer, got %q", v)
		return def
	}
	if n < min || n > max {
		r.fail(key, "must be between %d and %d, got %d", min, max, n)
		return def
	}
	return n
}

func (r *reader) float(key string, def, min, max float64) float64 {
	v, ok := r.value(key)
	if !ok {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		r.fail(key, "must be a number, got %q", v)
		return def
	}
	if f < min || f > max {
		r.fail(key, "must be between %g and %g, got %g", min, max, f)
		return def
	}
	return f
}

// duration parses a positive Go duration such as "50ms" or "24h".
func (r *reader) duration(key string, def time.Duration) time.Duration {
	v, ok := r.value(key)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		r.fail(key, "must be a duration such as 500ms or 10s, got %q", v)
		return def
	}
	if d <= 0 {
		r.fail(key, "must be positive, got %s", v)
		return def
	}
	return d
}
