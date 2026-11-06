package gateway

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	"github.com/ggb5023/EcommerceRAG/go/internal/domain"
	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"
)

// catalogBrowseFilter is the staff-facing, read-only product directory filter.
// Price fields are deliberately absent: the UI must not show currency amounts
// (decision 0021), and stock/order data is not part of this release.
type catalogBrowseFilter struct {
	Query       string
	Category    string
	SKU         string
	Attribute   string
	AttrValue   string
	Page        int
	PageSize    int
}

const (
	catalogBrowseMaxQuery    = 120
	catalogBrowseMaxCategory = 128
	catalogBrowseMaxSKU      = 128
	catalogBrowseMaxAttr     = 200
	catalogBrowseDefaultPage = 24
	catalogBrowseMaxPage     = 100
)

func parseCatalogBrowseFilter(values map[string][]string) (catalogBrowseFilter, bool) {
	get := func(key string) string {
		if len(values[key]) == 0 {
			return ""
		}
		return strings.TrimSpace(values[key][0])
	}
	filter := catalogBrowseFilter{
		Query:     get("q"),
		Category:  get("category"),
		SKU:       get("sku"),
		Attribute: strings.ToLower(get("attribute_key")),
		AttrValue: get("attribute_value"),
		Page:      1,
		PageSize:  catalogBrowseDefaultPage,
	}
	if len([]rune(filter.Query)) > catalogBrowseMaxQuery ||
		len(filter.Category) > catalogBrowseMaxCategory || len(filter.SKU) > catalogBrowseMaxSKU ||
		len(filter.AttrValue) > catalogBrowseMaxAttr {
		return filter, false
	}
	if filter.Attribute != "" {
		if !domain.CatalogAttributeAllowed(filter.Attribute) || filter.AttrValue == "" {
			return filter, false
		}
	} else if filter.AttrValue != "" {
		return filter, false
	}
	if raw := get("page"); raw != "" {
		page, err := strconv.Atoi(raw)
		if err != nil || page < 1 {
			return filter, false
		}
		filter.Page = page
	}
	if raw := get("page_size"); raw != "" {
		size, err := strconv.Atoi(raw)
		if err != nil || size < 1 || size > catalogBrowseMaxPage {
			return filter, false
		}
		filter.PageSize = size
	}
	return filter, true
}

// catalogSkus lists published product rows for the current shop. Authorization
// and the shop scope are decided here; visibility dates come from the shop's
// business date, never from the browser.
func (g *gateway) catalogSkus(w http.ResponseWriter, r *http.Request) {
	if !slices.Contains([]string{"owner", "admin", "operator", "viewer"}, g.session.Role) {
		httpapi.WriteError(w, http.StatusForbidden, "role_forbidden", "current role cannot read the product directory")
		return
	}
	shopID := strings.TrimSpace(r.URL.Query().Get("shop_id"))
	if shopID == "" {
		shopID = g.session.Shop
	}
	if shopID == "" || !slices.Contains(g.session.AllowedShops, shopID) {
		httpapi.WriteError(w, http.StatusForbidden, "admin_scope_denied", "shop is outside the current identity scope")
		return
	}
	filter, valid := parseCatalogBrowseFilter(r.URL.Query())
	if !valid {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_catalog_query", "catalog filters are invalid")
		return
	}
	businessDate, err := auth.ShopBusinessDate(r.Context(), g.db, g.session.TenantID, shopID)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "catalog_unavailable", "the product directory is unavailable")
		return
	}
	items, total, err := browseCatalogSKUs(r.Context(), g.db, g.session.TenantID, shopID, businessDate, filter)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "catalog_unavailable", "the product directory is unavailable")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"items": items, "total": total, "page": filter.Page, "page_size": filter.PageSize,
		"business_date": businessDate,
		"notice":        "商品目录来自已发布的商品表；库存、订单与配送请咨询人工。",
	})
}
