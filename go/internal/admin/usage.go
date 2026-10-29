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
	case "all":
	default:
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_range", "range must be session, today or all")
		return
	}
	rows, err := adminStore.UsageSlots(r.Context(), g.DB, g.Session.TenantID, since)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "usage_unavailable", "usage unavailable")
		return
	}
	bySlot, byModel := []usageRow{}, []usageRow{}
	var input, output, calls, failed int64
	for _, row := range rows {
		input += row.InputTokens
		output += row.OutputTokens
		calls += row.Calls
		failed += row.FailedCalls
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
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"input_tokens": input, "output_tokens": output, "calls": calls, "failed_calls": failed, "by_slot": bySlot, "by_model": byModel})
}
