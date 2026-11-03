package admin

import (
	"net/http"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"
)

type handoffResponse struct {
	Range                 string    `json:"range"`
	WindowStart           time.Time `json:"window_start"`
	WindowEnd             time.Time `json:"window_end"`
	HandoffConversations  int64     `json:"handoff_conversations"`
	CustomerConversations int64     `json:"customer_conversations"`
	HandoffRate           *float64  `json:"handoff_rate"`
}

// adminHandoff exposes the customer handoff ratio as a read-only metric. It is
// deliberately separate from workspace outcomes: customer conversations must
// never alter scored_total or the auto/assisted rates.
func (g *Handler) adminHandoff(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleObserver, adminRoleAccess, adminRoleMerchant, adminRoleReviewer)
	if !ok {
		return
	}
	rangeValue := r.URL.Query().Get("range")
	if rangeValue == "" {
		rangeValue = "30d"
	}
	days, valid := outcomesRangeDays(rangeValue)
	if !valid {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_range", "range must be 7d or 30d")
		return
	}
	now := time.Now()
	from := now.Add(-time.Duration(days) * 24 * time.Hour)
	summary, err := adminStore.CustomerHandoff(r.Context(), g.DB, scope.TenantID, scope.shops,
		scope.tenantWide, from, now)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "handoff_unavailable", "handoff metrics unavailable")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, handoffResponse{
		Range: rangeValue, WindowStart: from, WindowEnd: now,
		HandoffConversations:  summary.HandoffConversations,
		CustomerConversations: summary.CustomerConversations,
		HandoffRate:           summary.HandoffRate,
	})
}
