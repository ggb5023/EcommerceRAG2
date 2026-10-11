// Package ingestclient records control intent and reconciles Python receipts.
package ingestclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type Actor struct {
	Tenant, User int64
	Shop         string
}
type Signer func(context.Context, string, string, string, string) (*ragv1.RequestContext, error)
type Client struct {
	DB   *pgxpool.Pool
	RPC  ragv1.IngestServiceClient
	Sign Signer
}
type Outcome struct {
	ID, State string
	Result    *ragv1.IngestCommandResult
}

func Action(value ragv1.IngestCommandAction) string {
	return map[ragv1.IngestCommandAction]string{
		ragv1.IngestCommandAction_INGEST_COMMAND_ACTION_REVIEW:   "version.review",
		ragv1.IngestCommandAction_INGEST_COMMAND_ACTION_PUBLISH:  "version.publish",
		ragv1.IngestCommandAction_INGEST_COMMAND_ACTION_ROLLBACK: "version.rollback",
		ragv1.IngestCommandAction_INGEST_COMMAND_ACTION_REVOKE:   "version.revoke",
		ragv1.IngestCommandAction_INGEST_COMMAND_ACTION_RETRY:    "ingestion.retry",
		ragv1.IngestCommandAction_INGEST_COMMAND_ACTION_CANCEL:   "ingestion.cancel",
	}[value]
}

func Payload(request *ragv1.ExecuteCommandRequest) ([]byte, string) {
	copy := proto.Clone(request).(*ragv1.ExecuteCommandRequest)
	copy.Context = nil
	data, _ := proto.MarshalOptions{Deterministic: true}.Marshal(copy)
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:])
}

func (c Client) Execute(ctx context.Context, actor Actor, request *ragv1.ExecuteCommandRequest) (Outcome, error) {
	if request == nil || c.DB == nil || c.RPC == nil || c.Sign == nil || Action(request.Action) == "" || len(request.CommandId) > 200 {
		return Outcome{}, status.Error(codes.InvalidArgument, "invalid_command")
	}
	if request.CommandId == "" {
		_, hash := Payload(request)
		request.CommandId = "cmd-" + hash
	}
	data, hash := Payload(request)
	action := Action(request.Action)
	out := Outcome{ID: request.CommandId, State: "pending"}
	tx, err := c.DB.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `INSERT INTO control_command(tenant_id,command_id,actor_user_id,shop_id,action,resource_id,payload_sha256,request_bytes)
        VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(tenant_id,command_id) DO NOTHING`,
		actor.Tenant, request.CommandId, actor.User, actor.Shop, action, request.ResourceId, hash, data)
	if err != nil {
		return out, err
	}
	if tag.RowsAffected() == 0 {
		var previousHash, shop string
		var user int64
		err = tx.QueryRow(ctx, `SELECT payload_sha256,actor_user_id,shop_id FROM control_command WHERE tenant_id=$1 AND command_id=$2`, actor.Tenant, request.CommandId).Scan(&previousHash, &user, &shop)
		if err != nil {
			return out, err
		}
		if previousHash != hash || user != actor.User || shop != actor.Shop {
			return out, status.Error(codes.AlreadyExists, "command_payload_conflict")
		}
		if err = tx.Commit(ctx); err != nil {
			return out, err
		}
		// A repeated HTTP request reconciles the original immutable command.
		return c.Poll(ctx, actor, request.CommandId)
	}
	_, err = tx.Exec(ctx, `INSERT INTO admin_audit(tenant_id,actor_user_id,action,resource_type,resource_id,reason,result,metadata_json)
        VALUES($1,$2,$3,'control_command',$4,$5,'success',jsonb_build_object('phase','accepted','shop_id',$6::text))`, actor.Tenant, actor.User, action, request.CommandId, request.Reason, actor.Shop)
	if err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, err
	}
	return c.Poll(ctx, actor, request.CommandId)
}

func (c Client) Poll(ctx context.Context, actor Actor, id string) (Outcome, error) {
	out := Outcome{ID: id, State: "pending"}
	if c.DB == nil || c.RPC == nil || c.Sign == nil || id == "" {
		return out, status.Error(codes.InvalidArgument, "invalid_command")
	}
	var data []byte
	var hash, action, shop string
	var user int64
	err := c.DB.QueryRow(ctx, `SELECT request_bytes,payload_sha256,action,shop_id,actor_user_id,status
        FROM control_command WHERE tenant_id=$1 AND command_id=$2`, actor.Tenant, id).Scan(&data, &hash, &action, &shop, &user, &out.State)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, status.Error(codes.NotFound, "command_not_found")
	}
	if err != nil {
		return out, err
	}
	if user != actor.User || shop != actor.Shop {
		return out, status.Error(codes.PermissionDenied, "command_scope_denied")
	}
	request, err := storedRequest(data, id, action, hash)
	if err != nil {
		return out, err
	}
	result, err := c.reconcile(ctx, actor, request, hash, out.State == "pending")
	if err != nil || result == nil {
		return out, err
	}
	return c.record(ctx, actor, request, hash, result)
}

