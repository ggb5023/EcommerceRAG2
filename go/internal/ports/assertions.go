package ports

import (
	"context"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"google.golang.org/grpc"

	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
)

// Compile-time proof that the concrete types already satisfy the interfaces. No
// call site changed; if any of these lines fails to compile the port is wrong.
// FactClient is asserted in the gateway package, where its adapter lives.
var (
	_ IngestClient = (ragv1.IngestServiceClient)(nil)
	_ Authority    = (*auth.Authority)(nil)
)

// fakeSubmit only implements the single method the port exposes: the build proves
// the narrowed interface is injectable, not just satisfied by the generated client.
type fakeSubmit struct{}

func (fakeSubmit) Submit(context.Context, *ragv1.SubmitRequest, ...grpc.CallOption) (*ragv1.SubmitResponse, error) {
	return &ragv1.SubmitResponse{}, nil
}

var _ IngestClient = fakeSubmit{}
