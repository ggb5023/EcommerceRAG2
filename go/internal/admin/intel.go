package admin

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

type intelObservationFilter struct {
	Market   string
	Category string
	Platform string
	Since    string
	Limit    int32
}

func parseIntelObservationFilter(values url.Values) (intelObservationFilter, bool) {
	filter := intelObservationFilter{
		Market:   strings.TrimSpace(values.Get("market")),
		Category: strings.TrimSpace(values.Get("category")),
		Platform: strings.TrimSpace(values.Get("platform")),
		Limit:    50,
	}
	if raw := strings.TrimSpace(values.Get("limit")); raw != "" {
		limit, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || limit < 1 || limit > 100 {
			return filter, false
		}
		filter.Limit = int32(limit)
	}
	if raw := strings.TrimSpace(values.Get("since")); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return filter, false
		}
		filter.Since = parsed.UTC().Format(time.RFC3339Nano)
	}
	return filter, true
}

// adminIntelObservations proxies the shared observation read; it never touches
// market_observation with the gateway role.
func (g *Handler) adminIntelObservations(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.requireAdmin(w, r, adminRoleMerchant, adminRoleReviewer); !ok {
		return
	}
	filter, valid := parseIntelObservationFilter(r.URL.Query())
	if !valid {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_intel_filter", "market/category/platform/since/limit are invalid")
		return
	}
	if g.Intel == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "intel_unavailable", "intelligence service is unavailable")
		return
	}
	response, err := g.Intel.ListObservations(r.Context(), &ragv1.ListObservationsRequest{
		Market: filter.Market, Category: filter.Category, Platform: filter.Platform,
		Since: filter.Since, Limit: filter.Limit,
	})
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "intel_unavailable", "intelligence service is unavailable")
		return
	}
	items := make([]map[string]any, 0, len(response.GetItems()))
	for _, item := range response.GetItems() {
		items = append(items, map[string]any{
			"market": item.GetMarket(), "category": item.GetCategory(), "platform": item.GetPlatform(),
			"source_url": item.GetSourceUrl(), "title": item.GetTitle(), "score": item.GetScore(),
			"published_date": item.GetPublishedDate(), "observed_at": item.GetObservedAt(),
		})
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (g *Handler) adminIntelWatchConfigs(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant, adminRoleReviewer)
	if !ok {
		return
	}
	items, err := adminStore.IntelWatchConfigs(r.Context(), g.DB, scope.TenantID)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "intel_unavailable", "watch configs unavailable")
		return
	}
	response := make([]map[string]any, 0, len(items))
	for _, item := range items {
		response = append(response, intelWatchConfigResponse(item))
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"items": response})
}

func (g *Handler) adminIntelCreateWatchConfig(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant, adminRoleReviewer)
	if !ok {
		return
	}
	var in struct {
		Market   string `json:"market"`
		Category string `json:"category"`
		Platform string `json:"platform"`
		Enabled  *bool  `json:"enabled"`
	}
	if httpapi.DecodeJSON(r, &in) != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_watch_config", "market, category and platform are required")
		return
	}
	market, category, platform := strings.TrimSpace(in.Market), strings.TrimSpace(in.Category), strings.TrimSpace(in.Platform)
	if !validIntelWatchFields(market, category, platform) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_watch_config", "market, category and platform are required")
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	item, err := adminStore.CreateIntelWatchConfig(r.Context(), g.DB, scope.TenantID,
		market, category, platform, enabled)
	switch {
	case errors.Is(err, errIntelWatchConfigExists):
		httpapi.WriteError(w, http.StatusConflict, "watch_config_exists", "watch config already exists")
	case err != nil:
		httpapi.WriteError(w, http.StatusServiceUnavailable, "intel_unavailable", "watch config unavailable")
	default:
		httpapi.WriteJSON(w, http.StatusCreated, intelWatchConfigResponse(item))
	}
}

func (g *Handler) adminIntelUpdateWatchConfig(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant, adminRoleReviewer)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "watch config not found")
		return
	}
	var in struct {
		Market   *string `json:"market"`
		Category *string `json:"category"`
		Platform *string `json:"platform"`
		Enabled  *bool   `json:"enabled"`
	}
	if httpapi.DecodeJSON(r, &in) != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_watch_config", "watch config fields are invalid")
		return
	}
	for _, value := range []*string{in.Market, in.Category, in.Platform} {
		if value != nil && (strings.TrimSpace(*value) == "" || len(strings.TrimSpace(*value)) > 200) {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_watch_config", "watch config fields are invalid")
			return
		}
	}
	item, err := adminStore.UpdateIntelWatchConfig(r.Context(), g.DB, scope.TenantID, id,
		trimmedOrNil(in.Market), trimmedOrNil(in.Category), trimmedOrNil(in.Platform), in.Enabled)
	switch {
	case errors.Is(err, errIntelWatchConfigNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "watch config not found")
	case err != nil:
		httpapi.WriteError(w, http.StatusServiceUnavailable, "intel_unavailable", "watch config unavailable")
	default:
		httpapi.WriteJSON(w, http.StatusOK, intelWatchConfigResponse(item))
	}
}

func validIntelWatchFields(market, category, platform string) bool {
	for _, value := range []string{market, category, platform} {
		if value == "" || len(value) > 200 {
			return false
		}
	}
	return true
}

func trimmedOrNil(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}

func intelWatchConfigResponse(row IntelWatchConfigRow) map[string]any {
	return map[string]any{
		"id": row.ID, "market": row.Market, "category": row.Category,
		"platform": row.Platform, "enabled": row.Enabled, "created_at": row.CreatedAt,
	}
}
