package ingestclient

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The ledger uses a real restricted database role; RPC faults are injected.
// Four-process lifecycle acceptance separately exercises the Python receipt.
func TestDatabasePendingCommandRecovery(t *testing.T) {
	path := os.Getenv("V2_INTEGRATION_ENV")
	if path == "" {
		t.Skip("V2_INTEGRATION_ENV is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("protected configuration unavailable")
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[key] = strings.Trim(value, "\"'")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := pgxpool.New(ctx, values["V2_GATEWAY_DSN"])
	if err != nil {
		t.Fatal("database pool unavailable")
	}
	defer db.Close()
	var database, role string
	var superuser bool
	if db.QueryRow(ctx, `SELECT current_database(),current_user,rolsuper FROM pg_roles WHERE rolname=current_user`).Scan(&database, &role, &superuser) != nil || !strings.Contains(database, "v2_test") || role != "rag_v2_gateway" || superuser {
		t.Fatal("restricted isolated database required")
	}
	actor := Actor{Shop: "shop-demo"}
	if db.QueryRow(ctx, `SELECT t.id,u.id FROM tenant t JOIN app_user u ON u.tenant_id=t.id WHERE t.external_id='v2-demo-tenant' AND u.external_id='dev-user'`).Scan(&actor.Tenant, &actor.User) != nil {
		t.Fatal("fixture identity unavailable")
	}
	request := testCommand()
	request.CommandId = fmt.Sprintf("ledger-recovery-%d", time.Now().UnixNano())
	available, committed, revoked := false, false, false
	executions := 0
	result := &ragv1.IngestCommandResult{CommandId: request.CommandId, ResourceId: request.ResourceId, Status: "cancelled"}
	client := Client{DB: db, Sign: func(context.Context, string, string, string, string) (*ragv1.RequestContext, error) {
		if revoked {
			return nil, status.Error(codes.PermissionDenied, "revoked")
		}
		return &ragv1.RequestContext{}, nil
	}, RPC: commandRPC{
		get: func(context.Context, *ragv1.GetCommandResultRequest) (*ragv1.GetCommandResultResponse, error) {
			if !available {
				return nil, status.Error(codes.Unavailable, "injected")
			}
			if !committed {
				return nil, status.Error(codes.NotFound, "not dispatched")
			}
			return &ragv1.GetCommandResultResponse{Result: result}, nil
		},
		execute: func(context.Context, *ragv1.ExecuteCommandRequest) (*ragv1.ExecuteCommandResponse, error) {
			executions++
			committed = true
			return nil, status.Error(codes.Unavailable, "committed reply lost")
		},
	}}
	out, err := client.Execute(ctx, actor, request)
	if err != nil || out.State != "pending" || executions != 0 {
		t.Fatal("unavailable lookup dispatched or lost ledger")
	}
	available = true
	out, err = client.Poll(ctx, actor, request.CommandId)
	if err != nil || out.State != "applied" || executions != 1 {
		t.Fatal("pending command did not recover")
	}
	out, err = client.Execute(ctx, actor, request)
	if err != nil || out.State != "applied" || executions != 1 {
		t.Fatal("replay repeated mutation")
	}
	var count int
	if db.QueryRow(ctx, `SELECT count(*) FROM admin_audit WHERE tenant_id=$1 AND resource_type='control_command' AND resource_id=$2`, actor.Tenant, request.CommandId).Scan(&count) != nil || count != 2 {
		t.Fatal("intent or result audit duplicated")
	}
	request.Reason = "Changed original command payload"
	if _, err = client.Execute(ctx, actor, request); status.Code(err) != codes.AlreadyExists {
		t.Fatal("changed payload accepted")
	}
	wrong := actor
	wrong.User++
	if _, err = client.Poll(ctx, wrong, request.CommandId); status.Code(err) != codes.PermissionDenied {
		t.Fatal("other actor read command")
	}
	revoked = true
	if _, err = client.Poll(ctx, actor, request.CommandId); status.Code(err) != codes.PermissionDenied || executions != 1 {
		t.Fatal("cached result bypassed revocation")
	}
}
