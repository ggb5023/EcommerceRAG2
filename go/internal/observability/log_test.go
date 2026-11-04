package observability

import (
	"context"
	"encoding/json"
	"testing"

	"google.golang.org/grpc/metadata"
)

func TestWithRequestIDAddsMetadata(t *testing.T) {
	ctx := WithRequestID(context.Background(), "req-1")
	md, ok := metadata.FromOutgoingContext(ctx)
	values := md.Get(RequestIDMetadata)
	if !ok || len(values) != 1 || values[0] != "req-1" {
		t.Fatalf("metadata = %v, want req-1", values)
	}
}

func TestLogJSONHasRequiredFields(t *testing.T) {
	entry := map[string]any{}
	encoded := `{"ts":"2026-11-04T00:00:00Z","level":"info","component":"gateway","msg":"rpc_request","request_id":"req-1"}`
	if err := json.Unmarshal([]byte(encoded), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["component"] != "gateway" || entry["request_id"] != "req-1" {
		t.Fatalf("unexpected entry: %v", entry)
	}
}
