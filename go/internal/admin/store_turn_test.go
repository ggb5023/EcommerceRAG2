package admin

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"
)

func TestAppendTurnDecisionNeedsTheCallersTransaction(t *testing.T) {
	var store Store
	if err := store.AppendTurnDecision(context.Background(), nil, TurnDecision{}); !errors.Is(err, ErrStoreNeedsTransaction) {
		t.Fatalf("err = %v, want ErrStoreNeedsTransaction", err)
	}
}

// The round-trip and overwrite assertions need a real transaction; they live
// with the other integration tests so they run against the isolated database.
func TestAppendTurnDecisionRoundTripAndOverwrite(t *testing.T) {
	api, migration, _, alias, _ := integrationHandlerWithIngest(t, false)
	_ = api
	ctx := context.Background()
	var tenant int64
	if err := migration.QueryRow(ctx, `SELECT id FROM tenant WHERE external_id=$1`, alias).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	tx, err := migration.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var userID, conversationID, turnID, executionID int64
	stamp := time.Now().UnixNano()
	if err = tx.QueryRow(ctx, `INSERT INTO app_user(tenant_id,external_id,role,shop_ids)
		VALUES($1,$2,'operator',ARRAY['shop-a']) RETURNING id`, tenant, fmt.Sprintf("turn-decision-user-%d", stamp)).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO conversation(tenant_id,user_id,title) VALUES($1,$2,'turn-decision-test') RETURNING id`, tenant, userID).Scan(&conversationID); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO turn(tenant_id,conversation_id,turn_no,request_id,user_query,status)
		VALUES($1,$2,1,$3,'q','EXECUTING') RETURNING id`, tenant, conversationID, fmt.Sprintf("turn-decision-%d", stamp)).Scan(&turnID); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO conversation_execution(tenant_id,turn_id,execution_no,request_id,status)
		VALUES($1,$2,1,$3,'EXECUTING') RETURNING id`, tenant, turnID, fmt.Sprintf("turn-decision-exec-%d", stamp)).Scan(&executionID); err != nil {
		t.Fatal(err)
	}

	var store Store
	decision := TurnDecision{TenantID: tenant, ExecutionID: executionID,
		ConversationID: strconv.FormatInt(conversationID, 10), TurnID: strconv.FormatInt(turnID, 10),
		State: "ASKING", Chosen: []byte(`["doc-a"]`)}
	if err = store.AppendTurnDecision(ctx, tx, decision); err != nil {
		t.Fatalf("first append: %v", err)
	}
	// The same execution is replayed with its terminal state; the later write
	// wins instead of being ignored.
	decision.State = "DONE"
	decision.Chosen = []byte(`["doc-b"]`)
	if err = store.AppendTurnDecision(ctx, tx, decision); err != nil {
		t.Fatalf("overwrite append: %v", err)
	}
	var rows int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM turn_decision WHERE tenant_id=$1 AND execution_id=$2`, tenant, executionID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("rows = %d, want 1 (ON CONFLICT (tenant_id, execution_id) DO UPDATE)", rows)
	}
	var state, chosen, rejected string
	if err = tx.QueryRow(ctx, `SELECT state, chosen::text, rejected::text FROM turn_decision WHERE tenant_id=$1 AND execution_id=$2`, tenant, executionID).Scan(&state, &chosen, &rejected); err != nil {
		t.Fatal(err)
	}
	if state != "DONE" || chosen != `["doc-b"]` || rejected != "[]" {
		t.Fatalf("state=%q chosen=%q rejected=%q, want DONE, [\"doc-b\"], []", state, chosen, rejected)
	}
	if err = store.AppendTurnDecision(ctx, nil, decision); !errors.Is(err, ErrStoreNeedsTransaction) {
		t.Fatalf("nil tx err = %v, want ErrStoreNeedsTransaction", err)
	}
}
