package gateway

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// These lifecycle tests need the isolated integration database because they
// assert the transaction that finishTurn/recovery actually commit.
func lifecycleDBs(t *testing.T) (migration, gatewayDB *pgxpool.Pool) {
	t.Helper()
	path := os.Getenv("V2_INTEGRATION_ENV")
	if path == "" {
		t.Skip("V2_INTEGRATION_ENV is required for lifecycle tests")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("integration configuration unavailable")
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), "\"'")
		}
	}
	connect := func(key string) *pgxpool.Pool {
		dsn := values[key]
		if dsn == "" {
			t.Fatalf("%s is required", key)
		}
		pool, err := pgxpool.New(context.Background(), dsn)
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		t.Cleanup(pool.Close)
		return pool
	}
	return connect("V2_MIGRATION_DSN"), connect("V2_GATEWAY_DSN")
}

type lifecycleFixture struct {
	tenantID, userID, conversationID, turnID, executionID int64
	stamp                                                 string
}

func newLifecycleFixture(t *testing.T, migration *pgxpool.Pool) lifecycleFixture {
	t.Helper()
	ctx := context.Background()
	f := lifecycleFixture{stamp: fmt.Sprintf("%d", time.Now().UnixNano())}
	alias := "lifecycle-" + f.stamp
	if err := migration.QueryRow(ctx, `INSERT INTO tenant(name,external_id) VALUES($1,$1) RETURNING id`, alias).Scan(&f.tenantID); err != nil {
		t.Fatalf("tenant fixture: %v", err)
	}
	t.Cleanup(func() { cleanupLifecycleFixture(t, migration, f.tenantID) })
	if err := migration.QueryRow(ctx, `INSERT INTO app_user(tenant_id,external_id,role,shop_ids)
		VALUES($1,$2,'operator',ARRAY['shop-lifecycle']) RETURNING id`, f.tenantID, "user-"+f.stamp).Scan(&f.userID); err != nil {
		t.Fatalf("user fixture: %v", err)
	}
	if err := migration.QueryRow(ctx, `INSERT INTO conversation(tenant_id,user_id,title) VALUES($1,$2,'lifecycle') RETURNING id`, f.tenantID, f.userID).Scan(&f.conversationID); err != nil {
		t.Fatalf("conversation fixture: %v", err)
	}
	if err := migration.QueryRow(ctx, `INSERT INTO turn(tenant_id,conversation_id,turn_no,request_id,user_query,status)
		VALUES($1,$2,1,$3,'lifecycle query','EXECUTING') RETURNING id`, f.tenantID, f.conversationID, "turn-"+f.stamp).Scan(&f.turnID); err != nil {
		t.Fatalf("turn fixture: %v", err)
	}
	if err := migration.QueryRow(ctx, `INSERT INTO conversation_execution(tenant_id,turn_id,execution_no,request_id,status,is_mock)
		VALUES($1,$2,1,$3,'EXECUTING',true) RETURNING id`, f.tenantID, f.turnID, "exec-"+f.stamp).Scan(&f.executionID); err != nil {
		t.Fatalf("execution fixture: %v", err)
	}
	return f
}

