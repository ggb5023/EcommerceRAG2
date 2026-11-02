package admin

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"
)

// This reconciles the Go classifier with the hand-written SQL in the 03
// workpackage. It inserts one turn per rule, runs both sides against the same
// rows, and cleans the fixture rows afterwards.
func TestResolutionSummarySQLReconciliation(t *testing.T) {
	_, migration, gateway, _, handler := integrationHandlerWithIngest(t, false)
	ctx := context.Background()
	tenant := handler.Session.TenantID
	var userID int64
	if err := migration.QueryRow(ctx, `SELECT id FROM app_user WHERE tenant_id=$1 ORDER BY id LIMIT 1`, tenant).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	var conversationID int64
	if err := migration.QueryRow(ctx, `INSERT INTO conversation(tenant_id,user_id,title) VALUES($1,$2,'outcomes-reconciliation') RETURNING id`,
		tenant, userID).Scan(&conversationID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, statement := range []string{
			`DELETE FROM turn_decision WHERE tenant_id=$1`,
			`DELETE FROM conversation_execution WHERE tenant_id=$1`,
			`DELETE FROM turn WHERE tenant_id=$1`,
			`DELETE FROM conversation WHERE tenant_id=$1`,
		} {
			if _, err := migration.Exec(context.Background(), statement, tenant); err != nil {
				t.Logf("reconciliation cleanup skipped: %v", err)
			}
		}
	})
	now := time.Now()
	from := now.Add(-time.Hour)
	stamp := now.UnixNano()

	type turnSpec struct {
		minuteAgo int
		status    string
	}
	turnNo := 0
	insertTurn := func(spec turnSpec, parentID *int64) int64 {
		t.Helper()
		turnNo++
		var turnID int64
		if err := migration.QueryRow(ctx, `INSERT INTO turn(tenant_id,conversation_id,turn_no,request_id,user_query,status,parent_turn_id,created_at)
			VALUES($1,$2,$3,$4,'reconciliation','EXECUTING',$5,$6) RETURNING id`,
			tenant, conversationID, turnNo, fmt.Sprintf("recon-turn-%d-%d", stamp, turnNo),
			parentID, now.Add(-time.Duration(spec.minuteAgo)*time.Minute)).Scan(&turnID); err != nil {
			t.Fatal(err)
		}
		return turnID
	}
	insertExecution := func(turnID int64, executionNo int, state string, finished time.Time, withDecision bool) {
		t.Helper()
		var executionID int64
		if err := migration.QueryRow(ctx, `INSERT INTO conversation_execution(tenant_id,turn_id,execution_no,request_id,status,is_mock,finished_at)
			VALUES($1,$2,$3,$4,$5,true,$6) RETURNING id`,
			tenant, turnID, executionNo, fmt.Sprintf("recon-exec-%d-%d-%d", stamp, turnID, executionNo), state, finished).Scan(&executionID); err != nil {
			t.Fatal(err)
		}
		if !withDecision {
			return
		}
		if _, err := migration.Exec(ctx, `INSERT INTO turn_decision(tenant_id,execution_id,conversation_id,turn_id,state,chosen,rejected)
			VALUES($1,$2,$3,$4,$5,'[]','[]')`,
			tenant, executionID, strconv.FormatInt(conversationID, 10), strconv.FormatInt(turnID, 10), state); err != nil {
			t.Fatal(err)
		}
	}

	// parent ASKING ← child DONE by parent_turn_id: clarified.
	parentID := insertTurn(turnSpec{20, "ASKING"}, nil)
	childID := insertTurn(turnSpec{15, "DONE"}, &parentID)
	insertExecution(parentID, 1, "ASKING", now.Add(-20*time.Minute), true)
	insertExecution(childID, 1, "DONE", now.Add(-15*time.Minute), true)

	// Same conversation, DONE without a parent link: the ASKING stays abandoned.
	unrelatedParent := insertTurn(turnSpec{20, "ASKING"}, nil)
	unrelatedDone := insertTurn(turnSpec{15, "DONE"}, nil)
	insertExecution(unrelatedParent, 1, "ASKING", now.Add(-20*time.Minute), true)
	insertExecution(unrelatedDone, 1, "DONE", now.Add(-15*time.Minute), true)

	// ASKING inside the ten-minute grace: pending.
	pendingID := insertTurn(turnSpec{3, "ASKING"}, nil)
	insertExecution(pendingID, 1, "ASKING", now.Add(-3*time.Minute), true)

	// FAILED and CANCELLED are separate buckets.
	failedID := insertTurn(turnSpec{10, "FAILED"}, nil)
	insertExecution(failedID, 1, "FAILED", now.Add(-10*time.Minute), true)
	cancelledID := insertTurn(turnSpec{10, "CANCELLED"}, nil)
	insertExecution(cancelledID, 1, "CANCELLED", now.Add(-10*time.Minute), true)

	// A turn with an execution but no ledger row: unknown.
	unknownID := insertTurn(turnSpec{10, "EXECUTING"}, nil)
	insertExecution(unknownID, 1, "EXECUTING", now.Add(-10*time.Minute), false)

	// /resume: one turn, ASKING then DONE execution. The latest state is DONE,
	// so the turn is solved and does not need a parent link.
	resumedID := insertTurn(turnSpec{10, "DONE"}, nil)
	insertExecution(resumedID, 1, "ASKING", now.Add(-10*time.Minute), true)
	insertExecution(resumedID, 2, "DONE", now.Add(-8*time.Minute), true)

	const handSQL = `WITH latest AS (
		SELECT t.id, t.parent_turn_id, t.created_at, d.state, COALESCE(e.finished_at, t.created_at) AS clock
		FROM turn t
		LEFT JOIN LATERAL (
			SELECT e.id, e.finished_at FROM conversation_execution e
			WHERE e.tenant_id=t.tenant_id AND e.turn_id=t.id
			ORDER BY e.execution_no DESC LIMIT 1
		) e ON true
		LEFT JOIN turn_decision d ON d.tenant_id=t.tenant_id AND d.execution_id=e.id
		WHERE t.tenant_id=$1 AND t.created_at >= $2
	), classified AS (
		SELECT latest.*, EXISTS (
			SELECT 1 FROM latest child
			WHERE child.parent_turn_id=latest.id AND child.created_at > latest.created_at AND child.state='DONE'
		) AS clarified
		FROM latest
	)
	SELECT count(*),
		count(*) FILTER (WHERE state='DONE'),
		count(*) FILTER (WHERE state='ASKING' AND clarified),
		count(*) FILTER (WHERE state='ASKING' AND NOT clarified AND now()-clock >= interval '10 minutes'),
		count(*) FILTER (WHERE state='FAILED'),
		count(*) FILTER (WHERE state='CANCELLED'),
		count(*) FILTER (WHERE state='ASKING' AND NOT clarified AND now()-clock < interval '10 minutes'),
		count(*) FILTER (WHERE state IS NULL)
	FROM classified`
	var sqlTurns, sqlSolved, sqlClarified, sqlAbandoned, sqlFailed, sqlCancelled, sqlPending, sqlUnknown int64
	if err := migration.QueryRow(ctx, handSQL, tenant, from).Scan(&sqlTurns, &sqlSolved, &sqlClarified,
		&sqlAbandoned, &sqlFailed, &sqlCancelled, &sqlPending, &sqlUnknown); err != nil {
		t.Fatal(err)
	}
	summary, err := adminStore.ResolutionSummary(ctx, gateway, tenant, from)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("SQL: turns=%d solved=%d clarified=%d abandoned=%d failed=%d cancelled=%d pending=%d unknown=%d",
		sqlTurns, sqlSolved, sqlClarified, sqlAbandoned, sqlFailed, sqlCancelled, sqlPending, sqlUnknown)
	t.Logf("Go:  turns=%d solved=%d clarified=%d abandoned=%d failed=%d cancelled=%d pending=%d unknown=%d",
		summary.TurnsTotal, summary.Solved, summary.Clarified, summary.Abandoned, summary.Failed,
		summary.Cancelled, summary.Pending, summary.Unknown)
	if summary.TurnsTotal != sqlTurns || summary.Solved != sqlSolved || summary.Clarified != sqlClarified ||
		summary.Abandoned != sqlAbandoned || summary.Failed != sqlFailed || summary.Cancelled != sqlCancelled ||
		summary.Pending != sqlPending || summary.Unknown != sqlUnknown {
		t.Fatalf("Go and hand SQL disagree")
	}
	if summary.TurnsTotal != 9 || summary.Solved != 3 || summary.Clarified != 1 || summary.Abandoned != 1 ||
		summary.Failed != 1 || summary.Cancelled != 1 || summary.Pending != 1 || summary.Unknown != 1 {
		t.Fatalf("summary = %+v, want turns=9 solved=3 clarified=1 abandoned=1 failed=1 cancelled=1 pending=1 unknown=1", summary)
	}
}

