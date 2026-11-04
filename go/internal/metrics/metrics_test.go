package metrics

import (
	"strings"
	"testing"
	"time"
)

func TestRenderContainsRequiredSeries(t *testing.T) {
	mu.Lock()
	requests = map[requestKey]uint64{}
	durations = map[string]*histogram{}
	authLockouts = 0
	authRateLimits = 0
	mu.Unlock()
	ObserveHTTP("/v1/session", 200, 2*time.Millisecond)
	AuthLockout()
	AuthRateLimited()
	body := Render(Gauge{Name: "ai_jobs_pending", Value: 2})
	for _, want := range []string{
		"# TYPE http_requests_total counter",
		`http_requests_total{path="/v1/session",status="200"} 1`,
		"# TYPE http_request_duration_seconds histogram",
		"http_request_duration_seconds_bucket{path=\"/v1/session\",le=\"+Inf\"} 1",
		"auth_lockouts_total 1",
		"auth_rate_limited_total 1",
		"# TYPE ai_jobs_pending gauge",
		"ai_jobs_pending 2",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("render missing %s\n%s", want, body)
		}
	}
}
