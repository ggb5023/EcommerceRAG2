package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"
)

// adminRunInspections runs the four v1 rules and returns the per-kind summary.
func (g *Handler) adminRunInspections(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant, adminRoleReviewer)
	if !ok {
		return
	}
	summary, err := g.runInspections(r.Context(), scope)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "inspection_unavailable", "inspection run failed")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, summary)
}

// adminListInspections lists reports in one status for the caller's shop scope.
func (g *Handler) adminListInspections(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleObserver, adminRoleAccess, adminRoleMerchant, adminRoleReviewer)
	if !ok {
		return
	}
	status, valid := inspectionListStatus(r.URL.Query().Get("status"))
	if !valid {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_inspection_status", "status must be open, acknowledged, resolved or dismissed")
		return
	}
	items, err := adminStore.InspectionReports(r.Context(), g.DB, scope.TenantID, scope.shops, scope.tenantWide, status)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "inspection_unavailable", "inspection reports unavailable")
		return
	}
	response := make([]map[string]any, 0, len(items))
	for _, item := range items {
		response = append(response, inspectionResponse(item))
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"items": response})
}

// adminInspectionDetail returns the evidence panel for one report.
func (g *Handler) adminInspectionDetail(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleObserver, adminRoleAccess, adminRoleMerchant, adminRoleReviewer)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "inspection report not found")
		return
	}
	item, err := adminStore.InspectionReport(r.Context(), g.DB, scope.TenantID, id, scope.shops, scope.tenantWide)
	if errors.Is(err, errInspectionNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "inspection report not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "inspection_unavailable", "inspection report unavailable")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, inspectionResponse(item))
}

// adminInspectionStatus applies one merchant triage action.
func (g *Handler) adminInspectionStatus(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant, adminRoleReviewer)
	if !ok {
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if httpapi.DecodeJSON(r, &in) != nil || !contains([]string{"acknowledged", "resolved", "dismissed"}, in.Status) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_inspection_status", "status must be acknowledged, resolved or dismissed")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "inspection report not found")
		return
	}
	item, err := adminStore.UpdateInspectionStatus(r.Context(), g.DB, scope.TenantID, id, scope.shops, scope.tenantWide, in.Status)
	switch {
	case errors.Is(err, errInspectionNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "inspection report not found")
	case errors.Is(err, errInspectionClosed):
		httpapi.WriteError(w, http.StatusConflict, "inspection_closed", "inspection report is already closed")
	case err != nil:
		httpapi.WriteError(w, http.StatusServiceUnavailable, "inspection_unavailable", "inspection report unavailable")
	default:
		httpapi.WriteJSON(w, http.StatusOK, inspectionResponse(item))
	}
}

func inspectionListStatus(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "open", true
	}
	switch value {
	case "open", "acknowledged", "resolved", "dismissed":
		return value, true
	default:
		return "", false
	}
}

func inspectionResponse(row inspectionReportRow) map[string]any {
	return map[string]any{
		"id": row.ID, "shop_id": row.ShopID, "kind": row.Kind, "severity": row.Severity,
		"status": row.Status, "subject_type": row.SubjectType, "subject_id": row.SubjectID,
		"evidence": row.Evidence, "detected_at": row.DetectedAt, "resolved_at": row.ResolvedAt,
	}
}
