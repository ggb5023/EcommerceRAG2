package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// sseStream writes one server-sent-events response.
//
// The frames and their order are fixed: `id`/`event`/`data` triples for stored
// events, `event: error` for authorization failures, and `: keep-alive`
// comments while the stream is idle. Every write is followed by a flush when the
// writer supports it, so proxies do not buffer the stream.
type sseStream struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

// newSSEStream sets the response headers and returns the stream. Headers must be
// set before the first write, so this is the only constructor.
func newSSEStream(w http.ResponseWriter) *sseStream {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)
	return &sseStream{w: w, flusher: flusher}
}

// frame writes one stored event.
func (s *sseStream) frame(sequence int64, kind, data string) {
	fmt.Fprintf(s.w, "id: %d\nevent: %s\ndata: %s\n\n", sequence, kind, data)
}

// flush pushes buffered frames when the writer supports it.
func (s *sseStream) flush() {
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

// errorFrame always writes the frame and flushes when possible.
func (s *sseStream) errorFrame(code, message, requestID string, httpStatus int) {
	payload, _ := json.Marshal(map[string]any{"type": "error", "code": code, "message": message,
		"request_id": requestID, "http_status": httpStatus})
	fmt.Fprintf(s.w, "event: error\ndata: %s\n\n", payload)
	s.flush()
}

// errorFrameWhenFlushable keeps the pre-split behaviour of the outer loop: a
// writer that cannot flush receives nothing at all.
func (s *sseStream) errorFrameWhenFlushable(code, message, requestID string, httpStatus int) {
	if s.flusher == nil {
		return
	}
	s.errorFrame(code, message, requestID, httpStatus)
}

// keepAlive writes a comment frame so idle connections stay open.
func (s *sseStream) keepAlive() {
	fmt.Fprint(s.w, ": keep-alive\n\n")
	s.flush()
}
