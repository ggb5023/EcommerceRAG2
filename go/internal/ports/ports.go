// Package ports names the narrow interfaces the gateway depends on.
//
// Implementations are NOT injected: the concrete types satisfy these interfaces
// already, so the call sites are unchanged. Deleting this package must leave the
// tree compiling — that is the evidence that it adds no coupling.
package ports

import (
	"context"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"google.golang.org/grpc"
)

// IngestClient is the one RPC call the admin package makes. The generated client
// has a larger method set, so it satisfies this port; a fake only needs Submit.
type IngestClient interface {
	Submit(ctx context.Context, in *ragv1.SubmitRequest, opts ...grpc.CallOption) (*ragv1.SubmitResponse, error)
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
