package gateway

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/metrics"
	"github.com/ggb5023/EcommerceRAG/go/internal/observability"
)

// observeHTTP records route-template metrics after the inner router has set
// Request.Pattern. It never uses the raw request URL as a label.
func observeHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		metrics.ObserveHTTP(routeTemplate(r), recorder.status, time.Since(start))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	return r.ResponseWriter.Write(body)
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func routeTemplate(r *http.Request) string {
	if pattern := r.Pattern; pattern != "" {
		if index := strings.IndexByte(pattern, ' '); index >= 0 {
			return pattern[index+1:]
		}
		return pattern
	}
	switch {
	case strings.HasPrefix(r.URL.Path, "/v1/auth/"):
		return "/v1/auth/*"
	case strings.HasPrefix(r.URL.Path, "/v1/customer/"):
		return "/v1/customer/*"
	default:
		return "/unmatched"
	}
}

// startMetricsServer binds the dedicated loopback listener. It is intentionally
// separate from the public gateway listener so D1 cannot expose it.
func startMetricsServer(g *gateway) (*http.Server, error) {
	addr := strings.TrimSpace(os.Getenv("METRICS_ADDR"))
	if addr == "" {
		addr = "127.0.0.1:9090"
	}
	if err := validateLoopbackAddress(addr); err != nil {
		return nil, fmt.Errorf("METRICS_ADDR must be loopback: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/metrics", func(w http.ResponseWriter, r *http.Request) {
		gauges, err := g.queueGauges(r.Context())
		if err != nil {
			observability.LogJSON("error", "gateway", "metrics_queue_unavailable", nil)
			http.Error(w, "metrics unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte(metrics.Render(gauges...)))
	})
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			observability.LogJSON("error", "gateway", "metrics_server_stopped", nil)
		}
	}()
	return server, nil
}

func validateLoopbackAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if host == "localhost" {
		host = "127.0.0.1"
	}
	parsed := net.ParseIP(host)
	if parsed == nil {
		return fmt.Errorf("host is not an IP address")
	}
	if !parsed.IsLoopback() {
		return fmt.Errorf("host is not loopback")
	}
	var value int
	if _, err := fmt.Sscanf(port, "%d", &value); err != nil || value < 1 || value > 65535 {
		return fmt.Errorf("port is invalid")
	}
	return nil
}

func (g *gateway) queueGauges(ctx context.Context) ([]metrics.Gauge, error) {
	if g.db == nil {
		return nil, fmt.Errorf("database unavailable")
	}
	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var pending int64
	var age float64
	err := g.db.QueryRow(queryCtx, `SELECT count(*) FILTER (WHERE status IN ('pending','running')),
		coalesce(extract(epoch FROM now()-min(created_at) FILTER (WHERE status IN ('pending','running'))),0)
		FROM ingest_job`).Scan(&pending, &age)
	if err != nil {
		return nil, err
	}
	return []metrics.Gauge{
		{Name: "ai_jobs_pending", Value: float64(pending)},
		{Name: "ai_job_age_seconds", Value: age},
	}, nil
}
