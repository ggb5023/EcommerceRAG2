package localauth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const SessionCookie = "ecr_v2_session"

// DeriveCSRF builds a stable per-session token from the cookie and the gateway
// signing key, so a restart can still answer /v1/auth/session without storing
// the raw token anywhere.
func DeriveCSRF(key []byte, cookie string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("local-csrf:"))
	mac.Write([]byte(cookie))
	return hex.EncodeToString(mac.Sum(nil))
}

// SubjectHash keeps usernames out of the audit table.
func SubjectHash(username string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(username))))
	return hex.EncodeToString(sum[:])
}

// Handler serves /v1/auth/* for the local profile.
type Handler struct {
	Store           *Store
	Limiter         *Limiter
	Key             []byte
	Origins         []string
	Secure          func(*http.Request) bool
	LockoutAttempts int
	LockoutFor      time.Duration
}

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/auth/session":
		h.sessionState(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/login":
		h.login(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/logout":
		h.logout(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/password":
		h.changePassword(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/auth/login":
		// The local profile has no provider redirect: send the browser to the
		// login page, preserving the app and return_to parameters.
		target := "/login"
		if query := r.URL.RawQuery; query != "" {
			target += "?" + query
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) sessionState(w http.ResponseWriter, r *http.Request) {
	session, cookie, err := h.resolve(r)
	if err != nil {
		if errors.Is(err, ErrAccountDisabled) {
			writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "authenticated": false,
				"mode": "local", "account_revoked": true})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "authenticated": false, "mode": "local"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": true, "authenticated": true, "mode": "local",
		"csrf_token":           DeriveCSRF(h.Key, cookie),
		"must_change_password": session.MustChangePassword,
	})
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if ok, wait := h.Limiter.Allow(ip); !ok {
		_ = h.event(r.Context(), "rate_limited", "", nil, nil, ip, "")
		w.Header().Set("Retry-After", strings.TrimSuffix(wait.Round(time.Second).String(), "s"))
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limited"})
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &in); err != nil || in.Username == "" || len(in.Password) > 256 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	h.Limiter.Record(ip)
	account, err := h.Store.FindAccount(r.Context(), in.Username)
	if err != nil {
		_ = DummyVerify(in.Password)
		_ = h.event(r.Context(), "login_failed", SubjectHash(in.Username), nil, nil, ip, `{"source":"login"}`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_credentials"})
		return
	}
	now := h.Store.now()
	if account.LockedUntil != nil && account.LockedUntil.After(now) {
		if h.Limiter.ProbeExceeded(ip) {
			_ = h.event(r.Context(), "rate_limited", "", &account.TenantID, &account.UserID, ip, "")
			w.Header().Set("Retry-After", "900")
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limited"})
			return
		}
		w.Header().Set("Retry-After", strings.TrimSuffix(time.Until(*account.LockedUntil).Round(time.Second).String(), "s"))
		writeJSON(w, http.StatusLocked, map[string]string{"error": "account_locked"})
		return
	}
	if err := Verify(in.Password, account.PasswordHash); err != nil {
		// Password first: a wrong password never learns whether the account is
		// disabled, only a correct one does.
		_, lockedUntil, _ := h.Store.RegisterFailure(r.Context(), account.TenantID, account.UserID, h.LockoutAttempts, h.LockoutFor)
		_ = h.event(r.Context(), "login_failed", SubjectHash(in.Username), &account.TenantID, &account.UserID, ip, "")
		if lockedUntil != nil {
			_ = h.event(r.Context(), "account_locked", "", &account.TenantID, &account.UserID, ip, "")
			_, _ = h.Store.RevokeUserSessions(r.Context(), account.TenantID, account.UserID, "lockout")
			w.Header().Set("Retry-After", "900")
			writeJSON(w, http.StatusLocked, map[string]string{"error": "account_locked"})
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_credentials"})
		return
	}
	_ = h.Store.ClearFailures(r.Context(), account.TenantID, account.UserID)
	if account.RevokedAt != nil {
		_ = h.event(r.Context(), "login_failed", SubjectHash(in.Username), &account.TenantID, &account.UserID, ip,
			`{"source":"login","reason":"account_revoked"}`)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "account_revoked"})
		return
	}
	session, cookie, _, err := NewSession(account.TenantID, account.UserID, account.MustChangePassword, now)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "session_unavailable"})
		return
	}
	session.CSRFHash = HashToken(DeriveCSRF(h.Key, cookie))
	if err := h.Store.CreateSession(r.Context(), session); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "session_unavailable"})
		return
	}
	_ = h.event(r.Context(), "login_succeeded", "", &account.TenantID, &account.UserID, ip, "")
	h.setCookie(w, r, cookie)
	writeJSON(w, http.StatusOK, map[string]any{
		"mode": "local", "authenticated": true,
		"must_change_password": account.MustChangePassword,
		"csrf_token":           DeriveCSRF(h.Key, cookie),
	})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	session, cookie, err := h.resolve(r)
	if err == nil && h.validWrite(r, cookie, session) {
		_ = h.Store.RevokeSession(r.Context(), session.IDHash, "logout")
	}
	h.clearCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	session, cookie, err := h.resolve(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !h.validWrite(r, cookie, session) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "csrf_invalid"})
		return
	}
	var in struct {
		Current string `json:"current_password"`
		Next    string `json:"new_password"`
	}
	if err := decodeJSON(r, &in); err != nil || len(in.Next) < 12 || len(in.Next) > 256 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	account, err := h.Store.FindAccountByID(r.Context(), session.TenantID, session.UserID)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if err := Verify(in.Current, account.PasswordHash); err != nil {
		ip := clientIP(r)
		_, lockedUntil, _ := h.Store.RegisterFailure(r.Context(), session.TenantID, session.UserID, h.LockoutAttempts, h.LockoutFor)
		_ = h.event(r.Context(), "login_failed", SubjectHash(account.ExternalID), &session.TenantID, &session.UserID, ip,
			`{"source":"password_change"}`)
		if lockedUntil != nil {
			_ = h.event(r.Context(), "account_locked", "", &session.TenantID, &session.UserID, ip, `{"source":"password_change"}`)
			_, _ = h.Store.RevokeUserSessions(r.Context(), session.TenantID, session.UserID, "lockout")
			h.clearCookie(w, r)
			w.Header().Set("Retry-After", "900")
			writeJSON(w, http.StatusLocked, map[string]string{"error": "account_locked"})
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_credentials"})
		return
	}
	hash, err := Hash(in.Next)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "hash_unavailable"})
		return
	}
	if _, err := h.Store.ChangePassword(r.Context(), session.TenantID, session.UserID, hash); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "password_unavailable"})
		return
	}
	h.clearCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"changed": true, "relogin": true})
}

