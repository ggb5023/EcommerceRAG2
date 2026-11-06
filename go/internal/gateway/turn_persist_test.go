package gateway

import (
	"testing"

	"github.com/ggb5023/EcommerceRAG/go/internal/domain"
)

// The decision row must retain retrieval's rejected list even when generation
// never reaches its chosen callback (ASKING or a generation-before-failure).
func TestTurnTraceRetainsRejectedAfterRetrieval(t *testing.T) {
	trace := &turnTrace{}
	rejected := []domain.RejectedEvidence{{ID: "chunk-a", Reason: domain.ReasonNotAuthorized}}
	trace.setTrace(rejected)
	if len(trace.rejected) != 1 || trace.rejected[0].ID != "chunk-a" {
		t.Fatalf("rejected = %+v", trace.rejected)
	}
	trace.setChosen([]string{"chunk-b"})
	if len(trace.chosen) != 1 || trace.chosen[0] != "chunk-b" {
		t.Fatalf("chosen = %+v", trace.chosen)
	}
	if len(trace.rejected) != 1 || trace.rejected[0].ID != "chunk-a" {
		t.Fatalf("chosen overwrote rejected: %+v", trace.rejected)
	}
	trace.setChosen(nil)
	if len(trace.rejected) != 1 {
		t.Fatalf("empty chosen cleared rejected: %+v", trace.rejected)
	}
}
