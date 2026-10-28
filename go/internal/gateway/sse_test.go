package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSSEStreamHeadersAndFrames(t *testing.T) {
	recorder := httptest.NewRecorder()
	stream := newSSEStream(recorder)
	for header, want := range map[string]string{
		"Content-Type":      "text/event-stream",
		"Cache-Control":     "no-cache, no-transform",
		"X-Accel-Buffering": "no",
	} {
		if got := recorder.Header().Get(header); got != want {
			t.Fatalf("%s = %q, want %q", header, got, want)
		}
	}

	stream.frame(7, "delta", `{"text":"x"}`)
	stream.keepAlive()
	if want := "id: 7\nevent: delta\ndata: {\"text\":\"x\"}\n\n: keep-alive\n\n"; recorder.Body.String() != want {
		t.Fatalf("frames = %q, want %q", recorder.Body.String(), want)
	}
	if !recorder.Flushed {
		t.Fatal("frames must be flushed")
	}
}

func TestSSEErrorFrameCarriesCodeWithoutSecrets(t *testing.T) {
	recorder := httptest.NewRecorder()
	stream := newSSEStream(recorder)
	stream.errorFrame("scope_revoked", "authorization failed", "req-1", 403)

	body := recorder.Body.String()
	if !strings.HasPrefix(body, "event: error\ndata: ") || !strings.HasSuffix(body, "\n\n") {
		t.Fatalf("error frame shape = %q", body)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(body, "event: error\ndata: "), "\n\n")), &payload); err != nil {
		t.Fatalf("error payload is not JSON: %v", err)
	}
	for key, want := range map[string]any{"type": "error", "code": "scope_revoked",
		"message": "authorization failed", "request_id": "req-1", "http_status": float64(403)} {
		if payload[key] != want {
			t.Fatalf("%s = %v, want %v", key, payload[key], want)
		}
	}
}

type noFlushWriter struct {
	header http.Header
	body   strings.Builder
}

func (w *noFlushWriter) Header() http.Header            { return w.header }
func (w *noFlushWriter) Write(data []byte) (int, error) { return w.body.Write(data) }
func (w *noFlushWriter) WriteHeader(int)                {}

func TestSSEErrorFrameWhenFlushableWritesNothingWithoutFlusher(t *testing.T) {
	writer := &noFlushWriter{header: http.Header{}}
	stream := newSSEStream(writer)
	stream.errorFrameWhenFlushable("scope_revoked", "authorization failed", "req-1", 403)
	if writer.body.Len() != 0 {
		t.Fatalf("wrote %q to a writer that cannot flush", writer.body.String())
	}
	stream.errorFrame("scope_revoked", "authorization failed", "req-1", 403)
	if !strings.HasPrefix(writer.body.String(), "event: error\ndata: ") {
		t.Fatalf("errorFrame must still write: %q", writer.body.String())
	}
}
