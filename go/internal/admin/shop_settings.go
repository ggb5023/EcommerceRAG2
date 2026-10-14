package admin

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"
)

var publicSlug = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

func (g *Handler) adminShopSettings(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !contains(scope.shops, id) {
		httpapi.WriteError(w, 403, "shop_forbidden", "shop is outside permission scope")
		return
	}
	var in struct {
		Name     string `json:"name"`
		Slug     string `json:"public_slug"`
		Welcome  string `json:"welcome_message"`
		Enabled  bool   `json:"customer_chat_enabled"`
		Revision string `json:"permission_revision"`
	}
	if httpapi.DecodeJSON(r, &in) != nil || strings.TrimSpace(in.Name) == "" || len(in.Name) > 200 || len(in.Welcome) > 2000 || !publicSlug.MatchString(in.Slug) {
		httpapi.WriteError(w, 400, "invalid_shop_settings", "name, public slug and bounded welcome message required")
		return
	}
	tx, err := g.DB.Begin(r.Context())
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "settings not saved")
		return
	}
	defer tx.Rollback(r.Context())
	if !g.adminGrantInTx(r.Context(), tx, adminRoleMerchant, id) || in.Revision != g.Authority.CurrentRevision() {
		httpapi.WriteError(w, 409, "permission_changed", "reload current permissions")
		return
	}
	result, err := tx.Exec(r.Context(), `UPDATE shop SET name=$3,public_slug=$4,welcome_message=$5,customer_chat_enabled=$6 WHERE tenant_id=$1 AND id=$2`, g.Session.TenantID, id, strings.TrimSpace(in.Name), in.Slug, in.Welcome, in.Enabled)
	if err != nil || result.RowsAffected() != 1 {
		httpapi.WriteError(w, 409, "shop_settings_conflict", "public slug unavailable or shop changed")
		return
	}
	if auditAdmin(r.Context(), tx, g.Session.TenantID, g.Session.UserID, "shop_settings", "shop", id, "", "", 0, "merchant configured customer channel", "applied", map[string]any{"customer_chat_enabled": in.Enabled}) != nil || tx.Commit(r.Context()) != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "settings not confirmed")
		return
	}
	httpapi.WriteJSON(w, 200, map[string]any{"updated": true})
}
