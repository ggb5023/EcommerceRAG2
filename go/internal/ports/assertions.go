package ports

import (
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"

	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
)

// Compile-time proof that the concrete types already satisfy the interfaces. No
// call site changed; if any of these lines fails to compile the port is wrong.
// FactClient is asserted in the gateway package, where its adapter lives.
var (
	_ IngestClient = (ragv1.IngestServiceClient)(nil)
	_ Authority    = (*auth.Authority)(nil)
)
