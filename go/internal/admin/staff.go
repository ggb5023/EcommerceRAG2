package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"
	"github.com/ggb5023/EcommerceRAG/go/internal/localauth"
	"github.com/jackc/pgx/v5"
)

var staffUsername = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{2,63}$`)

// staffRoles are the only roles ecr_issue_admin_grant can approve. access_admin
// is out of band; offering it here would always fail with 42501.
var staffRoles = map[string]bool{"merchant_admin": true, "knowledge_reviewer": true}

// adminCreateStaff creates the account, its one-time credential, the pending
// permission request and the grant in a single transaction. The initial password
// is returned once and never logged.
func (g *Handler) adminCreateStaff(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant)
	if !ok {
		return
	}
	var in struct {
		Username string   `json:"username"`
		Role     string   `json:"role"`
		Shops    []string `json:"shop_ids"`
		Reason   string   `json:"reason"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 16*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	in.Reason = strings.TrimSpace(in.Reason)
	if !staffUsername.MatchString(in.Username) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_username", "username must be 3-64 characters of letters, digits, dot, dash or underscore")
		return
	}
	if !staffRoles[in.Role] {
		httpapi.WriteError(w, http.StatusBadRequest, "role_not_grantable",
			"only merchant_admin and knowledge_reviewer can be granted here; access_admin needs an out-of-band grant")
		return
	}
	if len(in.Reason) < 10 || len(in.Reason) > 2000 {
		httpapi.WriteError(w, http.StatusBadRequest, "reason_required", "a reason of 10-2000 characters is required")
		return
	}
	shops := in.Shops
	if len(shops) == 0 {
		shops = append([]string{}, scope.shops...)
	}
	if len(shops) == 0 {
		httpapi.WriteError(w, http.StatusBadRequest, "shop_scope_required", "at least one shop is required")
		return
	}
	for _, shop := range shops {
		if !contains(scope.shops, shop) {
			httpapi.WriteError(w, http.StatusForbidden, "admin_scope_denied", "shop is outside the current identity scope")
			return
		}
	}

	password, err := randomPassword()
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "staff_create_failed", "could not generate an initial password")
		return
	}
	hash, err := localauth.Hash(password)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "staff_create_failed", "could not hash the initial password")
		return
	}

	tx, err := g.DB.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "staff_create_failed", "could not start the account transaction")
		return
	}
	defer tx.Rollback(r.Context())

	var existing int64
	err = tx.QueryRow(r.Context(), `SELECT id FROM app_user WHERE tenant_id=$1 AND external_id=$2`,
		scope.TenantID, in.Username).Scan(&existing)
	if err == nil {
		httpapi.WriteError(w, http.StatusConflict, "username_exists", "an account with this username already exists")
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "staff_create_failed", "could not check the username")
		return
	}

	// The account and its one-time credential are written by localauth inside
	// this transaction; admin never issues those INSERTs itself.
	userID, err := localauth.CreateAccountTx(r.Context(), tx, localauth.AccountRecord{
		TenantID: scope.TenantID, ExternalID: in.Username, Role: "operator",
		ShopIDs: shops, PasswordHash: hash, MustChange: true,
	})
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "staff_create_failed", "could not create the account")
		return
	}

	canonical, _ := json.Marshal(map[string]any{"tenant_id": scope.TenantID, "user_id": userID,
		"role": in.Role, "scope_kind": "shops", "shop_ids": shops, "reason": in.Reason})
	sum := sha256.Sum256(canonical)
	var requestID int64
	if err = tx.QueryRow(r.Context(), `INSERT INTO admin_permission_request(tenant_id,requested_by,role,scope_kind,shop_ids,reason,payload_hash)
		VALUES($1,$2,$3,'shops',$4,$5,$6) RETURNING id`, scope.TenantID, userID, in.Role, shops,
		in.Reason, hex.EncodeToString(sum[:])).Scan(&requestID); err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "staff_create_failed", "could not create the permission request")
		return
	}
	var grantID int64
	if err = tx.QueryRow(r.Context(), `SELECT ecr_issue_admin_grant($1,$2,$3,'shops',$4,$5,$6,$7)`,
		scope.TenantID, userID, in.Role, shops, scope.UserID, requestID, in.Reason).Scan(&grantID); err != nil {
		httpapi.WriteError(w, http.StatusConflict, "role_not_grantable", "the administrative grant was refused")
		return
	}
	if err = auditAdmin(r.Context(), tx, scope.TenantID, scope.UserID, "staff.create", "app_user",
		strconv.FormatInt(userID, 10), "", "", 0, in.Reason, "success",
		map[string]any{"username": in.Username, "role": in.Role, "shop_ids": shops}); err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "staff_create_failed", "could not record the audit entry")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "staff_create_failed", "could not commit the account")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusCreated, map[string]any{
		"user_id": userID, "username": in.Username, "role": in.Role,
		"initial_password": password, "must_change_password": true,
	})
}

func randomPassword() (string, error) {
	buffer := make([]byte, 18)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
