package admin

import (
	"context"
	"errors"
	"testing"
)

func TestAppendTurnDecisionNeedsTheCallersTransaction(t *testing.T) {
	var store Store
	if err := store.AppendTurnDecision(context.Background(), nil, TurnDecision{}); !errors.Is(err, ErrStoreNeedsTransaction) {
		t.Fatalf("err = %v, want ErrStoreNeedsTransaction", err)
	}
}

// The round-trip and dedup assertions need a real transaction; they live with the
// other integration tests so they run against the isolated database.
func TestAppendTurnDecisionRoundTripAndDedup(t *testing.T) {
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

	var store Store
	decision := TurnDecision{TenantID: tenant, ConversationID: "conv-turn-test", TurnID: "turn-1"}
	if err := store.AppendTurnDecision(ctx, tx, decision); err != nil {
		t.Fatalf("first append: %v", err)
	}
	if err := store.AppendTurnDecision(ctx, tx, decision); err != nil {
		t.Fatalf("duplicate append must not fail: %v", err)
	}
	var rows int
	var chosen, rejected string
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM turn_decision WHERE tenant_id=$1 AND conversation_id='conv-turn-test'`, tenant).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("rows = %d, want 1 (ON CONFLICT DO NOTHING)", rows)
	}
	if err := tx.QueryRow(ctx, `SELECT chosen::text, rejected::text FROM turn_decision WHERE tenant_id=$1 AND conversation_id='conv-turn-test'`, tenant).Scan(&chosen, &rejected); err != nil {
		t.Fatal(err)
	}
	if chosen != "[]" || rejected != "[]" {
		t.Fatalf("chosen=%q rejected=%q, want [] and [] (never NULL)", chosen, rejected)
	}
}
