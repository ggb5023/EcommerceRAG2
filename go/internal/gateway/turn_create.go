package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type turnCreateStore struct {
	db *pgxpool.Pool
}

type turnCreateSessionData struct {
	TenantID           int64
	UserID             int64
	Shop               string
	PermissionRevision string
	Mock               bool
}

type turnCreateInput struct {
	ConversationID int64
	Text           string
	Query          string
	ParentTurnID   string
	IdempotencyKey string
}

type turnCreateResult struct {
	Existing        bool
	RequestID       string
	Status          string
	ConversationID  int64
	TurnID          int64
	ExecutionID     int64
	ExecutionNo     int
	Deadline        time.Time
	ParentTurnID    int64
	ParentExecution int64
	Query           string
	Mock            bool
}

type turnCreateFailure struct {
	status  int
	code    string
	message string
}

func (e *turnCreateFailure) Error() string { return e.code }

func newTurnCreateFailure(status int, code, message string) error {
	return &turnCreateFailure{status: status, code: code, message: message}
}

func prepareTurnCreate(input turnCreateInput) (query, key, payloadHash string, err error) {
	query = strings.TrimSpace(input.Text)
	if query == "" {
		query = strings.TrimSpace(input.Query)
	}
	if query == "" {
		return "", "", "", newTurnCreateFailure(http.StatusBadRequest, "invalid_query", "query is required")
	}
	key = strings.TrimSpace(input.IdempotencyKey)
	if key == "" {
		return "", "", "", newTurnCreateFailure(http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key is required")
	}
	canonical, _ := json.Marshal(map[string]string{"query": query, "parent_turn_id": strings.TrimSpace(input.ParentTurnID)})
	hash := sha256.Sum256(canonical)
	return query, key, hex.EncodeToString(hash[:]), nil
}

func resolveParentTurn(ctx context.Context, tx pgx.Tx, tenantID, conversationID int64, raw string) (*int64, int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, 0, nil
	}
	parentID, parseErr := strconv.ParseInt(raw, 10, 64)
	if parseErr != nil {
		var lookupErr error
		parentID, lookupErr = parentTurnIDByRequestID(ctx, tx, tenantID, conversationID, raw)
		if errors.Is(lookupErr, pgx.ErrNoRows) {
			return nil, 0, newTurnCreateFailure(http.StatusBadRequest, "invalid_parent_turn", "parent turn is invalid")
		}
		// Preserve createTurn's historical behavior: a non-row lookup error
		// falls through to the status query with the zero id and returns the
		// parent_turn_not_found branch instead of a database error.
	}
	status, err := parentTurnStatus(ctx, tx, tenantID, conversationID, parentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, newTurnCreateFailure(http.StatusNotFound, "parent_turn_not_found", "parent turn not found")
	}
	if err != nil {
		return nil, 0, newTurnCreateFailure(http.StatusServiceUnavailable, "database_unavailable", "could not load parent turn")
	}
	if status != "ASKING" {
		return nil, 0, newTurnCreateFailure(http.StatusConflict, "parent_turn_not_asking", "parent turn is not awaiting clarification")
	}
	executionID, err := parentTurnExecutionID(ctx, tx, tenantID, parentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, newTurnCreateFailure(http.StatusConflict, "parent_checkpoint_missing", "parent turn has no execution checkpoint")
	}
	if err != nil {
		return nil, 0, newTurnCreateFailure(http.StatusServiceUnavailable, "database_unavailable", "could not load parent execution")
	}
	return &parentID, executionID, nil
}

type turnIdempotencyDecision uint8

const (
	turnIdempotencyNew turnIdempotencyDecision = iota
	turnIdempotencyReplay
	turnIdempotencyConflict
)

// classifyTurnIdempotency keeps the replay/conflict decision independent of the
// database lookup so it can be tested at the default layer.
func classifyTurnIdempotency(found bool, storedHash, payloadHash string) turnIdempotencyDecision {
	if !found {
		return turnIdempotencyNew
	}
	if storedHash != payloadHash {
		return turnIdempotencyConflict
	}
	return turnIdempotencyReplay
}