func cleanupLifecycleFixture(t *testing.T, db *pgxpool.Pool, tenantID int64) {
	t.Helper()
	ctx := context.Background()
	for _, statement := range []string{
		`DELETE FROM sse_event WHERE tenant_id=$1`,
		`DELETE FROM customer_reply WHERE tenant_id=$1`,
		`DELETE FROM message WHERE tenant_id=$1`,
		`DELETE FROM citation WHERE tenant_id=$1`,
		`DELETE FROM evidence_snapshot WHERE tenant_id=$1`,
		`DELETE FROM turn_decision WHERE tenant_id=$1`,
		`DELETE FROM retrieval_trace WHERE tenant_id=$1`,
		`DELETE FROM routing_trace WHERE tenant_id=$1`,
		`DELETE FROM tool_call_trace WHERE tenant_id=$1`,
		`DELETE FROM feedback WHERE tenant_id=$1`,
		`DELETE FROM turn_idempotency WHERE tenant_id=$1`,
		`DELETE FROM conversation_execution WHERE tenant_id=$1`,
		`DELETE FROM turn WHERE tenant_id=$1`,
		`DELETE FROM conversation WHERE tenant_id=$1`,
		`DELETE FROM shop WHERE tenant_id=$1`,
		`DELETE FROM admin_audit WHERE tenant_id=$1`,
		`DELETE FROM app_user WHERE tenant_id=$1`,
		`DELETE FROM tenant WHERE id=$1`,
	} {
		if _, err := db.Exec(ctx, statement, tenantID); err != nil {
			t.Logf("fixture cleanup skipped: %v", err)
		}
	}
}

func lifecycleIdentity(f lifecycleFixture, executionID, executionNo int64) turnEventIdentity {
	return turnEventIdentity{tenantID: f.tenantID, executionID: executionID, turnID: f.turnID,
		conversationID: f.conversationID, executionNo: executionNo,
		requestID: fmt.Sprintf("req-%s-%d", f.stamp, executionNo)}
}

func lifecycleExecState(t *testing.T, db *pgxpool.Pool, tenantID, executionID int64) string {
	t.Helper()
	var state string
	if err := db.QueryRow(context.Background(), `SELECT status FROM conversation_execution WHERE tenant_id=$1 AND id=$2`, tenantID, executionID).Scan(&state); err != nil {
		t.Fatalf("execution state: %v", err)
	}
	return state
}

func lifecycleTurnState(t *testing.T, db *pgxpool.Pool, tenantID, turnID int64) string {
	t.Helper()
	var state string
	if err := db.QueryRow(context.Background(), `SELECT status FROM turn WHERE tenant_id=$1 AND id=$2`, tenantID, turnID).Scan(&state); err != nil {
		t.Fatalf("turn state: %v", err)
	}
	return state
}

func lifecycleLedgerState(t *testing.T, db *pgxpool.Pool, tenantID, executionID int64) string {
	t.Helper()
	var state string
	if err := db.QueryRow(context.Background(), `SELECT state FROM turn_decision WHERE tenant_id=$1 AND execution_id=$2`, tenantID, executionID).Scan(&state); err != nil {
		t.Fatalf("ledger state: %v", err)
	}
	return state
}

