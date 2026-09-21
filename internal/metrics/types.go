package metrics

import (
	"bufio"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
)

// Counter is a monotonically increasing value.
type Counter struct{ bits atomic.Uint64 }

// Add increases the counter by v, which must not be negative.
func (c *Counter) Add(v float64) {
	for {
		old := c.bits.Load()
		if c.bits.CompareAndSwap(old, math.Float64bits(math.Float64frombits(old)+v)) {
			return
		}
	}
}

// Inc increases the counter by one.
func (c *Counter) Inc() { c.Add(1) }

// Value returns the current value.
func (c *Counter) Value() float64 { return math.Float64frombits(c.bits.Load()) }

// Gauge is a value that can go up and down.
type Gauge struct{ bits atomic.Uint64 }

// Set replaces the value.
func (g *Gauge) Set(v float64) { g.bits.Store(math.Float64bits(v)) }

// Add changes the value by v.
func (g *Gauge) Add(v float64) {
	for {
		old := g.bits.Load()
		if g.bits.CompareAndSwap(old, math.Float64bits(math.Float64frombits(old)+v)) {
			return
		}
	}
}

// Value returns the current value.
func (g *Gauge) Value() float64 { return math.Float64frombits(g.bits.Load()) }

// Histogram counts observations in cumulative buckets.
type Histogram struct {
	mu      sync.Mutex
	bounds  []float64
	buckets []uint64
	count   uint64
	sum     float64
}

// Observe records one value.
func (h *Histogram) Observe(v float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	i := sort.SearchFloat64s(h.bounds, v)
	if i < len(h.buckets) {
		h.buckets[i]++
	}
	h.count++
	h.sum += v
}

// Count returns the number of observations.
func (h *Histogram) Count() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.count
}

// CounterVec is a family of counters with labels.
type CounterVec struct{ vec[Counter] }

// NewCounter registers a counter family.
func (r *Registry) NewCounter(name, help string, labels ...string) *CounterVec {
	v := &CounterVec{newVec(name, help, labels, func() *Counter { return &Counter{} })}
	r.register(v)
	return v
}

// With returns the counter for the label values, in label order.
func (v *CounterVec) With(values ...string) *Counter { return v.with(values) }

func (v *CounterVec) write(w *bufio.Writer) {
	writeHeader(w, v.fname, v.help, "counter")
	v.each(func(labels string, c *Counter) {
		fmt.Fprintf(w, "%s%s %s\n", v.fname, labels, formatFloat(c.Value()))
	})
}

// GaugeVec is a family of gauges with labels.
type GaugeVec struct{ vec[Gauge] }

// NewGauge registers a gauge family.
func (r *Registry) NewGauge(name, help string, labels ...string) *GaugeVec {
	v := &GaugeVec{newVec(name, help, labels, func() *Gauge { return &Gauge{} })}
	r.register(v)
	return v
}

// With returns the gauge for the label values, in label order.
func (v *GaugeVec) With(values ...string) *Gauge { return v.with(values) }

func (v *GaugeVec) write(w *bufio.Writer) {
	writeHeader(w, v.fname, v.help, "gauge")
	v.each(func(labels string, g *Gauge) {
		fmt.Fprintf(w, "%s%s %s\n", v.fname, labels, formatFloat(g.Value()))
	})
}

// HistogramVec is a family of histograms with labels.
type HistogramVec struct{ vec[Histogram] }

// NewHistogram registers a histogram family with the given upper bounds,
// which must be sorted ascending.
func (r *Registry) NewHistogram(name, help string, bounds []float64, labels ...string) *HistogramVec {
	v := &HistogramVec{newVec(name, help, labels, func() *Histogram {
		return &Histogram{bounds: bounds, buckets: make([]uint64, len(bounds))}
	})}
	r.register(v)
	return v
}

// With returns the histogram for the label values, in label order.
func (v *HistogramVec) With(values ...string) *Histogram { return v.with(values) }

func (v *HistogramVec) write(w *bufio.Writer) {
	writeHeader(w, v.fname, v.help, "histogram")
	v.each(func(labels string, h *Histogram) {
		h.mu.Lock()
		defer h.mu.Unlock()
		var cumulative uint64
		for i, bound := range h.bounds {
			cumulative += h.buckets[i]
			fmt.Fprintf(w, "%s_bucket%s %d\n", v.fname, withLabel(labels, "le", formatFloat(bound)), cumulative)
		}
		fmt.Fprintf(w, "%s_bucket%s %d\n", v.fname, withLabel(labels, "le", "+Inf"), h.count)
		fmt.Fprintf(w, "%s_sum%s %s\n", v.fname, labels, formatFloat(h.sum))
		fmt.Fprintf(w, "%s_count%s %d\n", v.fname, labels, h.count)
	})
}

// ExponentialBuckets returns count bounds starting at start, each factor
// times the previous.
func ExponentialBuckets(start, factor float64, count int) []float64 {
	b := make([]float64, count)
	for i := range b {
		b[i] = start
		start *= factor
	}
	return b
}

// Add increases a counter family that has no labels.
func (v *CounterVec) Add(x float64) { v.With().Add(x) }

// Inc increases a counter family that has no labels by one.
func (v *CounterVec) Inc() { v.With().Inc() }

// Set sets a gauge family that has no labels.
func (v *GaugeVec) Set(x float64) { v.With().Set(x) }

// Add changes a gauge family that has no labels.
func (v *GaugeVec) Add(x float64) { v.With().Add(x) }

// Observe records a value in a histogram family that has no labels.
func (v *HistogramVec) Observe(x float64) { v.With().Observe(x) }
