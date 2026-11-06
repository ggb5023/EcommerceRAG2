package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/domain"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
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
	behavior                                                   string
	mock                                                       bool
}

// The caller locks the execution row. Result, turn and terminal events become
// visible together. Sequences come from durable events, including after a crash.
func persistExecutionTerminal(ctx context.Context, tx pgx.Tx, id executionIdentity, state, code, answer string, facts []*ragv1.Fact) error {
	sum := sha256.Sum256([]byte(answer))
	var factsJSON []byte
	if facts != nil {
		encoded, err := json.Marshal(facts)
		if err != nil {
			return err
		}
		factsJSON = encoded
	}
	if err := updateExecutionTerminal(ctx, tx, id, state, code, answer, hex.EncodeToString(sum[:]), factsJSON); err != nil {
		return err
	}
	if err := updateTurnTerminal(ctx, tx, id, state, answer); err != nil {
		return err
	}
	citationCount, factCount := 0, 0
	if state == "DONE" {
		if err := insertAssistantMessage(ctx, tx, id.tenantID, id.turnID, answer); err != nil {
			return err
		}
		citations, err := countExecutionCitations(ctx, tx, id.tenantID, id.executionID)
		if err != nil {
			return err
		}
		facts, err := countExecutionFacts(ctx, tx, id.tenantID, id.executionID)
		if err != nil {
			return err
		}
		citationCount, factCount = citations, facts
	}
	canCopy, blocked := domain.CanCopyReply(state, id.behavior, citationCount, factCount, id.mock)
	if state == "DONE" {
		if err := upsertCustomerReply(ctx, tx, id.tenantID, id.executionID, answer, id.mock, canCopy, blocked); err != nil {
			return err
		}
	} else if err := clearCustomerReply(ctx, tx, id.tenantID, id.executionID); err != nil {
		return err
	}
	seq, err := nextSSESequence(ctx, tx, id.tenantID, id.executionID)
	if err != nil {
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
		return insertSSEEvent(ctx, tx, id.tenantID, id.executionID, seq, kind, body)
	}
	if state != "DONE" && state != "ASKING" {
		if err = appendEvent("error", map[string]any{"type": "error", "code": code, "message": code}); err != nil {
			return err
		}
	}
	return appendEvent("completed", map[string]any{"type": "completed", "status": state, "clarification": answer, "error_code": code, "is_mock": id.mock, "can_copy": canCopy, "blocked_reason": blocked})
}

// Only run with exclusive database ownership, before accepting HTTP requests.
// No model call is resumed or retried; terminal and ASKING records are untouched.
func (g *gateway) recoverInterruptedExecutions(ctx context.Context) (int, error) {
	tx, err := g.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	pending, err := lockInterruptedExecutions(ctx, tx)
	if err != nil {
		return 0, err
	}
	for _, item := range pending {
		state, code := "FAILED", "gateway_restarted"
		if item.state == "CANCEL_REQUESTED" || item.cancel {
			state, code = "CANCELLED", "cancelled"
		}
		if err = persistExecutionTerminal(ctx, tx, item.id, state, code, "", nil); err != nil {
			return 0, err
		}
		ident := turnEventIdentity{tenantID: item.id.tenantID, executionID: item.id.executionID,
			turnID: item.id.turnID, conversationID: item.id.conversationID,
			executionNo: item.id.executionNo, requestID: item.id.requestID}
		if err = appendTurnDecision(ctx, tx, ident, state, code, nil, nil); err != nil {
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
