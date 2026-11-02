package gateway

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ensureSuggestionConversation returns the one hidden suggestion conversation
// for this staff member and shop. The advisory lock makes concurrent first
// suggestions converge on one row without a new unique index.
func ensureSuggestionConversation(ctx context.Context, tx pgx.Tx, tenantID, userID int64, shop string) (int64, error) {
	lockKey := fmt.Sprintf("suggestion-conversation:%d:%d:%s", tenantID, userID, shop)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1)::bigint)`, lockKey); err != nil {
		return 0, err
	}
	var id int64
	err := tx.QueryRow(ctx, `SELECT id FROM conversation
		WHERE tenant_id=$1 AND user_id=$2 AND shop_id=$3 AND kind='suggestion'
		ORDER BY id LIMIT 1`, tenantID, userID, shop).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO conversation(tenant_id,user_id,shop_id,title,kind)
		VALUES($1,$2,$3,'建议回复','suggestion') RETURNING id`, tenantID, userID, shop).Scan(&id)
	return id, err
}

// lookupSuggestionIdempotency mirrors createTurn's key lookup inside the hidden
// suggestion conversation. A matching key reuses the original request.
func lookupSuggestionIdempotency(ctx context.Context, tx pgx.Tx, tenantID, conversationID int64,
	key string) (payloadHash, requestID, status string, turnID int64, executionNo int, found bool, err error) {
	err = tx.QueryRow(ctx, `SELECT i.payload_hash,e.request_id,e.status,t.id,e.execution_no
		FROM turn_idempotency i
		JOIN turn t ON t.tenant_id=i.tenant_id AND t.id=i.turn_id
		JOIN conversation_execution e ON e.tenant_id=t.tenant_id AND e.turn_id=t.id
			AND ((i.execution_id IS NOT NULL AND e.id=i.execution_id)
				OR (i.execution_id IS NULL AND e.execution_no=1))
		WHERE i.tenant_id=$1 AND i.conversation_id=$2 AND i.idempotency_key=$3`,
		tenantID, conversationID, key).Scan(&payloadHash, &requestID, &status, &turnID, &executionNo)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", 0, 0, false, nil
	}
	if err != nil {
		return "", "", "", 0, 0, false, err
	}
	return payloadHash, requestID, status, turnID, executionNo, true, nil
}

