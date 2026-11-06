package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"
)

const answerListDefaultLimit = 20
const answerListMaxLimit = 100
const answerPreviewRunes = 200

// adminAnswers lists staff and customer answers for citation review. The
// response never contains customer identity, sessions, addresses or cookies.
func (g *Handler) adminAnswers(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant, adminRoleReviewer)
	if !ok {
		return
	}
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	if kind == "" {
		kind = "all"
	}
	if kind != "staff" && kind != "customer" && kind != "all" {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_answer_kind", "kind must be staff, customer or all")
		return
	}
	var mock *bool
	switch strings.TrimSpace(r.URL.Query().Get("mock")) {
	case "":
	case "true":
		value := true
		mock = &value
	case "false":
		value := false
		mock = &value
	default:
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_answer_mock", "mock must be true or false")
		return
	}
	limit := answerListDefaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > answerListMaxLimit {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_answer_limit", "limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	var before *time.Time
	if raw := r.URL.Query().Get("before"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_answer_cursor", "before must be an RFC3339 timestamp")
			return
		}
		before = &parsed
	}
	rows, next, err := adminStore.MerchantAnswers(r.Context(), g.DB, scope.TenantID, scope.shops, kind, mock, before, limit)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "answers_unavailable", "answers are unavailable")
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{
			"kind": row.Kind, "id": row.ID, "request_id": row.RequestID,
			"created_at": row.CreatedAt.UTC().Format(time.RFC3339Nano), "shop_id": row.ShopID,
			"question": truncateRunes(row.Question, answerPreviewRunes),
			"answer": truncateRunes(row.Answer, answerPreviewRunes),
			"citation_count": row.CitationCount, "fact_count": row.FactCount,
			"is_mock": row.IsMock, "behavior": row.Behavior, "state": row.State,
		})
	}
	response := map[string]any{"items": items}
	if next != nil {
		response["next_before"] = next.UTC().Format(time.RFC3339Nano)
	}
	httpapi.WriteJSON(w, http.StatusOK, response)
}

// adminAnswerDetail returns full text, citations and fact snapshots, and records
// the review access without storing any body.
func (g *Handler) adminAnswerDetail(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant, adminRoleReviewer)
	if !ok {
		return
	}
	kind, id := r.PathValue("kind"), strings.TrimSpace(r.PathValue("id"))
	if (kind != "staff" && kind != "customer") || id == "" {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "answer not found")
		return
	}
	detail, err := adminStore.MerchantAnswer(r.Context(), g.DB, scope.TenantID, scope.shops, kind, id)
	if errors.Is(err, errAnswerNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "answer not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "answers_unavailable", "answer is unavailable")
		return
	}
	if err = adminStore.RecordAnswerViewAudit(r.Context(), g.DB, scope.TenantID, scope.UserID, kind, id); err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "answers_unavailable", "answer audit is unavailable")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"kind": detail.Kind, "id": detail.ID, "request_id": detail.RequestID,
		"created_at": detail.CreatedAt.UTC().Format(time.RFC3339Nano), "shop_id": detail.ShopID,
		"question": detail.Question, "answer": detail.Answer,
		"citation_count": detail.CitationCount, "fact_count": detail.FactCount,
		"is_mock": detail.IsMock, "behavior": detail.Behavior, "state": detail.State,
		"citations": detail.Citations, "facts": detail.Facts,
	})
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}
