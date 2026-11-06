package admin

import (
	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"
	"net/http"
	"time"
)

type usageRow struct {
	Slot         string `json:"slot"`
	Model        string `json:"model"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	Calls        int64  `json:"calls"`
	FailedCalls  int64  `json:"failed_calls"`
}

type usageTotals struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	Calls        int64 `json:"calls"`
	FailedCalls  int64 `json:"failed_calls"`
}

func (totals *usageTotals) add(row UsageRow) {
	totals.InputTokens += row.InputTokens
	totals.OutputTokens += row.OutputTokens
	totals.Calls += row.Calls
	totals.FailedCalls += row.FailedCalls
}

// usageResponse keeps NULL-tenant rows out of tenant totals and breaks them
// out under "unattributed". A tenant must never see them as its own spend.
func usageResponse(rows []UsageRow) map[string]any {
	bySlot, byModel := []usageRow{}, []usageRow{}
	var totals, unattributed usageTotals
	for _, row := range rows {
		if row.Unattributed {
			unattributed.add(row)
			continue
		}
		totals.add(row)
		if len(bySlot) == 0 || bySlot[len(bySlot)-1].Slot != row.Slot {
			bySlot = append(bySlot, usageRow{Slot: row.Slot})
		}
		i := len(bySlot) - 1
		bySlot[i].InputTokens += row.InputTokens
		bySlot[i].OutputTokens += row.OutputTokens
		bySlot[i].Calls += row.Calls
		bySlot[i].FailedCalls += row.FailedCalls
		if len(byModel) == 0 || byModel[len(byModel)-1].Model != row.Model {
			byModel = append(byModel, usageRow{Model: row.Model})
		}
		j := len(byModel) - 1
		byModel[j].InputTokens += row.InputTokens
		byModel[j].OutputTokens += row.OutputTokens
		byModel[j].Calls += row.Calls
		byModel[j].FailedCalls += row.FailedCalls
	}
	return map[string]any{
		"input_tokens": totals.InputTokens, "output_tokens": totals.OutputTokens,
		"calls": totals.Calls, "failed_calls": totals.FailedCalls,
		"by_slot": bySlot, "by_model": byModel, "unattributed": unattributed,
	}
}

func (g *Handler) adminUsage(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.requireAdmin(w, r, adminRoleObserver, adminRoleAccess, adminRoleMerchant, adminRoleReviewer); !ok {
		return
	}
	rangeValue := r.URL.Query().Get("range")
	if rangeValue == "" {
		rangeValue = "today"
	}
	var since *time.Time
	now := time.Now()
	switch rangeValue {
	case "session":
		start := now.Add(-time.Hour)
		since = &start
	case "today":
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		since = &start
	case "7d", "30d":
		days, _ := outcomesRangeDays(rangeValue)
		start := now.Add(-time.Duration(days) * 24 * time.Hour)
		since = &start
	case "all":
	default:
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_range", "range must be session, today, 7d, 30d or all")
		return
	}
	rows, err := adminStore.UsageSlots(r.Context(), g.DB, g.Session.TenantID, since)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "usage_unavailable", "usage unavailable")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, usageResponse(rows))
}
