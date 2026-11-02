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

	"github.com/ggb5023/EcommerceRAG/go/internal/admin"
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
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1)::bigint)`, lockKey); err != nil {
		httpapi.WriteError(w, 503, "copy_unavailable", "copy is unavailable")
		return
	}
	var turnKind, conversationKind, executionStatus string
	err = tx.QueryRow(ctx, `SELECT t.kind,c.kind,e.status
		FROM conversation_execution e JOIN turn t ON t.tenant_id=e.tenant_id AND t.id=e.turn_id
		JOIN conversation c ON c.tenant_id=t.tenant_id AND c.id=t.conversation_id
		WHERE e.tenant_id=$1 AND e.request_id=$2 AND c.user_id=$3 AND c.shop_id=$4 FOR UPDATE OF e`,
		g.session.TenantID, requestID, g.session.UserID, g.session.Shop).Scan(&turnKind, &conversationKind, &executionStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		httpapi.WriteError(w, 503, "copy_unavailable", "copy is unavailable")
		return
	}
	if errors.Is(err, pgx.ErrNoRows) || turnKind != "suggestion" || conversationKind != "suggestion" {
		httpapi.WriteError(w, 404, "not_found", "suggestion not found")
		return
	}
	result, err := g.executionResult(ctx, requestID)
	if err != nil {
		httpapi.WriteError(w, 503, "copy_unavailable", "copy is unavailable")
		return
	}
	reply, _ := result["customer_reply"].(map[string]any)
	text := ""
	switch value := reply["text_plain"].(type) {
	case string:
		text = value
	case *string:
		if value != nil {
			text = *value
		}
	}
	canCopy, _ := reply["can_copy"].(bool)
	blocked, _ := reply["blocked_reason"].(string)
	citations, _ := result["citations"].([]map[string]any)
	deny := func() {
		if err := appendSuggestionCopyAudit(ctx, tx, g.session.TenantID, g.session.UserID, requestID, "denied", blocked, text, len(citations)); err != nil {
			httpapi.WriteError(w, 503, "copy_unavailable", "copy audit is unavailable")
			return
		}
		if err := tx.Commit(ctx); err != nil {
			httpapi.WriteError(w, 503, "copy_unavailable", "copy audit is unavailable")
			return
		}
		httpapi.WriteError(w, 403, "copy_not_allowed", "suggestion is not copyable")
	}
	if executionStatus != "DONE" || !canCopy || strings.TrimSpace(text) == "" {
		deny()
		return
	}
	rows, err := tx.Query(ctx, `SELECT c.evidence_id,COALESCE(es.document_id,''),COALESCE(es.version_id,''),COALESCE(es.shop_id,'')
		FROM citation c JOIN evidence_snapshot es ON es.tenant_id=c.tenant_id AND es.execution_id=c.execution_id AND es.evidence_id=c.evidence_id
		WHERE c.tenant_id=$1 AND c.execution_id=(SELECT id FROM conversation_execution WHERE tenant_id=$1 AND request_id=$2)
		ORDER BY c.citation_index`, g.session.TenantID, requestID)
	if err != nil {
		httpapi.WriteError(w, 503, "copy_unavailable", "copy is unavailable")
		return
	}
	citationCount := 0
	allowedAll := true
	for rows.Next() {
		var id, document, version, shop string
		if err = rows.Scan(&id, &document, &version, &shop); err != nil {
			rows.Close()
			httpapi.WriteError(w, 503, "copy_unavailable", "copy is unavailable")
			return
		}
		citationCount++
		allowed, allowErr := g.authority().EvidenceAllowed(ctx, &ragv1.Evidence{Id: id, DocumentId: document, VersionId: version, ShopId: shop})
		if allowErr != nil {
			rows.Close()
			httpapi.WriteError(w, 503, "copy_unavailable", "copy is unavailable")
			return
		}
		allowedAll = allowedAll && allowed
	}
	rows.Close()
	if err = rows.Err(); err != nil || citationCount == 0 || len(citations) != citationCount || !allowedAll {
		deny()
		return
	}
	var already bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admin_audit WHERE tenant_id=$1 AND actor_user_id=$2
		AND action='support.suggestion.copy' AND resource_type='execution' AND resource_id=$3 AND result='success')`,
		g.session.TenantID, g.session.UserID, requestID).Scan(&already); err != nil {
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

func appendSuggestionCopyAudit(ctx context.Context, tx pgx.Tx, tenantID, userID int64, requestID, result, blocked, text string, citationCount int) error {
	sum := sha256.Sum256([]byte(text))
	store := admin.Store{}
	return store.AppendAudit(ctx, tx, admin.AuditEntry{
		TenantID: tenantID, UserID: userID, Action: "support.suggestion.copy",
		ResourceType: "execution", ResourceID: requestID, Result: result,
		Metadata: map[string]any{"reply_sha256": hex.EncodeToString(sum[:]),
			"citation_count": citationCount, "blocked_reason": blocked},
	})
}