func TestSuggestionTurnsExcludedFromOutcomesAndGaps(t *testing.T) {
	_, migration, gateway, _, handler := integrationHandlerWithIngest(t, false)
	ctx := context.Background()
	tenant := handler.Session.TenantID
	var userID int64
	if err := migration.QueryRow(ctx, `SELECT id FROM app_user WHERE tenant_id=$1 ORDER BY id LIMIT 1`, tenant).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	var conversationID int64
	if err := migration.QueryRow(ctx, `INSERT INTO conversation(tenant_id,user_id,title,kind) VALUES($1,$2,'suggestion-hidden','suggestion') RETURNING id`,
		tenant, userID).Scan(&conversationID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, statement := range []string{
			`DELETE FROM turn_decision WHERE tenant_id=$1`,
			`DELETE FROM conversation_execution WHERE tenant_id=$1`,
			`DELETE FROM turn WHERE tenant_id=$1`,
			`DELETE FROM conversation WHERE tenant_id=$1`,
		} {
			if _, err := migration.Exec(context.Background(), statement, tenant); err != nil {
				t.Logf("suggestion analytics cleanup skipped: %v", err)
			}
		}
	})
	now := time.Now()
	from := now.Add(-time.Hour)
	for index, state := range []string{"DONE", "FAILED"} {
		var turnID int64
		if err := migration.QueryRow(ctx, `INSERT INTO turn(tenant_id,conversation_id,turn_no,request_id,user_query,status,kind)
			VALUES($1,$2,$3,$4,'suggestion probe',$5,'suggestion') RETURNING id`,
			tenant, conversationID, index+1, fmt.Sprintf("suggestion-%d-%d", now.UnixNano(), index), state).Scan(&turnID); err != nil {
			t.Fatal(err)
		}
		var executionID int64
		if err := migration.QueryRow(ctx, `INSERT INTO conversation_execution(tenant_id,turn_id,execution_no,request_id,status,is_mock,finished_at)
			VALUES($1,$2,1,$3,$4,true,$5) RETURNING id`,
			tenant, turnID, fmt.Sprintf("suggestion-exec-%d-%d", now.UnixNano(), index), state, now.Add(-time.Minute)).Scan(&executionID); err != nil {
			t.Fatal(err)
		}
		if _, err := migration.Exec(ctx, `INSERT INTO turn_decision(tenant_id,execution_id,conversation_id,turn_id,state,chosen,rejected)
			VALUES($1,$2,$3,$4,$5,'[]','[]')`,
			tenant, executionID, strconv.FormatInt(conversationID, 10), strconv.FormatInt(turnID, 10), state); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := adminStore.ResolutionSummary(ctx, gateway, tenant, from)
	if err != nil {
		t.Fatal(err)
	}
	if summary.TurnsTotal != 0 || summary.ScoredTotal != 0 || summary.Solved != 0 || summary.Failed != 0 {
		t.Fatalf("suggestion turns entered outcomes: %+v", summary)
	}
	gaps, _, err := adminStore.KnowledgeGaps(ctx, gateway, tenant, from, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 0 {
		t.Fatalf("suggestion turns entered gaps: %+v", gaps)
	}
}
