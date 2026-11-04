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

	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"
	"github.com/jackc/pgx/v5"
)

// This file holds the HTTP entry layer for turns. Handlers here may take
// *http.Request / http.ResponseWriter: they are the boundary that turns a
// request into a turnRequestContext, and everything below them (turn_*.go stage
// files) receives plain structs instead. Do not treat these as stage functions.

func (g *gateway) createTurn(w http.ResponseWriter, r *http.Request) {
	if !g.requireWriteRole(w) {
		return
	}
	conversationID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, 404, "not_found", "conversation not found")
		return
	}
	var in struct {
		Text         string `json:"text"`
		Query        string `json:"query"`
		ParentTurnID string `json:"parent_turn_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httpapi.WriteError(w, 400, "invalid_json", "invalid request body")
		return
	}
	session := turnCreateSessionData{
		TenantID:           g.session.TenantID,
		UserID:             g.session.UserID,
		Shop:               g.session.Shop,
		PermissionRevision: g.authority().CurrentRevision(),
		Mock:               isMockProfile(g.profile),
	}
	result, err := startTurn(r.Context(), turnCreateStore{db: g.db}, session, turnCreateInput{
		ConversationID: conversationID,
		Text:           in.Text,
		Query:          in.Query,
		ParentTurnID:   in.ParentTurnID,
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeTurnCreateError(w, err)
		return
	}
	if !result.Existing {
		g.launchTurn(result)
	}
	httpapi.WriteJSON(w, turnCreateStatus(result), turnCreateResponse(result))
}

// turnCreateStatus keeps the replay/created status switch out of the handler
// body while preserving the original 200/202 distinction.
func turnCreateStatus(result turnCreateResult) int {
	if result.Existing {
		return 200
	}
	return 202
}

func turnCreateResponse(result turnCreateResult) map[string]any {
	return map[string]any{
		"id":              result.RequestID,
		"request_id":      result.RequestID,
		"status":          result.Status,
		"turn_id":         strconv.FormatInt(result.TurnID, 10),
		"execution_no":    result.ExecutionNo,
		"conversation_id": strconv.FormatInt(result.ConversationID, 10),
		"events_url":      "/v1/turns/" + result.RequestID + "/events",
		"is_mock":         result.Mock,
	}
}

func writeTurnCreateError(w http.ResponseWriter, err error) {
	var failure *turnCreateFailure
	if errors.As(err, &failure) {
		httpapi.WriteError(w, failure.status, failure.code, failure.message)
		return
	}
	httpapi.WriteError(w, 503, "database_unavailable", "could not persist turn")
}

// launchTurn starts the asynchronous execution after the create transaction
// commits. It stays in the HTTP entry file because it owns the gateway cancel
// registry, which must not leak into the store/use-case layer.
func (g *gateway) launchTurn(result turnCreateResult) {
	ctx, cancel := context.WithDeadline(context.Background(), result.Deadline)
	registry := g.executionRegistryOwner()
	registry.mu.Lock()
	registry.cancels[result.RequestID] = cancel
	registry.mu.Unlock()
	go func() {
		g.runTurn(ctx, result.RequestID, result.ConversationID, result.TurnID, result.ExecutionID,
			result.Query, result.ParentTurnID, result.ParentExecution, 1)
	}()
}

func (g *gateway) resumeTurn(w http.ResponseWriter, r *http.Request) {
	if !g.requireWriteRole(w) {
		return
	}
	if err := g.authority().Refresh(r.Context()); err != nil {
		httpapi.WriteError(w, authorizationHTTPStatus(err), auth.FailureCode(err), "authorization failed")
		return
	}
	if !isV2Profile(g.profile) {
		httpapi.WriteError(w, http.StatusConflict, "checkpoint_resume_unavailable", "checkpoint resume is available only for the v2 graph profile")
		return
	}
	turnID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "turn not found")
		return
	}
	var in struct {
		Text   string `json:"text"`
		Answer string `json:"answer"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_json", "invalid request body")
		return
	}
	query := strings.TrimSpace(in.Text)
	if query == "" {
		query = strings.TrimSpace(in.Answer)
	}
	if query == "" {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_query", "answer is required")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		httpapi.WriteError(w, http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key is required")
		return
	}
	canonicalPayload, _ := json.Marshal(map[string]string{"answer": query})
	hash := sha256.Sum256(canonicalPayload)
	payloadHash := hex.EncodeToString(hash[:])
	storageKey := "resume:" + key
	tx, err := g.db.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "database_unavailable", "could not start resume")
		return
	}
	defer tx.Rollback(r.Context())
	var conversationID int64
	var shopID, turnStatus string
	err = tx.QueryRow(r.Context(), `SELECT t.conversation_id,c.shop_id,t.status
		FROM turn t JOIN conversation c ON c.tenant_id=t.tenant_id AND c.id=t.conversation_id
		WHERE t.tenant_id=$1 AND t.id=$2 AND c.user_id=$3 FOR UPDATE OF t,c`,
		g.session.TenantID, turnID, g.session.UserID).Scan(&conversationID, &shopID, &turnStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "turn not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "database_unavailable", "could not lock turn")
		return
	}
	if shopID != g.session.Shop {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "turn is outside the current session")
		return
	}
	if !g.authorizeConversationHistory(w, r, conversationID) {
		return
	}
	var oldHash string
	var oldTurnID, oldExecutionID int64
	err = tx.QueryRow(r.Context(), `SELECT payload_hash,turn_id,COALESCE(execution_id,
		(SELECT e.id FROM conversation_execution e
		 WHERE e.tenant_id=turn_idempotency.tenant_id AND e.turn_id=turn_idempotency.turn_id
		 ORDER BY e.execution_no ASC LIMIT 1))
		FROM turn_idempotency
		WHERE tenant_id=$1 AND conversation_id=$2 AND idempotency_key=$3`,
		g.session.TenantID, conversationID, storageKey).Scan(&oldHash, &oldTurnID, &oldExecutionID)
	if err == nil {
		if oldHash != payloadHash || oldTurnID != turnID {
			httpapi.WriteError(w, http.StatusConflict, "idempotency_conflict", "idempotency key was used with a different resume")
			return
		}
		var oldRequest, oldStatus string
		var oldExecutionNo int64
		err = tx.QueryRow(r.Context(), `SELECT request_id,status,execution_no FROM conversation_execution
			WHERE tenant_id=$1 AND id=$2`, g.session.TenantID, oldExecutionID).Scan(&oldRequest, &oldStatus, &oldExecutionNo)
		if err != nil {
			httpapi.WriteError(w, http.StatusServiceUnavailable, "database_unavailable", "could not load resumed execution")
			return
		}
		httpapi.WriteJSON(w, http.StatusOK, map[string]any{"id": oldRequest, "request_id": oldRequest,
			"status": oldStatus, "turn_id": strconv.FormatInt(turnID, 10), "execution_no": oldExecutionNo,
			"conversation_id": strconv.FormatInt(conversationID, 10), "events_url": "/v1/turns/" + oldRequest + "/events", "is_mock": isMockProfile(g.profile)})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "database_unavailable", "could not check resume idempotency")
		return
	}
	if turnStatus != "ASKING" {
		httpapi.WriteError(w, http.StatusConflict, "turn_not_asking", "turn is not awaiting clarification")
		return
	}
	var askingExecutionID int64
	var askingExecutionNo int64
	err = tx.QueryRow(r.Context(), `SELECT id,execution_no FROM conversation_execution
		WHERE tenant_id=$1 AND turn_id=$2 AND status='ASKING'
		ORDER BY execution_no DESC LIMIT 1 FOR UPDATE`, g.session.TenantID, turnID).Scan(&askingExecutionID, &askingExecutionNo)
	if errors.Is(err, pgx.ErrNoRows) {
		httpapi.WriteError(w, http.StatusConflict, "parent_checkpoint_missing", "turn has no pending checkpoint execution")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "database_unavailable", "could not load checkpoint execution")
		return
	}
	requestID := newRequestID()
	executionNo := askingExecutionNo + 1
	deadline := time.Now().Add(turnTimeout)
	var executionID int64
	err = tx.QueryRow(r.Context(), `INSERT INTO conversation_execution(tenant_id,turn_id,execution_no,request_id,status,is_mock,deadline_at,permission_revision)
			VALUES($1,$2,$3,$4,'EXECUTING',$7,$5,$6) RETURNING id`,
		g.session.TenantID, turnID, executionNo, requestID, deadline, g.authority().CurrentRevision(), isMockProfile(g.profile)).Scan(&executionID)
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE turn SET status='EXECUTING',answer=NULL WHERE tenant_id=$1 AND id=$2`, g.session.TenantID, turnID)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO turn_idempotency(tenant_id,conversation_id,idempotency_key,payload_hash,turn_id,execution_id)
			VALUES($1,$2,$3,$4,$5,$6)`, g.session.TenantID, conversationID, storageKey, payloadHash, turnID, executionID)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO message(tenant_id,turn_id,role,content) VALUES($1,$2,'user',$3)`, g.session.TenantID, turnID, query)
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "database_unavailable", "could not persist resume")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "database_unavailable", "could not commit resume")
		return
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	registry := g.executionRegistryOwner()
	registry.mu.Lock()
	registry.cancels[requestID] = cancel
	registry.mu.Unlock()
	go func() {
		g.runTurn(ctx, requestID, conversationID, turnID, executionID, query, turnID, askingExecutionID, executionNo)
	}()
	httpapi.WriteJSON(w, http.StatusAccepted, map[string]any{"id": requestID, "request_id": requestID,
		"status": "EXECUTING", "turn_id": strconv.FormatInt(turnID, 10), "execution_no": executionNo,
		"conversation_id": strconv.FormatInt(conversationID, 10), "is_mock": isMockProfile(g.profile),
		"events_url": "/v1/turns/" + requestID + "/events"})
}

func (g *gateway) events(w http.ResponseWriter, r *http.Request) {
	requestID := r.PathValue("id")
	if !g.authorizeExecution(w, r, requestID) {
		return
	}
	var executionID int64
	var deadline time.Time
	err := g.db.QueryRow(r.Context(), `SELECT e.id,e.deadline_at FROM conversation_execution e
		JOIN turn t ON t.tenant_id=e.tenant_id AND t.id=e.turn_id
		JOIN conversation c ON c.tenant_id=t.tenant_id AND c.id=t.conversation_id
		WHERE e.tenant_id=$1 AND e.request_id=$2 AND c.user_id=$3 AND c.shop_id=$4`,
		g.session.TenantID, requestID, g.session.UserID, g.session.Shop).Scan(&executionID, &deadline)
	if errors.Is(err, pgx.ErrNoRows) {
		httpapi.WriteError(w, 404, "not_found", "turn not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load events")
		return
	}
	lastID, _ := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
	if header := r.URL.Query().Get("last_event_id"); header != "" && lastID == 0 {
		lastID, _ = strconv.ParseInt(header, 10, 64)
	}
	stream := newSSEStream(w)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := g.authority().Refresh(r.Context()); err != nil {
			stream.errorFrameWhenFlushable(auth.FailureCode(err), "authorization failed", requestID, authorizationHTTPStatus(err))
			return
		}
		rows, queryErr := g.db.Query(r.Context(), `SELECT sequence,event_type,data_json::text FROM sse_event
			WHERE tenant_id=$1 AND execution_id=$2 AND sequence>$3 ORDER BY sequence`, g.session.TenantID, executionID, lastID)
		if queryErr != nil {
			return
		}
		count := 0
		for rows.Next() {
			var seq int64
			var kind, data string
			if rows.Scan(&seq, &kind, &data) != nil {
				rows.Close()
				return
			}
			if authErr := g.authority().Refresh(r.Context()); authErr != nil {
				rows.Close()
				stream.errorFrame(auth.FailureCode(authErr), "authorization failed", requestID, authorizationHTTPStatus(authErr))
				return
			}
			stream.frame(seq, kind, data)
			lastID, count = seq, count+1
		}
		rows.Close()
		if count > 0 {
			stream.flush()
		}
		var state string
		if err := g.db.QueryRow(r.Context(), `SELECT status FROM conversation_execution WHERE tenant_id=$1 AND id=$2`, g.session.TenantID, executionID).Scan(&state); err != nil {
			return
		}
		if terminal(state) && count == 0 {
			return
		}
		if !deadline.IsZero() && time.Now().After(deadline.Add(5*time.Second)) {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if count == 0 {
				stream.keepAlive()
			}
		}
	}
}

func (g *gateway) getConversation(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, 404, "not_found", "conversation not found")
		return
	}
	var title string
	err = g.db.QueryRow(r.Context(), `SELECT COALESCE(title,'') FROM conversation
		WHERE tenant_id=$1 AND user_id=$2 AND shop_id=$3 AND id=$4 AND kind='user'`,
		g.session.TenantID, g.session.UserID, g.session.Shop, id).Scan(&title)
	if errors.Is(err, pgx.ErrNoRows) {
		httpapi.WriteError(w, 404, "not_found", "conversation not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load conversation")
		return
	}
	if !g.authorizeConversationHistory(w, r, id) {
		return
	}
	rows, err := g.db.Query(r.Context(), `SELECT m.Role, COALESCE(m.content,''), t.request_id,t.id,COALESCE((SELECT MAX(e.execution_no) FROM conversation_execution e WHERE e.tenant_id=t.tenant_id AND e.turn_id=t.id),1)
		FROM turn t JOIN message m ON m.tenant_id=t.tenant_id AND m.turn_id=t.id
		WHERE t.tenant_id=$1 AND t.conversation_id=$2 ORDER BY t.turn_no,m.created_at`, g.session.TenantID, id)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load messages")
		return
	}
	defer rows.Close()
	messages := make([]map[string]any, 0)
	for rows.Next() {
		var role, content, requestID string
		var turnID int64
		var executionNo int
		if err := rows.Scan(&role, &content, &requestID, &turnID, &executionNo); err != nil {
			httpapi.WriteError(w, 503, "database_unavailable", "could not load messages")
			return
		}
		messages = append(messages, map[string]any{"role": role, "content": content, "request_id": requestID, "turn_id": strconv.FormatInt(turnID, 10), "execution_no": executionNo, "mock": role == "assistant"})
	}
	if err = rows.Err(); err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load messages")
		return
	}
	rows.Close()
	var latestRequest string
	err = g.db.QueryRow(r.Context(), `SELECT e.request_id FROM turn t JOIN conversation_execution e ON e.tenant_id=t.tenant_id AND e.turn_id=t.id
		WHERE t.tenant_id=$1 AND t.conversation_id=$2 ORDER BY t.turn_no DESC,e.execution_no DESC LIMIT 1`, g.session.TenantID, id).Scan(&latestRequest)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load execution")
		return
	}
	result := map[string]any{"id": r.PathValue("id"), "title": title, "messages": messages}
	if latestRequest != "" {
		last, loadErr := g.executionResult(r.Context(), latestRequest)
		if loadErr != nil {
			httpapi.WriteError(w, 503, "database_unavailable", "could not load execution")
			return
		}
		result["last_turn"] = last
		result["evidence"] = last["evidence"]
		result["citations"] = last["citations"]
		result["lastReply"] = last["answer"]
		result["lastReplyCopyable"] = false
	}
	if err := g.authority().Refresh(r.Context()); err != nil {
		httpapi.WriteError(w, authorizationHTTPStatus(err), auth.FailureCode(err), "authorization failed")
		return
	}
	httpapi.WriteJSON(w, 200, result)
}

func (g *gateway) cancelTurn(w http.ResponseWriter, r *http.Request) {
	if !g.requireWriteRole(w) {
		return
	}
	requestID := r.PathValue("id")
	if !g.authorizeExecution(w, r, requestID) {
		return
	}
	tx, err := g.db.Begin(r.Context())
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not cancel turn")
		return
	}
	defer tx.Rollback(r.Context())
	var state string
	var executionID, turnID int64
	err = tx.QueryRow(r.Context(), `SELECT e.status,e.id,e.turn_id FROM conversation_execution e
		JOIN turn t ON t.tenant_id=e.tenant_id AND t.id=e.turn_id JOIN conversation c ON c.tenant_id=t.tenant_id AND c.id=t.conversation_id
		WHERE e.tenant_id=$1 AND e.request_id=$2 AND c.user_id=$3 AND c.shop_id=$4 FOR UPDATE OF e`, g.session.TenantID, requestID, g.session.UserID, g.session.Shop).Scan(&state, &executionID, &turnID)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not cancel turn")
		return
	}
	if terminal(state) {
		httpapi.WriteJSON(w, 200, map[string]any{"request_id": requestID, "status": state})
		return
	}
	_, err = tx.Exec(r.Context(), `UPDATE conversation_execution SET cancel_requested=true,status='CANCEL_REQUESTED' WHERE tenant_id=$1 AND id=$2`, g.session.TenantID, executionID)
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE turn SET status='CANCEL_REQUESTED' WHERE tenant_id=$1 AND id=$2`, g.session.TenantID, turnID)
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not persist cancellation")
		return
	}
	registry := g.executionRegistryOwner()
	registry.mu.Lock()
	stop := registry.cancels[requestID]
	registry.mu.Unlock()
	if stop != nil {
		stop()
	}
	httpapi.WriteJSON(w, 200, map[string]any{"request_id": requestID, "status": "CANCEL_REQUESTED"})
}
