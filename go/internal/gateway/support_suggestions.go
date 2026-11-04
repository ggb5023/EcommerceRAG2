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

	"github.com/ggb5023/EcommerceRAG/go/internal/customer"
	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5"
)

const suggestionMaxQuery = 32768

func (g *gateway) suggestionStaff() customer.Staff {
	return customer.Staff{Tenant: g.session.TenantID, User: g.session.UserID,
		Shops: g.session.AllowedShops, Role: g.session.Role, Check: g.authority().Refresh}
}

func (g *gateway) createSuggestion(w http.ResponseWriter, r *http.Request) {
	if !g.requireWriteRole(w) || !g.authorizeHTTP(w, r) {
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if decodeJSON(r, &in) != nil {
		httpapi.WriteError(w, 400, "invalid_json", "invalid request body")
		return
	}
	text := strings.TrimSpace(in.Text)
	if text == "" || len(text) > suggestionMaxQuery {
		httpapi.WriteError(w, 400, "invalid_query", "suggestion query is invalid")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		httpapi.WriteError(w, 400, "idempotency_key_required", "Idempotency-Key is required")
		return
	}
	g.createSuggestionTurn(w, r, text, "suggestion:paste:"+key, "paste")
}

func (g *gateway) createSupportSuggestion(w http.ResponseWriter, r *http.Request) {
	if !g.requireWriteRole(w) || !g.authorizeHTTP(w, r) {
		return
	}
	if g.customer == nil {
		httpapi.WriteError(w, 503, "suggestion_unavailable", "support suggestions are unavailable")
		return
	}
	conversationID := strings.TrimSpace(r.PathValue("id"))
	if conversationID == "" {
		httpapi.WriteError(w, 404, "not_found", "support conversation not found")
		return
	}
	text, err := g.customer.LastCustomerMessage(r.Context(), g.suggestionStaff(), conversationID)
	if errors.Is(err, customer.ErrNoCustomerMessage) {
		httpapi.WriteError(w, 409, "no_customer_message", "the support conversation has no customer message")
		return
	}
	if errors.Is(err, customer.ErrSupportConversationNotFound) {
		httpapi.WriteError(w, 404, "not_found", "support conversation not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, 503, "suggestion_unavailable", "support suggestions are unavailable")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		httpapi.WriteError(w, 400, "idempotency_key_required", "Idempotency-Key is required")
		return
	}
	g.createSuggestionTurn(w, r, text, "suggestion:support:"+conversationID+":"+key, "support:"+conversationID)
}

func suggestionPayloadHash(text, source string) string {
	payload, _ := json.Marshal(map[string]string{"text": text, "source": source})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func (g *gateway) createSuggestionTurn(w http.ResponseWriter, r *http.Request, text, key, source string) {
	ctx := r.Context()
	tx, err := g.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not start suggestion")
		return
	}
	defer tx.Rollback(ctx)
	conversationID, err := ensureSuggestionConversation(ctx, tx, g.session.TenantID, g.session.UserID, g.session.Shop)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not prepare suggestion session")
		return
	}
	payloadHash := suggestionPayloadHash(text, source)
	oldHash, oldRequest, oldStatus, oldTurnID, oldExecutionNo, found, err := lookupSuggestionIdempotency(ctx, tx, g.session.TenantID, conversationID, key)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not check suggestion idempotency")
		return
	}
	if found {
		if oldHash != payloadHash {
			httpapi.WriteError(w, 409, "idempotency_conflict", "idempotency key was used with a different suggestion")
			return
		}
		httpapi.WriteJSON(w, 200, map[string]any{"id": oldRequest, "request_id": oldRequest, "status": oldStatus,
			"turn_id": strconv.FormatInt(oldTurnID, 10), "execution_no": oldExecutionNo,
			"conversation_id": strconv.FormatInt(conversationID, 10), "is_mock": isMockProfile(g.profile),
			"events_url": "/v1/turns/" + oldRequest + "/events"})
		return
	}
	active, err := suggestionActive(ctx, tx, g.session.TenantID, conversationID)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not check active suggestion")
		return
	}
	if active {
		httpapi.WriteError(w, 409, "turn_in_progress", "a suggestion is already running")
		return
	}
	requestID := newRequestID()
	deadline := time.Now().Add(turnTimeout)
	turnID, executionID, err := insertSuggestionTurn(ctx, tx, g.session.TenantID, conversationID,
		text, requestID, key, payloadHash, g.authority().CurrentRevision(), deadline, isMockProfile(g.profile))
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not persist suggestion")
		return
	}
	if err = tx.Commit(ctx); err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not commit suggestion")
		return
	}
	runCtx, cancel := context.WithDeadline(context.Background(), deadline)
	registry := g.executionRegistryOwner()
	registry.mu.Lock()
	registry.cancels[requestID] = cancel
	registry.mu.Unlock()
	go func() {
		g.runTurn(runCtx, requestID, conversationID, turnID, executionID, text, 0, 0, 1)
	}()
	httpapi.WriteJSON(w, 202, map[string]any{"id": requestID, "request_id": requestID, "status": "EXECUTING",
		"turn_id": strconv.FormatInt(turnID, 10), "execution_no": 1,
		"conversation_id": strconv.FormatInt(conversationID, 10), "is_mock": isMockProfile(g.profile),
		"events_url": "/v1/turns/" + requestID + "/events"})
}