func uncertain(err error) bool {
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.Canceled, codes.Unknown, codes.Internal:
		return true
	}
	return false
}

func (c Client) reconcile(ctx context.Context, actor Actor, request *ragv1.ExecuteCommandRequest, hash string, pending bool) (*ragv1.IngestCommandResult, error) {
	scope, err := c.Sign(ctx, actor.Shop, request.CommandId, Action(request.Action), hash)
	if err != nil {
		return nil, err
	}
	// Always revalidate Python authority, even for locally cached outcomes.
	rpcCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	response, err := c.RPC.GetCommandResult(rpcCtx, &ragv1.GetCommandResultRequest{Context: scope, CommandId: request.CommandId})
	if err == nil {
		return response.GetResult(), validateReceipt(request, response.GetResult())
	}
	if uncertain(err) {
		return nil, nil
	}
	if status.Code(err) != codes.NotFound {
		return nil, err
	}
	if !pending {
		return nil, status.Error(codes.Internal, "command_receipt_missing")
	}
	// A crash between the ledger commit and the first RPC dispatch leaves no
	// receipt. Resend the stored bytes; Python deduplicates by command id.
	scope, err = c.Sign(rpcCtx, actor.Shop, request.CommandId, Action(request.Action), hash)
	if err != nil {
		return nil, err
	}
	request.Context = scope
	executed, rpcErr := c.RPC.ExecuteCommand(rpcCtx, request)
	if rpcErr == nil {
		return executed.GetResult(), validateReceipt(request, executed.GetResult())
	}
	if !uncertain(rpcErr) {
		return nil, rpcErr
	}
	// Detached bounded receipt lookup survives a disconnected browser. It
	// reauthorizes after publication may have changed the permission revision.
	recovery, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	scope, err = c.Sign(recovery, actor.Shop, request.CommandId, Action(request.Action), hash)
	if err != nil {
		return nil, err
	}
	response, err = c.RPC.GetCommandResult(recovery, &ragv1.GetCommandResultRequest{Context: scope, CommandId: request.CommandId})
	if status.Code(err) == codes.NotFound || uncertain(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return response.GetResult(), validateReceipt(request, response.GetResult())
}

func storedRequest(data []byte, id, action, hash string) (*ragv1.ExecuteCommandRequest, error) {
	request := &ragv1.ExecuteCommandRequest{}
	if err := proto.Unmarshal(data, request); err != nil || request.Context != nil || request.CommandId != id || Action(request.Action) != action {
		return nil, status.Error(codes.Internal, "invalid_stored_command")
	}
	_, actual := Payload(request)
	if actual != hash {
		return nil, status.Error(codes.Internal, "invalid_stored_command")
	}
	return request, nil
}

func validateReceipt(request *ragv1.ExecuteCommandRequest, result *ragv1.IngestCommandResult) error {
	if result == nil || result.CommandId != request.CommandId || result.ResourceId != request.ResourceId || result.Status == "" || (result.ErrorCode != "" && result.Status != "rejected") {
		return status.Error(codes.Internal, "invalid_command_receipt")
	}
	return nil
}

func (c Client) record(ctx context.Context, actor Actor, request *ragv1.ExecuteCommandRequest, hash string, result *ragv1.IngestCommandResult) (Outcome, error) {
	state, auditResult := "applied", "success"
	if result.Status == "rejected" {
		state, auditResult = "rejected", "conflict"
	}
	data, err := protojson.Marshal(result)
	out := Outcome{ID: result.CommandId, State: state, Result: result}
	if err != nil {
		return out, err
	}
	tx, err := c.DB.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE control_command SET status=$4,result_json=$5::jsonb,updated_at=now()
        WHERE tenant_id=$1 AND command_id=$2 AND actor_user_id=$3 AND shop_id=$6 AND payload_sha256=$7 AND resource_id=$8 AND action=$9 AND status='pending'`, actor.Tenant, request.CommandId, actor.User, state, data, actor.Shop, hash, request.ResourceId, Action(request.Action))
	if err != nil {
		return out, err
	}
	if tag.RowsAffected() > 0 {
		_, err = tx.Exec(ctx, `INSERT INTO admin_audit(tenant_id,actor_user_id,action,resource_type,resource_id,reason,result,metadata_json)
            SELECT tenant_id,actor_user_id,action,'control_command',command_id,'Python command result reconciled',$3,
              jsonb_build_object('phase','result','shop_id',shop_id,'status',$4::text) FROM control_command WHERE tenant_id=$1 AND command_id=$2`, actor.Tenant, result.CommandId, auditResult, result.Status)
		if err != nil {
			return out, err
		}
	}
	return out, tx.Commit(ctx)
}