func suggestionActive(ctx context.Context, tx pgx.Tx, tenantID, conversationID int64) (bool, error) {
	var active bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM turn WHERE tenant_id=$1
		AND conversation_id=$2 AND status IN ('PENDING','EXECUTING','CANCEL_REQUESTED'))`,
		tenantID, conversationID).Scan(&active)
	return active, err
}

const suggestionTurnRetention = 50

// pruneSuggestionTurns keeps only the newest suggestionTurnRetention turns in
// one hidden suggestion conversation. The schema has no ON DELETE CASCADE on
// these tables, so every dependent row is removed explicitly in FK order.
func pruneSuggestionTurns(ctx context.Context, tx pgx.Tx, tenantID, conversationID int64) error {
	rows, err := tx.Query(ctx, `SELECT id,request_id FROM turn
		WHERE tenant_id=$1 AND conversation_id=$2 AND kind='suggestion'
		ORDER BY turn_no DESC OFFSET $3`, tenantID, conversationID, suggestionTurnRetention)
	if err != nil {
		return err
	}
	turnIDs := []int64{}
	requestIDs := []string{}
	turnTextIDs := []string{}
	for rows.Next() {
		var id int64
		var requestID string
		if err = rows.Scan(&id, &requestID); err != nil {
			rows.Close()
			return err
		}
		turnIDs = append(turnIDs, id)
		requestIDs = append(requestIDs, requestID)
		turnTextIDs = append(turnTextIDs, fmt.Sprintf("%d", id))
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(turnIDs) == 0 {
		return err
	}

	rows, err = tx.Query(ctx, `SELECT id FROM conversation_execution
		WHERE tenant_id=$1 AND turn_id=ANY($2::bigint[])`, tenantID, turnIDs)
	if err != nil {
		return err
	}
	executionIDs := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		executionIDs = append(executionIDs, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}

	if err = deleteTurnDecisions(ctx, tx, tenantID, executionIDs, turnTextIDs); err != nil {
		return err
	}
	statements := []struct {
		query string
		args  []any
	}{
		{`DELETE FROM citation WHERE tenant_id=$1 AND execution_id=ANY($2::bigint[])`, []any{tenantID, executionIDs}},
		{`DELETE FROM evidence_snapshot WHERE tenant_id=$1 AND execution_id=ANY($2::bigint[])`, []any{tenantID, executionIDs}},
		{`DELETE FROM customer_reply WHERE tenant_id=$1 AND execution_id=ANY($2::bigint[])`, []any{tenantID, executionIDs}},
		{`DELETE FROM sse_event WHERE tenant_id=$1 AND execution_id=ANY($2::bigint[])`, []any{tenantID, executionIDs}},
		{`DELETE FROM retrieval_trace WHERE tenant_id=$1 AND request_id=ANY($2::text[])`, []any{tenantID, requestIDs}},
		{`DELETE FROM routing_trace WHERE tenant_id=$1 AND request_id=ANY($2::text[])`, []any{tenantID, requestIDs}},
		{`DELETE FROM tool_call_trace WHERE tenant_id=$1 AND request_id=ANY($2::text[])`, []any{tenantID, requestIDs}},
		{`DELETE FROM feedback WHERE tenant_id=$1 AND turn_id=ANY($2::bigint[])`, []any{tenantID, turnIDs}},
		{`DELETE FROM message WHERE tenant_id=$1 AND turn_id=ANY($2::bigint[])`, []any{tenantID, turnIDs}},
		{`DELETE FROM turn_idempotency WHERE tenant_id=$1 AND turn_id=ANY($2::bigint[])`, []any{tenantID, turnIDs}},
		{`DELETE FROM conversation_execution WHERE tenant_id=$1 AND turn_id=ANY($2::bigint[])`, []any{tenantID, turnIDs}},
		{`DELETE FROM turn WHERE tenant_id=$1 AND id=ANY($2::bigint[])`, []any{tenantID, turnIDs}},
	}
	for _, statement := range statements {
		if _, err = tx.Exec(ctx, statement.query, statement.args...); err != nil {
			return err
		}
	}
	return nil
}

// insertSuggestionTurn creates the suggestion turn/execution and its
// idempotency row. It is deliberately separate from createTurn so a normal
// user turn can never be marked as a hidden suggestion by a caller mistake.
func insertSuggestionTurn(ctx context.Context, tx pgx.Tx, tenantID, conversationID int64,
	query, requestID, key, payloadHash, permissionRevision string, deadline time.Time, mock bool) (turnID, executionID int64, err error) {
	var turnNo int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(turn_no),0)+1 FROM turn
		WHERE tenant_id=$1 AND conversation_id=$2`, tenantID, conversationID).Scan(&turnNo); err != nil {
		return 0, 0, err
	}
	if err = tx.QueryRow(ctx, `INSERT INTO turn(tenant_id,conversation_id,turn_no,request_id,user_query,status,kind)
		VALUES($1,$2,$3,$4,$5,'EXECUTING','suggestion') RETURNING id`,
		tenantID, conversationID, turnNo, requestID, query).Scan(&turnID); err != nil {
		return 0, 0, err
	}
	if err = tx.QueryRow(ctx, `INSERT INTO conversation_execution(tenant_id,turn_id,execution_no,request_id,status,is_mock,deadline_at,permission_revision)
		VALUES($1,$2,1,$3,'EXECUTING',$4,$5,$6) RETURNING id`,
		tenantID, turnID, requestID, mock, deadline, permissionRevision).Scan(&executionID); err != nil {
		return 0, 0, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO turn_idempotency(tenant_id,conversation_id,idempotency_key,payload_hash,turn_id,execution_id)
		VALUES($1,$2,$3,$4,$5,$6)`, tenantID, conversationID, key, payloadHash, turnID, executionID); err != nil {
		return 0, 0, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO message(tenant_id,turn_id,role,content) VALUES($1,$2,'user',$3)`,
		tenantID, turnID, query); err != nil {
		return 0, 0, err
	}
	if err = pruneSuggestionTurns(ctx, tx, tenantID, conversationID); err != nil {
		return 0, 0, err
	}
	return turnID, executionID, nil
}

// loadSuggestionForCopy locks the suggestion execution before copy audit so two
// concurrent copy requests cannot both count as the first success.
func loadSuggestionForCopy(ctx context.Context, tx pgx.Tx, tenantID, userID int64, shop, requestID string) (turnKind, conversationKind, status string, found bool, err error) {
	err = tx.QueryRow(ctx, `SELECT t.kind,c.kind,e.status
		FROM conversation_execution e JOIN turn t ON t.tenant_id=e.tenant_id AND t.id=e.turn_id
		JOIN conversation c ON c.tenant_id=t.tenant_id AND c.id=t.conversation_id
		WHERE e.tenant_id=$1 AND e.request_id=$2 AND c.user_id=$3 AND c.shop_id=$4 FOR UPDATE OF e`,
		tenantID, requestID, userID, shop).Scan(&turnKind, &conversationKind, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", false, nil
	}
	if err != nil {
		return "", "", "", false, err
	}
	return turnKind, conversationKind, status, true, nil
}