func findIdempotentTurn(ctx context.Context, tx pgx.Tx, tenantID, conversationID int64, key, payloadHash string) (*turnCreateResult, error) {
	record, found, err := lookupTurnIdempotency(ctx, tx, tenantID, conversationID, key)
	if err != nil {
		return nil, newTurnCreateFailure(http.StatusServiceUnavailable, "database_unavailable", "could not check idempotency")
	}
	switch classifyTurnIdempotency(found, record.payloadHash, payloadHash) {
	case turnIdempotencyNew:
		return nil, nil
	case turnIdempotencyConflict:
		return nil, newTurnCreateFailure(http.StatusConflict, "idempotency_conflict", "idempotency key was used with a different query")
	}
	return &turnCreateResult{
		Existing:       true,
		RequestID:      record.requestID,
		Status:         record.status,
		ConversationID: conversationID,
		TurnID:         record.turnID,
		ExecutionNo:    record.executionNo,
	}, nil
}

// startTurn owns the create-turn transaction and orchestration. It receives
// only data, begins and commits the transaction, and never touches an HTTP
// request or gateway field (decision 0025 R3).
func startTurn(ctx context.Context, store turnCreateStore, session turnCreateSessionData, input turnCreateInput) (turnCreateResult, error) {
	query, key, payloadHash, err := prepareTurnCreate(input)
	if err != nil {
		return turnCreateResult{}, err
	}
	tx, err := store.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return turnCreateResult{}, newTurnCreateFailure(http.StatusServiceUnavailable, "database_unavailable", "could not start turn")
	}
	defer tx.Rollback(ctx)

	shopID, err := lockConversationShop(ctx, tx, session.TenantID, session.UserID, input.ConversationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return turnCreateResult{}, newTurnCreateFailure(http.StatusNotFound, "not_found", "conversation not found")
	}
	if err != nil {
		return turnCreateResult{}, newTurnCreateFailure(http.StatusServiceUnavailable, "database_unavailable", "could not lock conversation")
	}
	if shopID != session.Shop {
		return turnCreateResult{}, newTurnCreateFailure(http.StatusForbidden, "forbidden", "shop is outside the current session")
	}
	stale, err := conversationHistoryStale(ctx, store.db, session.TenantID, input.ConversationID, session.PermissionRevision)
	if err != nil {
		return turnCreateResult{}, newTurnCreateFailure(http.StatusServiceUnavailable, "authorization_unavailable", "authorization failed")
	}
	if stale {
		return turnCreateResult{}, newTurnCreateFailure(http.StatusForbidden, "execution_permission_changed", "historical execution scope is no longer valid")
	}
	parentTurnID, parentExecutionID, err := resolveParentTurn(ctx, tx, session.TenantID, input.ConversationID, input.ParentTurnID)
	if err != nil {
		return turnCreateResult{}, err
	}
	existing, err := findIdempotentTurn(ctx, tx, session.TenantID, input.ConversationID, key, payloadHash)
	if err != nil {
		return turnCreateResult{}, err
	}
	if existing != nil {
		existing.Mock = session.Mock
		return *existing, nil
	}
	active, err := activeTurnExists(ctx, tx, session.TenantID, input.ConversationID)
	if err != nil {
		return turnCreateResult{}, newTurnCreateFailure(http.StatusServiceUnavailable, "database_unavailable", "could not check active turn")
	}
	if active {
		return turnCreateResult{}, newTurnCreateFailure(http.StatusConflict, "turn_in_progress", "conversation already has an active turn")
	}
	deadline := time.Now().Add(turnTimeout)
	requestID := newRequestID()
	turnID, executionID, err := insertTurnAggregate(ctx, tx, session.TenantID, input.ConversationID,
		requestID, query, key, payloadHash, session.PermissionRevision, deadline, session.Mock, parentTurnID)
	if err != nil {
		return turnCreateResult{}, newTurnCreateFailure(http.StatusServiceUnavailable, "database_unavailable", "could not persist turn")
	}
	if err := tx.Commit(ctx); err != nil {
		return turnCreateResult{}, newTurnCreateFailure(http.StatusServiceUnavailable, "database_unavailable", "could not commit turn")
	}
	_ = updateConversationTitle(ctx, store.db, session.TenantID, input.ConversationID, first24(query))
	result := turnCreateResult{
		RequestID:      requestID,
		Status:         "EXECUTING",
		ConversationID: input.ConversationID,
		TurnID:         turnID,
		ExecutionID:    executionID,
		ExecutionNo:    1,
		Deadline:       deadline,
		Query:          query,
		Mock:           session.Mock,
	}
	if parentTurnID != nil {
		result.ParentTurnID = *parentTurnID
		result.ParentExecution = parentExecutionID
	}
	return result, nil
}
