// Package metrics exposes counters, gauges, and histograms in the Prometheus
// text exposition format.
//
// The implementation is deliberately small: labelled metric families, a
// registry, and an HTTP handler. The text format is stable and simple, and
// keeping it in-repo avoids a large dependency tree for a few hundred lines
// of behavior.
package metrics

import (
	"bufio"
	"fmt"
	"math"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Registry holds metric families and renders them.
type Registry struct {
	mu       sync.Mutex
	families []family
	names    map[string]bool
}

type family interface {
	name() string
	write(w *bufio.Writer)
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{names: make(map[string]bool)}
}

func (r *Registry) register(f family) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.names[f.name()] {
		panic("metrics: duplicate metric " + f.name())
	}
	r.names[f.name()] = true
	r.families = append(r.families, f)
}

// Handler serves every registered family plus Go runtime gauges.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		bw := bufio.NewWriter(w)
		r.mu.Lock()
		families := append([]family(nil), r.families...)
		r.mu.Unlock()
		for _, f := range families {
			f.write(bw)
		}
		writeRuntime(bw)
		_ = bw.Flush()
	})
}

func writeRuntime(w *bufio.Writer) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	writeHeader(w, "go_goroutines", "Number of goroutines.", "gauge")
	fmt.Fprintf(w, "go_goroutines %d\n", runtime.NumGoroutine())
	writeHeader(w, "go_memstats_heap_alloc_bytes", "Bytes of allocated heap objects.", "gauge")
	fmt.Fprintf(w, "go_memstats_heap_alloc_bytes %d\n", ms.HeapAlloc)
}

func writeHeader(w *bufio.Writer, name, help, kind string) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
}

// vec is the shared part of a labelled family: one series per distinct
// combination of label values.
type vec[T any] struct {
	fname  string
	help   string
	labels []string
	newFn  func() *T

	mu     sync.Mutex
	series map[string]*T
	keys   map[string][]string
}

func newVec[T any](name, help string, labels []string, newFn func() *T) vec[T] {
	return vec[T]{fname: name, help: help, labels: labels, newFn: newFn,
		series: make(map[string]*T), keys: make(map[string][]string)}
}

func (v *vec[T]) name() string { return v.fname }

func (v *vec[T]) with(values []string) *T {
	if len(values) != len(v.labels) {
		panic(fmt.Sprintf("metrics: %s takes %d label values, got %d", v.fname, len(v.labels), len(values)))
	}
	key := strings.Join(values, "\x00")
	v.mu.Lock()
	defer v.mu.Unlock()
	s, ok := v.series[key]
	if !ok {
		s = v.newFn()
		v.series[key] = s
		v.keys[key] = append([]string(nil), values...)
	}
	return s
}

// each visits series in a stable order.
func (v *vec[T]) each(fn func(labels string, s *T)) {
	v.mu.Lock()
	keys := make([]string, 0, len(v.series))
	for k := range v.series {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	type entry struct {
		labels string
		s      *T
	}
	entries := make([]entry, len(keys))
	for i, k := range keys {
		entries[i] = entry{formatLabels(v.labels, v.keys[k]), v.series[k]}
	}
	v.mu.Unlock()
	for _, e := range entries {
		fn(e.labels, e.s)
	}
}

func formatLabels(names, values []string) string {
	if len(names) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, n := range names {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(n)
		b.WriteString(`="`)
		b.WriteString(escape(values[i]))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

func escape(s string) string { return labelEscaper.Replace(s) }

func formatFloat(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "+Inf"
	case math.IsInf(f, -1):
		return "-Inf"
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// withLabel appends one label to a formatted label set.
func withLabel(labels, name, value string) string {
	pair := name + `="` + escape(value) + `"`
	if labels == "" {
		return "{" + pair + "}"
	}
	return labels[:len(labels)-1] + "," + pair + "}"
}
