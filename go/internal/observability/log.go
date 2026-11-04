// Package observability contains the dependency-free logging and request
// correlation primitives shared by the Go services.
package observability

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"

	"google.golang.org/grpc/metadata"
)

// RequestIDMetadata is the internal gRPC metadata key. The protobuf
// RequestContext.request_id remains the authoritative field; the header lets
// the Python process correlate transport logs before it decodes the scope.
const RequestIDMetadata = "x-request-id"

var outputMu sync.Mutex

// LogJSON writes one JSON object per line. Callers must only pass already
// allowlisted fields: never queries, prompts, tokens, credentials or DSNs.
func LogJSON(level, component, message string, fields map[string]any) {
	entry := map[string]any{
		"ts":        time.Now().UTC().Format(time.RFC3339Nano),
		"level":     level,
		"component": component,
		"msg":       message,
	}
	for key, value := range fields {
		entry[key] = value
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return
	}
	outputMu.Lock()
	_, _ = os.Stdout.Write(append(encoded, '\n'))
	outputMu.Unlock()
}

// WithRequestID appends the internal metadata header when a request ID exists.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	if requestID == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, RequestIDMetadata, requestID)
}

// LogRPC records one outgoing AI RPC without business content.
func LogRPC(component, rpc, requestID string) {
	fields := map[string]any{"rpc": rpc}
	if requestID != "" {
		fields["request_id"] = requestID
	}
	LogJSON("info", component, "rpc_request", fields)
}

// PrepareRPC logs the outgoing call and returns the context carrying the
// internal request-id header.
func PrepareRPC(ctx context.Context, component, rpc, requestID string) context.Context {
	LogRPC(component, rpc, requestID)
	return WithRequestID(ctx, requestID)
}
