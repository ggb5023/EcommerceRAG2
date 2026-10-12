package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// A dedicated connection owns the execution process for this database. It must
// never be returned to a pool: closing it releases the session advisory lock.
func (g *gateway) acquireExecutionOwner(ctx context.Context) (*pgx.Conn, error) {
	conn, err := pgx.ConnectConfig(ctx, g.db.Config().ConnConfig.Copy())
	if err != nil {
		return nil, errors.New("execution ownership connection unavailable")
	}
	var owned bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(742019,1)`).Scan(&owned); err != nil || !owned {
		_ = conn.Close(ctx)
		return nil, errors.New("another Gateway owns this database or ownership is unavailable")
	}
	return conn, nil
}

type executionIdentity struct {
	tenantID, executionID, turnID, conversationID, executionNo int64
	requestID                                                  string
	mock                                                       bool
}

// The caller locks the execution row. Result, turn and terminal events become
// visible together. Sequences come from durable events, including after a crash.
func persistExecutionTerminal(ctx context.Context, tx pgx.Tx, id executionIdentity, state, code, answer string) error {
	sum := sha256.Sum256([]byte(answer))
	_, err := tx.Exec(ctx, `UPDATE conversation_execution SET status=$3,finish_reason=$3,error_code=NULLIF($4,''),answer=$5,answer_hash=$6,finished_at=now()
		WHERE tenant_id=$1 AND id=$2`, id.tenantID, id.executionID, state, code, answer, hex.EncodeToString(sum[:]))
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE turn SET status=$3,answer=$4 WHERE tenant_id=$1 AND id=$2
		AND $5=(SELECT e.id FROM conversation_execution e WHERE e.tenant_id=$1 AND e.turn_id=$2 ORDER BY execution_no DESC LIMIT 1)`,
		id.tenantID, id.turnID, state, answer, id.executionID)
	if err != nil {
		return err
	}
	if state == "DONE" {
		if _, err = tx.Exec(ctx, `INSERT INTO message(tenant_id,turn_id,role,content) VALUES($1,$2,'assistant',$3)`, id.tenantID, id.turnID, answer); err != nil {
			return err
		}
		blocked := "provider_not_approved"
		if id.mock {
			blocked = "mock"
		}
		_, err = tx.Exec(ctx, `INSERT INTO customer_reply(tenant_id,execution_id,text_plain,is_mock,can_copy,blocked_reason)
			VALUES($1,$2,$3,$4,false,$5) ON CONFLICT(tenant_id,execution_id) DO UPDATE SET text_plain=EXCLUDED.text_plain,is_mock=EXCLUDED.is_mock,can_copy=false,blocked_reason=EXCLUDED.blocked_reason`,
			id.tenantID, id.executionID, answer, id.mock, blocked)
	} else {
		_, err = tx.Exec(ctx, `UPDATE customer_reply SET text_plain='',can_copy=false,blocked_reason='incomplete' WHERE tenant_id=$1 AND execution_id=$2`, id.tenantID, id.executionID)
	}
	if err != nil {
		return err
	}
	var seq int64
	if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0) FROM sse_event WHERE tenant_id=$1 AND execution_id=$2`, id.tenantID, id.executionID).Scan(&seq); err != nil {
		return err
	}
	appendEvent := func(kind string, object map[string]any) error {
		seq++
		object["seq"], object["request_id"] = seq, id.requestID
		object["turn_id"], object["conversation_id"] = strconv.FormatInt(id.turnID, 10), strconv.FormatInt(id.conversationID, 10)
		object["execution_no"] = id.executionNo
		body, err := json.Marshal(object)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO sse_event(tenant_id,execution_id,sequence,event_type,data_json) VALUES($1,$2,$3,$4,$5::jsonb)`, id.tenantID, id.executionID, seq, kind, string(body))
		return err
	}
	if state != "DONE" && state != "ASKING" {
		if err = appendEvent("error", map[string]any{"type": "error", "code": code, "message": code}); err != nil {
			return err
		}
	}
	return appendEvent("completed", map[string]any{"type": "completed", "status": state, "clarification": answer, "error_code": code, "is_mock": id.mock, "can_copy": false})
}

// Only run with exclusive database ownership, before accepting HTTP requests.
// No model call is resumed or retried; terminal and ASKING records are untouched.
func (g *gateway) recoverInterruptedExecutions(ctx context.Context) (int, error) {
	tx, err := g.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT e.tenant_id,e.id,e.turn_id,t.conversation_id,e.execution_no,e.request_id,e.is_mock,e.status,e.cancel_requested
		FROM conversation_execution e JOIN turn t ON t.tenant_id=e.tenant_id AND t.id=e.turn_id
		WHERE e.status IN ('PENDING','EXECUTING','CANCEL_REQUESTED') ORDER BY e.id FOR UPDATE OF e`)
	if err != nil {
		return 0, err
	}
	type interrupted struct {
		id     executionIdentity
		state  string
		cancel bool
	}
	var pending []interrupted
	for rows.Next() {
		var item interrupted
		err = rows.Scan(&item.id.tenantID, &item.id.executionID, &item.id.turnID, &item.id.conversationID, &item.id.executionNo, &item.id.requestID, &item.id.mock, &item.state, &item.cancel)
		if err != nil {
			rows.Close()
			return 0, err
		}
		pending = append(pending, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, item := range pending {
		state, code := "FAILED", "gateway_restarted"
		if item.state == "CANCEL_REQUESTED" || item.cancel {
			state, code = "CANCELLED", "cancelled"
		}
		if err = persistExecutionTerminal(ctx, tx, item.id, state, code, ""); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(pending), nil
}

func executionOwnerAlive(conn *pgx.Conn) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return conn.Ping(ctx) == nil
}
