package ingestclient

import (
	"context"
	"testing"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type commandRPC struct {
	ragv1.IngestServiceClient
	get     func(context.Context, *ragv1.GetCommandResultRequest) (*ragv1.GetCommandResultResponse, error)
	execute func(context.Context, *ragv1.ExecuteCommandRequest) (*ragv1.ExecuteCommandResponse, error)
}

func (r commandRPC) GetCommandResult(ctx context.Context, in *ragv1.GetCommandResultRequest, _ ...grpc.CallOption) (*ragv1.GetCommandResultResponse, error) {
	return r.get(ctx, in)
}

func (r commandRPC) ExecuteCommand(ctx context.Context, in *ragv1.ExecuteCommandRequest, _ ...grpc.CallOption) (*ragv1.ExecuteCommandResponse, error) {
	return r.execute(ctx, in)
}

func testCommand() *ragv1.ExecuteCommandRequest {
	return &ragv1.ExecuteCommandRequest{CommandId: "recover-command", Action: ragv1.IngestCommandAction_INGEST_COMMAND_ACTION_CANCEL, ResourceId: "42", ExpectedFencingEpoch: 1, Reason: "Cancel isolated acceptance job"}
}

func TestReconcileFailureBoundaries(t *testing.T) {
	for _, scenario := range []struct {
		name           string
		lookup         codes.Code
		execution      codes.Code
		pending        bool
		denySignAt     int
		badReceipt     bool
		want           codes.Code
		wantExecutions int
		wantReceipt    bool
	}{
		{"recover_before_dispatch", codes.NotFound, codes.OK, true, 0, false, codes.OK, 1, true},
		{"recover_lost_reply", codes.NotFound, codes.Unavailable, true, 0, false, codes.OK, 1, true},
		{"existing_receipt", codes.OK, codes.OK, true, 0, false, codes.OK, 0, true},
		{"lookup_unavailable", codes.Unavailable, codes.OK, true, 0, false, codes.OK, 0, false},
		{"lookup_timeout", codes.DeadlineExceeded, codes.OK, true, 0, false, codes.OK, 0, false},
		{"lookup_denied", codes.PermissionDenied, codes.OK, true, 0, false, codes.PermissionDenied, 0, false},
		{"revoked_before_dispatch", codes.NotFound, codes.OK, true, 2, false, codes.PermissionDenied, 0, false},
		{"execute_denied", codes.NotFound, codes.PermissionDenied, true, 0, false, codes.PermissionDenied, 1, false},
		{"missing_final_receipt", codes.NotFound, codes.OK, false, 0, false, codes.Internal, 0, false},
		{"wrong_receipt", codes.OK, codes.OK, true, 0, true, codes.Internal, 0, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			request := testCommand()
			_, hash := Payload(request)
			result := &ragv1.IngestCommandResult{CommandId: request.CommandId, ResourceId: request.ResourceId, Status: "cancelled"}
			if scenario.badReceipt {
				result.ResourceId = "other-resource"
			}
			lookups, executions, signatures := 0, 0, 0
			client := Client{Sign: func(ctx context.Context, shop, id, action, digest string) (*ragv1.RequestContext, error) {
				signatures++
				if signatures == scenario.denySignAt {
					return nil, status.Error(codes.PermissionDenied, "revoked")
				}
				if shop != "shop-test" || id != request.CommandId || action != Action(request.Action) || digest != hash {
					t.Fatal("scope binding changed")
				}
				return &ragv1.RequestContext{CommandId: id, CommandAction: action, CommandPayloadSha256: digest, PermissionRevision: "current"}, nil
			}}
			client.RPC = commandRPC{
				get: func(ctx context.Context, in *ragv1.GetCommandResultRequest) (*ragv1.GetCommandResultResponse, error) {
					lookups++
					if lookups == 1 && scenario.lookup != codes.OK {
						return nil, status.Error(scenario.lookup, "injected")
					}
					if ctx.Err() != nil {
						t.Fatal("recovery inherited cancelled context")
					}
					return &ragv1.GetCommandResultResponse{Result: result}, nil
				},
				execute: func(ctx context.Context, in *ragv1.ExecuteCommandRequest) (*ragv1.ExecuteCommandResponse, error) {
					executions++
					_, actual := Payload(in)
					if actual != hash || in.Context.CommandPayloadSha256 != hash {
						t.Fatal("replay changed original payload")
					}
					if scenario.execution != codes.OK {
						return nil, status.Error(scenario.execution, "lost response")
					}
					return &ragv1.ExecuteCommandResponse{Result: result}, nil
				},
			}
			got, err := client.reconcile(context.Background(), Actor{Shop: "shop-test"}, request, hash, scenario.pending)
			if status.Code(err) != scenario.want || executions != scenario.wantExecutions || (got != nil) != scenario.wantReceipt {
				t.Fatalf("code=%v executions=%d receipt=%t", status.Code(err), executions, got != nil)
			}
		})
	}
}

func TestStoredCommandRejectsChangedPayloadAndScope(t *testing.T) {
	request := testCommand()
	data, hash := Payload(request)
	if _, err := storedRequest(data, request.CommandId, Action(request.Action), hash); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ragv1.ExecuteCommandRequest){
		func(r *ragv1.ExecuteCommandRequest) { r.Reason = "Changed payload" },
		func(r *ragv1.ExecuteCommandRequest) { r.CommandId = "other-command" },
		func(r *ragv1.ExecuteCommandRequest) { r.Context = &ragv1.RequestContext{UserId: "stale-user"} },
	} {
		changed := proto.Clone(request).(*ragv1.ExecuteCommandRequest)
		mutate(changed)
		encoded, _ := proto.MarshalOptions{Deterministic: true}.Marshal(changed)
		if _, err := storedRequest(encoded, request.CommandId, Action(request.Action), hash); status.Code(err) != codes.Internal {
			t.Fatal("accepted modified command")
		}
	}
}