func suggestionReplyParts(result map[string]any) (text string, canCopy bool, blocked string, citations []map[string]any) {
	reply, _ := result["customer_reply"].(map[string]any)
	switch value := reply["text_plain"].(type) {
	case string:
		text = value
	case *string:
		if value != nil {
			text = *value
		}
	}
	canCopy, _ = reply["can_copy"].(bool)
	blocked, _ = reply["blocked_reason"].(string)
	citations, _ = result["citations"].([]map[string]any)
	return text, canCopy, blocked, citations
}

func (g *gateway) writeSuggestionCopyDenied(w http.ResponseWriter, ctx context.Context, tx pgx.Tx,
	tenantID, userID int64, requestID, text, blocked string, citations []map[string]any) {
	if err := appendSuggestionCopyAudit(ctx, tx, tenantID, userID, requestID, "denied", blocked, text, len(citations)); err != nil {
		httpapi.WriteError(w, 503, "copy_unavailable", "copy audit is unavailable")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpapi.WriteError(w, 503, "copy_unavailable", "copy audit is unavailable")
		return
	}
	httpapi.WriteError(w, 403, "copy_not_allowed", "suggestion is not copyable")
}

func (g *gateway) suggestionCitationsAllowed(ctx context.Context, tx pgx.Tx, tenantID int64, requestID string, expected int) (int, bool, error) {
	citations, err := loadSuggestionCopyCitations(ctx, tx, tenantID, requestID)
	if errors.Is(err, errSuggestionCitationsIncomplete) {
		return len(citations), false, nil
	}
	if err != nil {
		return 0, false, err
	}
	allowedAll := true
	for _, item := range citations {
		allowed, allowErr := g.authority().EvidenceAllowed(ctx, &ragv1.Evidence{Id: item.id, DocumentId: item.document, VersionId: item.version, ShopId: item.shop})
		if allowErr != nil {
			return 0, false, allowErr
		}
		allowedAll = allowedAll && allowed
	}
	return len(citations), allowedAll && len(citations) > 0 && expected == len(citations), nil
}

func (g *gateway) copySuggestion(w http.ResponseWriter, r *http.Request) {
	if !g.requireWriteRole(w) || !g.authorizeHTTP(w, r) {
		return
	}
	requestID := strings.TrimSpace(r.PathValue("id"))
	if requestID == "" {
		httpapi.WriteError(w, 404, "not_found", "suggestion not found")
		return
	}
	ctx := r.Context()
	tx, err := g.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		httpapi.WriteError(w, 503, "copy_unavailable", "copy is unavailable")
		return
	}
	defer tx.Rollback(ctx)
	lockKey := "suggestion-copy:" + strconv.FormatInt(g.session.TenantID, 10) + ":" + strconv.FormatInt(g.session.UserID, 10) + ":" + requestID
	if err = lockSuggestionCopy(ctx, tx, lockKey); err != nil {
		httpapi.WriteError(w, 503, "copy_unavailable", "copy is unavailable")
		return
	}
	turnKind, conversationKind, executionStatus, found, err := loadSuggestionForCopy(ctx, tx, g.session.TenantID, g.session.UserID, g.session.Shop, requestID)
	if err != nil {
		httpapi.WriteError(w, 503, "copy_unavailable", "copy is unavailable")
		return
	}
	if !found || turnKind != "suggestion" || conversationKind != "suggestion" {
		httpapi.WriteError(w, 404, "not_found", "suggestion not found")
		return
	}
	result, err := g.executionResult(ctx, requestID)
	if err != nil {
		httpapi.WriteError(w, 503, "copy_unavailable", "copy is unavailable")
		return
	}
	text, canCopy, blocked, citations := suggestionReplyParts(result)
	if executionStatus != "DONE" || !canCopy || strings.TrimSpace(text) == "" {
		g.writeSuggestionCopyDenied(w, ctx, tx, g.session.TenantID, g.session.UserID, requestID, text, blocked, citations)
		return
	}
	citationCount, allowed, err := g.suggestionCitationsAllowed(ctx, tx, g.session.TenantID, requestID, len(citations))
	if err != nil {
		httpapi.WriteError(w, 503, "copy_unavailable", "copy is unavailable")
		return
	}
	if !allowed {
		g.writeSuggestionCopyDenied(w, ctx, tx, g.session.TenantID, g.session.UserID, requestID, text, blocked, citations)
		return
	}
	already, err := suggestionCopyAudited(ctx, tx, g.session.TenantID, g.session.UserID, requestID)
	if err != nil {
		httpapi.WriteError(w, 503, "copy_unavailable", "copy is unavailable")
		return
	}
	if already {
		_ = appendSuggestionCopyAudit(ctx, tx, g.session.TenantID, g.session.UserID, requestID, "conflict", "already_audited", text, citationCount)
		_ = tx.Commit(ctx)
		httpapi.WriteError(w, 409, "copy_already_audited", "this suggestion was already copied")
		return
	}
	if err = appendSuggestionCopyAudit(ctx, tx, g.session.TenantID, g.session.UserID, requestID, "success", "", text, citationCount); err != nil {
		httpapi.WriteError(w, 503, "copy_unavailable", "copy is unavailable")
		return
	}
	if err = tx.Commit(ctx); err != nil {
		httpapi.WriteError(w, 503, "copy_unavailable", "copy is unavailable")
		return
	}
	httpapi.WriteJSON(w, 200, map[string]any{"text": text, "citations": citations})
}
