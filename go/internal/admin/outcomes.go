package admin

import (
	"log"
	"net/http"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"
)

const (
	outcomesDefaultRange = 30
	outcomesWeekRange    = 7
)

// adminOutcomes reports how the tenant's conversations ended in one window.
// The tenant comes from the session; a client-supplied tenant is never read.
func (g *Handler) adminOutcomes(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleObserver, adminRoleAccess, adminRoleMerchant, adminRoleReviewer)
	if !ok {
		return
	}
	days, valid := outcomesRangeDays(r.URL.Query().Get("range"))
	if !valid {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_range", "range must be 7d or 30d")
		return
	}
	from := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	summary, err := adminStore.ResolutionSummary(r.Context(), g.DB, scope.TenantID, from)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "outcomes_unavailable", "outcomes unavailable")
		return
	}
	g.respondOutcomes(w, scope.TenantID, summary)
}

// respondOutcomes keeps the page usable when the buckets do not add up: the
// anomaly is logged and flagged in the payload instead of turning the whole
// metric page into a 500. A false flag is always serialized so the frontend can
// test it without guessing the field's absence.
func (g *Handler) respondOutcomes(w http.ResponseWriter, tenantID int64, summary ResolutionSummary) {
	if summary.TurnsTotal != summary.ScoredTotal+summary.Unknown+summary.Pending {
		log.Printf("outcome metric identity violated: tenant=%d turns=%d scored=%d unknown=%d pending=%d",
			tenantID, summary.TurnsTotal, summary.ScoredTotal, summary.Unknown, summary.Pending)
		summary.MetricGap = true
	}
	httpapi.WriteJSON(w, http.StatusOK, summary)
}

// outcomesRangeDays maps the only two accepted windows. An empty value means
// the documented default rather than an error.
func outcomesRangeDays(value string) (int, bool) {
	switch value {
	case "", "30d":
		return outcomesDefaultRange, true
	case "7d":
		return outcomesWeekRange, true
	default:
		return 0, false
	}
}
