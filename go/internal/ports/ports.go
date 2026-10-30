// Package ports names the narrow interfaces the gateway depends on.
//
// Implementations are NOT injected: the concrete types satisfy these interfaces
// already, so the call sites are unchanged. Deleting this package must leave the
// tree compiling — that is the evidence that it adds no coupling.
package ports

import (
	"context"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

// IngestClient is the generated ingest RPC client the admin package holds.
type IngestClient interface {
	ragv1.IngestServiceClient
}

// FactClient fetches authorized facts for one turn.
type FactClient interface {
	Lookup(ctx context.Context, scope *ragv1.RequestContext, names []string, resourceID string) ([]*ragv1.Fact, error)
}

// Authority resolves the caller's permissions and evidence visibility.
type Authority interface {
	Refresh(ctx context.Context) error
	CurrentRevision() string
	EvidenceAllowed(ctx context.Context, item *ragv1.Evidence) (bool, error)
}
