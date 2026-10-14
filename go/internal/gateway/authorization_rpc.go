package gateway

import (
	"context"
	"errors"
	"net"
	"os"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/ggb5023/EcommerceRAG/go/internal/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

type authorizationRPC struct {
	ragv1.UnimplementedAuthorizationServiceServer
	g *gateway
}

func (s authorizationRPC) ValidateScope(ctx context.Context, r *ragv1.ValidateScopeRequest) (*ragv1.ValidateScopeResponse, error) {
	if r.GetContext() == nil {
		return nil, status.Error(codes.InvalidArgument, "scope_required")
	}
	if err := s.g.authority().ValidateScope(ctx, r.GetContext()); err != nil {
		return nil, err
	}
	return &ragv1.ValidateScopeResponse{Allowed: true}, nil
}
func (g *gateway) startAuthorizationRPC() (*grpc.Server, error) {
	address := os.Getenv("V2_AUTHORITY_GRPC_ADDR")
	if address == "" {
		return nil, nil
	}
	config, err := transport.Config()
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, errors.New("authority RPC bind failed")
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(config)), grpc.MaxRecvMsgSize(128<<10))
	ragv1.RegisterAuthorizationServiceServer(server, authorizationRPC{g: g})
	go func() { _ = server.Serve(listener) }()
	return server, nil
}
