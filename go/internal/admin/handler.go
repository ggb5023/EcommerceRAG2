package admin

import (
	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
)

type Handler struct {
	DB           *pgxpool.Pool
	Session      *auth.Session
	Authority    *auth.Authority
	Ingest       ragv1.IngestServiceClient
	AIConnection *grpc.ClientConn
	Profile      string
	SigningKey   []byte
	// Set only from the initialized customer service's validated configuration.
	CustomerOrigin string
}