// ValidateWrite is the local counterpart of the OIDC browser check.
func (h *Handler) ValidateWrite(r *http.Request) error {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return nil
	}
	session, cookie, err := h.resolve(r)
	if err != nil {
		return err
	}
	if !h.validWrite(r, cookie, session) {
		return errors.New("localauth: csrf rejected")
	}
	return nil
}

func (h *Handler) validWrite(r *http.Request, cookie string, session ResolvedSession) bool {
	origin := r.Header.Get("Origin")
	allowed := false
	for _, candidate := range h.Origins {
		if candidate != "" && candidate == origin {
			allowed = true
			break
		}
	}
	if !allowed {
		return false
	}
	token := r.Header.Get("X-CSRF-Token")
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(HashToken(token)), []byte(session.CSRFHash)) == 1
}

func (h *Handler) resolve(r *http.Request) (ResolvedSession, string, error) {
	cookie, err := r.Cookie(SessionCookie)
	if err != nil || ValidateTokenShape(cookie.Value) != nil {
		return ResolvedSession{}, "", ErrSessionInvalid
	}
	session, err := h.Store.ResolveSession(r.Context(), HashToken(cookie.Value))
	if err != nil {
		return ResolvedSession{}, "", err
	}
	_ = h.Store.TouchSession(r.Context(), session.IDHash, session.NextIdleExpiry(h.Store.now()))
	return session, cookie.Value, nil
}

func (h *Handler) setCookie(w http.ResponseWriter, r *http.Request, value string) {
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: value, Path: "/", HttpOnly: true,
		Secure: h.Secure != nil && h.Secure(r), SameSite: http.SameSiteLaxMode, MaxAge: int(AbsoluteTTL.Seconds())})
}

func (h *Handler) clearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", HttpOnly: true,
		Secure: h.Secure != nil && h.Secure(r), SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

func (h *Handler) event(ctx context.Context, name, subject string, tenantID, userID *int64, ip, detail string) error {
	return h.Store.RecordEvent(ctx, Event{Name: name, SubjectHash: subject, TenantID: tenantID, UserID: userID, IP: ip, Detail: detail})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 64*1024))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
