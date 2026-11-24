package api

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type mkey struct {
	method, route string
	code          int
}

type dur struct {
	sum   float64
	count uint64
}

// Metrics keeps basic request counters; route labels come from the chi pattern so cardinality is bounded.
type Metrics struct {
	mu       sync.Mutex
	reqs     map[mkey]uint64
	dur      map[string]*dur
	inflight int
}

func NewMetrics() *Metrics { return &Metrics{reqs: map[mkey]uint64{}, dur: map[string]*dur{}} }

func (m *Metrics) start() { m.mu.Lock(); m.inflight++; m.mu.Unlock() }

// methodLabel bounds the method label: clients can send arbitrary method tokens.
func methodLabel(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return m
	}
	return "OTHER"
}

func (m *Metrics) done(method, route string, code int, d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inflight--
	m.reqs[mkey{methodLabel(method), route, code}]++
	x := m.dur[route]
	if x == nil {
		x = &dur{}
		m.dur[route] = x
	}
	x.sum += d.Seconds()
	x.count++
}

// Handler renders Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		var lines []string
		for k, v := range m.reqs {
			lines = append(lines, fmt.Sprintf("http_requests_total{method=%q,route=%q,code=\"%d\"} %d", k.method, k.route, k.code, v))
		}
		for k, v := range m.dur {
			lines = append(lines, fmt.Sprintf("http_request_duration_seconds_sum{route=%q} %g", k, v.sum),
				fmt.Sprintf("http_request_duration_seconds_count{route=%q} %d", k, v.count))
		}
		lines = append(lines, fmt.Sprintf("http_requests_in_flight %d", m.inflight))
		m.mu.Unlock()
		sort.Strings(lines)
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintln(w, strings.Join(lines, "\n"))
	})
}
