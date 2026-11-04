// Package metrics implements the small dependency-free metric registry used by
// the gateway. It intentionally emits a Prometheus text-compatible subset.
package metrics

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var defaultBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type requestKey struct {
	path   string
	status int
}

type histogram struct {
	buckets []float64
	counts  []uint64
	count   uint64
	sum     float64
}

var (
	mu             sync.Mutex
	requests       = map[requestKey]uint64{}
	durations      = map[string]*histogram{}
	authLockouts   uint64
	authRateLimits uint64
)

// ObserveHTTP records one completed HTTP request. path must be a route
// template, never the raw request URL.
func ObserveHTTP(path string, status int, elapsed time.Duration) {
	if path == "" {
		path = "/unmatched"
	}
	key := requestKey{path: path, status: status}
	mu.Lock()
	requests[key]++
	hist := durations[path]
	if hist == nil {
		hist = &histogram{buckets: defaultBuckets, counts: make([]uint64, len(defaultBuckets))}
		durations[path] = hist
	}
	seconds := elapsed.Seconds()
	for index, upper := range hist.buckets {
		if seconds <= upper {
			hist.counts[index]++
		}
	}
	hist.count++
	hist.sum += seconds
	mu.Unlock()
}

func AuthLockout() {
	mu.Lock()
	authLockouts++
	mu.Unlock()
}

func AuthRateLimited() {
	mu.Lock()
	authRateLimits++
	mu.Unlock()
}

// Gauge is a value collected at scrape time by the gateway.
type Gauge struct {
	Name  string
	Value float64
}

// Render returns text exposition with # TYPE lines. It never includes raw
// URLs or request data.
func Render(gauges ...Gauge) string {
	mu.Lock()
	requestSnapshot := make(map[requestKey]uint64, len(requests))
	for key, value := range requests {
		requestSnapshot[key] = value
	}
	durationSnapshot := make(map[string]histogram, len(durations))
	for path, value := range durations {
		copyValue := histogram{buckets: append([]float64(nil), value.buckets...), counts: append([]uint64(nil), value.counts...), count: value.count, sum: value.sum}
		durationSnapshot[path] = copyValue
	}
	lockouts, rateLimits := authLockouts, authRateLimits
	mu.Unlock()

	var out strings.Builder
	out.WriteString("# HELP http_requests_total HTTP requests by route template and status.\n")
	out.WriteString("# TYPE http_requests_total counter\n")
	requestKeys := make([]requestKey, 0, len(requestSnapshot))
	for key := range requestSnapshot {
		requestKeys = append(requestKeys, key)
	}
	sort.Slice(requestKeys, func(i, j int) bool {
		if requestKeys[i].path == requestKeys[j].path {
			return requestKeys[i].status < requestKeys[j].status
		}
		return requestKeys[i].path < requestKeys[j].path
	})
	for _, key := range requestKeys {
		fmt.Fprintf(&out, "http_requests_total{path=%s,status=%s} %d\n",
			quote(key.path), quote(strconv.Itoa(key.status)), requestSnapshot[key])
	}
	out.WriteString("# HELP http_request_duration_seconds HTTP request duration.\n")
	out.WriteString("# TYPE http_request_duration_seconds histogram\n")
	paths := make([]string, 0, len(durationSnapshot))
	for path := range durationSnapshot {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		value := durationSnapshot[path]
		for index, upper := range value.buckets {
			fmt.Fprintf(&out, "http_request_duration_seconds_bucket{path=%s,le=%s} %d\n",
				quote(path), quote(strconv.FormatFloat(upper, 'g', -1, 64)), value.counts[index])
		}
		fmt.Fprintf(&out, "http_request_duration_seconds_bucket{path=%s,le=\"+Inf\"} %d\n", quote(path), value.count)
		fmt.Fprintf(&out, "http_request_duration_seconds_sum{path=%s} %s\n", quote(path), strconv.FormatFloat(value.sum, 'g', -1, 64))
		fmt.Fprintf(&out, "http_request_duration_seconds_count{path=%s} %d\n", quote(path), value.count)
	}
	out.WriteString("# HELP auth_lockouts_total Local account lockouts.\n")
	out.WriteString("# TYPE auth_lockouts_total counter\n")
	fmt.Fprintf(&out, "auth_lockouts_total %d\n", lockouts)
	out.WriteString("# HELP auth_rate_limited_total Local auth rate-limit rejections.\n")
	out.WriteString("# TYPE auth_rate_limited_total counter\n")
	fmt.Fprintf(&out, "auth_rate_limited_total %d\n", rateLimits)
	for _, gauge := range gauges {
		fmt.Fprintf(&out, "# HELP %s %s\n", gauge.Name, gauge.Name)
		fmt.Fprintf(&out, "# TYPE %s gauge\n", gauge.Name)
		fmt.Fprintf(&out, "%s %s\n", gauge.Name, strconv.FormatFloat(gauge.Value, 'g', -1, 64))
	}
	return out.String()
}

func quote(value string) string {
	return strconv.Quote(value)
}
