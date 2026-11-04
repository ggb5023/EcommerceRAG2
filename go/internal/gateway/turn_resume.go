package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type turnResumeStore struct {
	db *pgxpool.Pool
}

type turnResumeSessionData struct {
	TenantID           int64
	UserID             int64
	Shop               string
	PermissionRevision string
	Mock               bool
}

type turnResumeInput struct {
	TurnID         int64
	Text           string
	Answer         string
	IdempotencyKey string
}

type turnResumeResult struct {
	Existing        bool
	RequestID       string
	Status          string
	ConversationID  int64
	TurnID          int64
	ExecutionID     int64
	ExecutionNo     int64
	Deadline        time.Time
	ParentTurnID    int64
	ParentExecution int64
	Query           string
	Mock            bool
}

type turnResumeFailure struct {
	status  int
	code    string
	message string
}

func (e *turnResumeFailure) Error() string { return e.code }

func newTurnResumeFailure(status int, code, message string) error {
	return &turnResumeFailure{status: status, code: code, message: message}
}

func prepareResumeTurn(input turnResumeInput) (query, payloadHash, storageKey string, err error) {
	query = strings.TrimSpace(input.Text)
	if query == "" {
		query = strings.TrimSpace(input.Answer)
	}
	if query == "" {
		return "", "", "", newTurnResumeFailure(http.StatusBadRequest, "invalid_query", "answer is required")
	}
	key := strings.TrimSpace(input.IdempotencyKey)
	if key == "" {
		return "", "", "", newTurnResumeFailure(http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key is required")
	}
	canonical, _ := json.Marshal(map[string]string{"answer": query})
	hash := sha256.Sum256(canonical)
	return query, hex.EncodeToString(hash[:]), "resume:" + key, nil
}

func findResumedExecution(ctx context.Context, tx pgx.Tx, tenantID, conversationID, turnID int64, storageKey, payloadHash string) (*turnResumeResult, error) {
	record, found, err := lookupResumeIdempotency(ctx, tx, tenantID, conversationID, storageKey)
	if err != nil {
		return nil, newTurnResumeFailure(http.StatusServiceUnavailable, "database_unavailable", "could not check resume idempotency")
	}
	if !found {
		return nil, nil
	}
	if record.payloadHash != payloadHash || record.turnID != turnID {
		return nil, newTurnResumeFailure(http.StatusConflict, "idempotency_conflict", "idempotency key was used with a different resume")
	}
	execution, err := loadResumedExecution(ctx, tx, tenantID, record.executionID)
	if err != nil {
		return nil, newTurnResumeFailure(http.StatusServiceUnavailable, "database_unavailable", "could not load resumed execution")
	}
	return &turnResumeResult{
		Existing:       true,
		RequestID:      execution.requestID,
		Status:         execution.status,
		ConversationID: conversationID,
		TurnID:         turnID,
		ExecutionID:    record.executionID,
		ExecutionNo:    execution.executionNo,
	}, nil
}

// resumeTurnExecution owns the resume transaction and orchestration. It takes
// only data, never an HTTP request or gateway field (decision 0025 R3).
func resumeTurnExecution(ctx context.Context, store turnResumeStore, session turnResumeSessionData, input turnResumeInput) (turnResumeResult, error) {
	query, payloadHash, storageKey, err := prepareResumeTurn(input)
	if err != nil {
		return turnResumeResult{}, err
	}
	tx, err := store.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return turnResumeResult{}, newTurnResumeFailure(http.StatusServiceUnavailable, "database_unavailable", "could not start resume")
	}
	defer tx.Rollback(ctx)

	conversationID, shopID, turnStatus, err := lockResumeTurn(ctx, tx, session.TenantID, input.TurnID, session.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return turnResumeResult{}, newTurnResumeFailure(http.StatusNotFound, "not_found", "turn not found")
	}
	if err != nil {
		return turnResumeResult{}, newTurnResumeFailure(http.StatusServiceUnavailable, "database_unavailable", "could not lock turn")
	}
	if shopID != session.Shop {
		return turnResumeResult{}, newTurnResumeFailure(http.StatusForbidden, "forbidden", "turn is outside the current session")
	}
	stale, err := conversationHistoryStale(ctx, store.db, session.TenantID, conversationID, session.PermissionRevision)
	if err != nil {
		return turnResumeResult{}, newTurnResumeFailure(http.StatusServiceUnavailable, "authorization_unavailable", "authorization failed")
	}
	if stale {
		return turnResumeResult{}, newTurnResumeFailure(http.StatusForbidden, "execution_permission_changed", "historical execution scope is no longer valid")
	}
	existing, err := findResumedExecution(ctx, tx, session.TenantID, conversationID, input.TurnID, storageKey, payloadHash)
	if err != nil {
		return turnResumeResult{}, err
	}
	if existing != nil {
		existing.Mock = session.Mock
		return *existing, nil
	}
	if turnStatus != "ASKING" {
		return turnResumeResult{}, newTurnResumeFailure(http.StatusConflict, "turn_not_asking", "turn is not awaiting clarification")
	}
	askingExecutionID, askingExecutionNo, err := loadAskingExecution(ctx, tx, session.TenantID, input.TurnID)
	if errors.Is(err, pgx.ErrNoRows) {
		return turnResumeResult{}, newTurnResumeFailure(http.StatusConflict, "parent_checkpoint_missing", "turn has no pending checkpoint execution")
	}
	if err != nil {
		return turnResumeResult{}, newTurnResumeFailure(http.StatusServiceUnavailable, "database_unavailable", "could not load checkpoint execution")
	}
	requestID := newRequestID()
	executionNo := askingExecutionNo + 1
	deadline := time.Now().Add(turnTimeout)
	executionID, err := persistResume(ctx, tx, session.TenantID, input.TurnID, conversationID, executionNo,
		requestID, session.PermissionRevision, deadline, session.Mock, storageKey, payloadHash, query)
	if err != nil {
		return turnResumeResult{}, newTurnResumeFailure(http.StatusServiceUnavailable, "database_unavailable", "could not persist resume")
	}
	if err := tx.Commit(ctx); err != nil {
		return turnResumeResult{}, newTurnResumeFailure(http.StatusServiceUnavailable, "database_unavailable", "could not commit resume")
	}
	return turnResumeResult{
		RequestID:       requestID,
		Status:          "EXECUTING",
		ConversationID:  conversationID,
		TurnID:          input.TurnID,
		ExecutionID:     executionID,
		ExecutionNo:     executionNo,
		Deadline:        deadline,
		ParentTurnID:    input.TurnID,
		ParentExecution: askingExecutionID,
		Query:           query,
		Mock:            session.Mock,
	}, nil
}