// A resume creates a second execution for the same turn. The ASKING row must
// stay with the first execution and the final ledger row must be DONE.
func TestTurnDecisionResumeFinalState(t *testing.T) {
	migration, db := lifecycleDBs(t)
	ctx := context.Background()
	f := newLifecycleFixture(t, migration)

	finishTurn(ctx, db, lifecycleIdentity(f, f.executionID, 1), "ASKING", "", "please clarify", true, nil, nil)
	if got := lifecycleLedgerState(t, db, f.tenantID, f.executionID); got != "ASKING" {
		t.Fatalf("first execution ledger = %q, want ASKING", got)
	}

	var resumedExecutionID int64
	if err := migration.QueryRow(ctx, `INSERT INTO conversation_execution(tenant_id,turn_id,execution_no,request_id,status,is_mock)
		VALUES($1,$2,2,$3,'EXECUTING',true) RETURNING id`, f.tenantID, f.turnID, "exec-"+f.stamp+"-2").Scan(&resumedExecutionID); err != nil {
		t.Fatalf("resume execution fixture: %v", err)
	}
	if _, err := migration.Exec(ctx, `UPDATE turn SET status='EXECUTING',answer=NULL WHERE tenant_id=$1 AND id=$2`, f.tenantID, f.turnID); err != nil {
		t.Fatalf("resume turn fixture: %v", err)
	}
	finishTurn(ctx, db, lifecycleIdentity(f, resumedExecutionID, 2), "DONE", "", "the answer", true, []string{"doc-a"}, nil)

	var rows int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM turn_decision WHERE tenant_id=$1 AND turn_id=$2`,
		f.tenantID, strconv.FormatInt(f.turnID, 10)).Scan(&rows); err != nil {
		t.Fatalf("ledger row count: %v", err)
	}
	if rows != 2 {
		t.Fatalf("ledger rows = %d, want one row per execution (2)", rows)
	}
	if got := lifecycleLedgerState(t, db, f.tenantID, resumedExecutionID); got != "DONE" {
		t.Fatalf("resumed execution ledger = %q, want DONE (not ASKING)", got)
	}
	if got := lifecycleExecState(t, db, f.tenantID, resumedExecutionID); got != "DONE" {
		t.Fatalf("resumed execution status = %q, want DONE", got)
	}
	if got := lifecycleTurnState(t, db, f.tenantID, f.turnID); got != "DONE" {
		t.Fatalf("turn status = %q, want DONE", got)
	}
}

// Cancellation overrides the requested state; execution, turn and ledger must
// all say CANCELLED, never the caller's DONE.
func TestTurnDecisionCancelWritesCancelled(t *testing.T) {
	migration, db := lifecycleDBs(t)
	f := newLifecycleFixture(t, migration)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	finishTurn(ctx, db, lifecycleIdentity(f, f.executionID, 1), "DONE", "", "must not persist", true, nil, nil)

	if got := lifecycleExecState(t, db, f.tenantID, f.executionID); got != "CANCELLED" {
		t.Fatalf("execution status = %q, want CANCELLED", got)
	}
	if got := lifecycleTurnState(t, db, f.tenantID, f.turnID); got != "CANCELLED" {
		t.Fatalf("turn status = %q, want CANCELLED", got)
	}
	if got := lifecycleLedgerState(t, db, f.tenantID, f.executionID); got != "CANCELLED" {
		t.Fatalf("ledger state = %q, want CANCELLED", got)
	}
}

// Recovery closes interrupted executions and writes the same state to the
// ledger in that transaction.
func TestTurnDecisionRecoveryWritesLedger(t *testing.T) {
	migration, db := lifecycleDBs(t)
	f := newLifecycleFixture(t, migration)
	g := &gateway{db: db}
	recovered, err := g.recoverInterruptedExecutions(context.Background())
	if err != nil {
		t.Fatalf("recovery: %v", err)
	}
	if recovered < 1 {
		t.Fatalf("recovered = %d, want at least the fixture execution", recovered)
	}
	if got := lifecycleExecState(t, db, f.tenantID, f.executionID); got != "FAILED" {
		t.Fatalf("execution status = %q, want FAILED", got)
	}
	if got := lifecycleLedgerState(t, db, f.tenantID, f.executionID); got != "FAILED" {
		t.Fatalf("ledger state = %q, want FAILED", got)
	}
}

func TestPersistExecutionTerminalSetsCopyability(t *testing.T) {
	ctx := context.Background()
	migration, gatewayDB := lifecycleDBs(t)
	persist := func(t *testing.T, f lifecycleFixture, state string, addCitation, mock bool) (bool, string) {
		t.Helper()
		tx, err := gatewayDB.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if addCitation {
			if _, err = tx.Exec(ctx, `INSERT INTO evidence_snapshot(tenant_id,execution_id,evidence_id,content,disclosure_class,customer_eligible,citation_index)
				VALUES($1,$2,$3,'copyable body','external_allowed',true,1)`, f.tenantID, f.executionID, "ev-"+f.stamp); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO citation(tenant_id,execution_id,evidence_id,citation_index)
				VALUES($1,$2,$3,1)`, f.tenantID, f.executionID, "ev-"+f.stamp); err != nil {
				t.Fatal(err)
			}
		}
		if err = persistExecutionTerminal(ctx, tx, executionIdentity{tenantID: f.tenantID, executionID: f.executionID,
			turnID: f.turnID, conversationID: f.conversationID, executionNo: 1, requestID: "copy-" + f.stamp, mock: mock}, state, "", "suggestion body"); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		var canCopy bool
		var blocked string
		err = gatewayDB.QueryRow(ctx, `SELECT can_copy,blocked_reason FROM customer_reply WHERE tenant_id=$1 AND execution_id=$2`,
			f.tenantID, f.executionID).Scan(&canCopy, &blocked)
		if err != nil {
			t.Fatal(err)
		}
		var eventCopy bool
		if err = gatewayDB.QueryRow(ctx, `SELECT (data_json->>'can_copy')::boolean FROM sse_event
			WHERE tenant_id=$1 AND execution_id=$2 AND event_type='completed'
			ORDER BY sequence DESC LIMIT 1`, f.tenantID, f.executionID).Scan(&eventCopy); err != nil {
			t.Fatal(err)
		}
		if eventCopy != canCopy {
			t.Fatalf("completed can_copy=%v, customer_reply can_copy=%v", eventCopy, canCopy)
		}
		return canCopy, blocked
	}

	t.Run("done with citation", func(t *testing.T) {
		f := newLifecycleFixture(t, migration)
		if _, err := migration.Exec(ctx, `UPDATE conversation_execution SET is_mock=false WHERE tenant_id=$1 AND id=$2`, f.tenantID, f.executionID); err != nil {
			t.Fatal(err)
		}
		canCopy, blocked := persist(t, f, "DONE", true, false)
		if !canCopy || blocked != "" {
			t.Fatalf("can_copy=%v blocked=%q, want true/empty", canCopy, blocked)
		}
	})

	t.Run("done without citation", func(t *testing.T) {
		f := newLifecycleFixture(t, migration)
		if _, err := migration.Exec(ctx, `UPDATE conversation_execution SET is_mock=false WHERE tenant_id=$1 AND id=$2`, f.tenantID, f.executionID); err != nil {
			t.Fatal(err)
		}
		canCopy, blocked := persist(t, f, "DONE", false, false)
		if canCopy || blocked != "no_citable_evidence" {
			t.Fatalf("can_copy=%v blocked=%q, want false/no_citable_evidence", canCopy, blocked)
		}
	})

	t.Run("mock done", func(t *testing.T) {
		f := newLifecycleFixture(t, migration)
		canCopy, blocked := persist(t, f, "DONE", true, true)
		if canCopy || blocked != "mock" {
			t.Fatalf("can_copy=%v blocked=%q, want false/mock", canCopy, blocked)
		}
	})

	t.Run("asking is never copyable", func(t *testing.T) {
		f := newLifecycleFixture(t, migration)
		if _, err := migration.Exec(ctx, `UPDATE conversation_execution SET is_mock=false WHERE tenant_id=$1 AND id=$2`, f.tenantID, f.executionID); err != nil {
			t.Fatal(err)
		}
		tx, err := gatewayDB.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if err = persistExecutionTerminal(ctx, tx, executionIdentity{tenantID: f.tenantID, executionID: f.executionID,
			turnID: f.turnID, conversationID: f.conversationID, executionNo: 1, requestID: "copy-" + f.stamp}, "ASKING", "", "clarify"); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		var count int
		if err = gatewayDB.QueryRow(ctx, `SELECT count(*) FROM customer_reply WHERE tenant_id=$1 AND execution_id=$2 AND can_copy`,
			f.tenantID, f.executionID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("ASKING wrote a copyable reply row")
		}
		var eventCopy bool
		if err = gatewayDB.QueryRow(ctx, `SELECT (data_json->>'can_copy')::boolean FROM sse_event
			WHERE tenant_id=$1 AND execution_id=$2 AND event_type='completed'
			ORDER BY sequence DESC LIMIT 1`, f.tenantID, f.executionID).Scan(&eventCopy); err != nil {
			t.Fatal(err)
		}
		if eventCopy {
			t.Fatal("ASKING completed frame is copyable")
		}
	})
}
