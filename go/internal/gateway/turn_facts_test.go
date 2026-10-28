package gateway

import (
	"context"
	"errors"
	"testing"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

type spyFactClient struct {
	calls      int
	names      []string
	resourceID string
	rows       []*ragv1.Fact
	err        error
}

func (s *spyFactClient) Lookup(_ context.Context, _ *ragv1.RequestContext, names []string, resourceID string) ([]*ragv1.Fact, error) {
	s.calls++
	s.names = names
	s.resourceID = resourceID
	return s.rows, s.err
}

func TestLookupFactsValidSKUCallsClientOnce(t *testing.T) {
	client := &spyFactClient{rows: []*ragv1.Fact{{ToolName: "get_stock"}, {ToolName: "get_price"}}}
	result, err := lookupFacts(client, factInput{ctx: context.Background(), names: []string{"get_stock", "get_price"}, resourceID: "SKU-CUP-480"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.calls != 1 {
		t.Fatalf("client called %d times, want 1", client.calls)
	}
	if client.resourceID != "SKU-CUP-480" || len(client.names) != 2 {
		t.Fatalf("client received names=%v resource=%q", client.names, client.resourceID)
	}
	if len(result.facts) != 2 {
		t.Fatalf("returned %d facts, want 2", len(result.facts))
	}
}

func TestLookupFactsClientErrorIsNotSwallowed(t *testing.T) {
	client := &spyFactClient{err: errors.New("provider down")}
	result, err := lookupFacts(client, factInput{ctx: context.Background(), names: []string{"get_stock"}, resourceID: "SKU-CUP-480"})
	if err == nil {
		t.Fatal("a client error must be returned so the caller fails closed")
	}
	if len(result.facts) != 0 {
		t.Fatalf("error path returned %d facts", len(result.facts))
	}
	if client.calls != 1 {
		t.Fatalf("client called %d times, want 1", client.calls)
	}
}

func TestLookupFactsWithoutToolsSkipsClient(t *testing.T) {
	client := &spyFactClient{rows: []*ragv1.Fact{{ToolName: "get_stock"}}}
	result, err := lookupFacts(client, factInput{ctx: context.Background(), resourceID: ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.calls != 0 {
		t.Fatalf("client must not be called without tools, got %d calls", client.calls)
	}
	if len(result.facts) != 0 {
		t.Fatalf("returned %d facts, want none", len(result.facts))
	}
}
