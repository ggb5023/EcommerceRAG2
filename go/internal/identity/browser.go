package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

const sessionCookie = "ecr_v2_session"
const stateCookie = "ecr_v2_oidc_state"
const browserCapacity = 4096

var conversationPathID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

type BrowserConfig struct {
	CustomerOrigin    string `json:"customer_origin"`
	AdminOrigin       string `json:"admin_origin"`
	CallbackURL       string `json:"callback_url"`
	AllowLoopbackHTTP bool   `json:"allow_loopback_http"`
}

type loginTransaction struct {
	nonce, verifier, destination string
	expires                      time.Time
}

type browserSession struct {
	mu            sync.Mutex
	principal     Principal
	refresh, csrf string
	expires       time.Time
	revoked       atomic.Bool
}

// Browser stores credentials only in the owning Go process. It is deliberately
// not a distributed or persistent session store.
type Browser struct {
	BindAuthority                func(context.Context, Principal) (Principal, error)
	provider                     *Provider
	config                       BrowserConfig
	validate                     func(context.Context, Principal) error
	authorization, token, logout string
	secure                       bool
	mu                           sync.Mutex
	transactions                 map[string]loginTransaction
	sessions                     map[string]*browserSession
	now                          func() time.Time
}

func origin(raw string, allowHTTP bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.Opaque != "" {
		return nil, ErrInvalid
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(allowHTTP && u.Scheme == "http" && ip != nil && ip.IsLoopback()) {
		return nil, ErrInvalid
	}
	return u, nil
}

func NewBrowser(p *Provider, validate func(context.Context, Principal) error) (*Browser, error) {
	if p.config.Browser == nil || validate == nil {
		return nil, ErrInvalid
	}
	c := *p.config.Browser
	customer, err := origin(c.CustomerOrigin, c.AllowLoopbackHTTP)
	if err != nil {
		return nil, err
	}
	admin, err := origin(c.AdminOrigin, c.AllowLoopbackHTTP)
	if err != nil {
		return nil, err
	}
	// A shared host-only cookie serves the two development frontends. Different
	// hosts would need a different session-delivery design, never a Domain cookie.
	if customer.Hostname() != admin.Hostname() || customer.Scheme != admin.Scheme {
		return nil, ErrInvalid
	}
	callback, err := url.Parse(c.CallbackURL)
	if err != nil || callback.User != nil || callback.RawQuery != "" || callback.Fragment != "" || callback.Path != "/v1/auth/callback" || callback.Scheme+"://"+callback.Host != c.CustomerOrigin {
		return nil, ErrInvalid
	}
	var d struct {
		Authorization string   `json:"authorization_endpoint"`
		Token         string   `json:"token_endpoint"`
		Logout        string   `json:"end_session_endpoint"`
		PKCE          []string `json:"code_challenge_methods_supported"`
	}
	if p.discovery.Claims(&d) != nil || !slices.Contains(d.PKCE, "S256") {
		return nil, ErrInvalid
	}
	issuer, _ := url.Parse(p.config.Issuer)
	for _, raw := range []string{d.Authorization, d.Token, d.Logout} {
		u, e := url.Parse(raw)
		if e != nil || u.Scheme != issuer.Scheme || u.Host != issuer.Host || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, issuer.Path+"/") {
			return nil, ErrInvalid
		}
	}
	return &Browser{provider: p, config: c, validate: validate, authorization: d.Authorization, token: d.Token, logout: d.Logout, secure: customer.Scheme == "https", transactions: make(map[string]loginTransaction), sessions: make(map[string]*browserSession), now: time.Now}, nil
}

func randomID() string {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic("secure random unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(bytes[:])
}

func (b *Browser) cookie(w http.ResponseWriter, name, value string, seconds int) {
	path := "/"
	if name == stateCookie {
		path = "/v1/auth"
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: path, HttpOnly: true, Secure: b.secure, SameSite: http.SameSiteLaxMode, MaxAge: seconds})
}

func uniqueCookie(r *http.Request, name string) string {
	value, count := "", 0
	for _, c := range r.Cookies() {
		if c.Name == name {
			value, count = c.Value, count+1
		}
	}
	if count != 1 || len(value) != 43 {
		return ""
	}
	return value
}

func (b *Browser) cleanupLocked() {
	now := b.now()
	for id, t := range b.transactions {
		if !now.Before(t.expires) {
			delete(b.transactions, id)
		}
	}
	for id, s := range b.sessions {
		if !now.Before(s.expires) || s.revoked.Load() {
			s.revoked.Store(true)
			delete(b.sessions, id)
		}
	}
}

func (b *Browser) destination(app, path string) (string, error) {
	if path == "" {
		if app == "admin" {
			path = "/admin/overview"
		} else {
			path = "/chat"
		}
	}
	u, err := url.Parse(path)
	if err != nil || u.IsAbs() || u.Host != "" || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || strings.ContainsAny(path, "\\\r\n") {
		return "", ErrInvalid
	}
	if app == "customer" && (path == "/support" || (strings.HasPrefix(path, "/support/") && conversationPathID.MatchString(strings.TrimPrefix(path, "/support/"))) || path == "/chat" || (strings.HasPrefix(path, "/chat/") && conversationPathID.MatchString(strings.TrimPrefix(path, "/chat/")))) {
		return b.config.CustomerOrigin + path, nil
	}
	if app == "admin" && slices.Contains([]string{"/admin/overview", "/admin/access/accounts", "/admin/access/requests", "/admin/access/audit", "/admin/merchant/shops", "/admin/merchant/sources", "/admin/merchant/ingestion", "/admin/merchant/reviews"}, path) {
		return b.config.AdminOrigin + path, nil
	}
	return "", ErrInvalid
}

func authError(w http.ResponseWriter, err error) {
	code, label := 401, "identity_invalid"
	if errors.Is(err, ErrUnavailable) {
		code, label = 503, "identity_unavailable"
	} else if errors.Is(err, ErrRevoked) {
		code, label = 403, "identity_revoked"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": label, "message": "identity request failed"})
}

func (b *Browser) Handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	switch r.Method + " " + r.URL.Path {
	case "GET /v1/auth/login":
		b.login(w, r)
	case "GET /v1/auth/callback":
		b.callback(w, r)
	case "GET /v1/auth/session":
		_, err := b.Resolve(r)
		if err != nil && !errors.Is(err, ErrInvalid) {
			authError(w, err)
			return
		}
		result := map[string]any{"enabled": true, "authenticated": err == nil}
		if err == nil {
			s := b.session(r)
			if s == nil {
				authError(w, ErrInvalid)
				return
			}
			result["csrf_token"] = s.csrf
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	case "POST /v1/auth/logout":
		b.signout(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (b *Browser) login(w http.ResponseWriter, r *http.Request) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q["app"]) != 1 || len(q["return_to"]) > 1 {
		authError(w, ErrInvalid)
		return
	}
	destination, err := b.destination(q.Get("app"), q.Get("return_to"))
	if err != nil {
		authError(w, err)
		return
	}
	state, nonce, verifier := randomID(), randomID(), randomID()
	b.mu.Lock()
	b.cleanupLocked()
	if len(b.transactions) >= browserCapacity {
		b.mu.Unlock()
		authError(w, ErrUnavailable)
		return
	}
	b.transactions[state] = loginTransaction{nonce, verifier, destination, b.now().Add(5 * time.Minute)}
	b.mu.Unlock()
	b.cookie(w, stateCookie, state, 300)
	hash := sha256.Sum256([]byte(verifier))
	u, _ := url.Parse(b.authorization)
	u.RawQuery = url.Values{"client_id": {b.provider.config.ClientID}, "redirect_uri": {b.config.CallbackURL}, "response_type": {"code"}, "scope": {"openid"}, "state": {state}, "nonce": {nonce}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "code_challenge_method": {"S256"}}.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

type browserTokens struct {
	Access  string `json:"access_token"`
	Refresh string `json:"refresh_token"`
	ID      string `json:"id_token"`
	Type    string `json:"token_type"`
}

func (b *Browser) exchange(ctx context.Context, values url.Values) (browserTokens, error) {
	var tokens browserTokens
	values.Set("client_id", b.provider.config.ClientID)
	req, err := http.NewRequestWithContext(ctx, "POST", b.token, strings.NewReader(values.Encode()))
	if err != nil {
		return tokens, ErrInvalid
	}
	req.SetBasicAuth(b.provider.config.ClientID, b.provider.secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := b.provider.client.Do(req)
	if err != nil {
		return tokens, ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		if response.StatusCode >= 500 || response.StatusCode == 429 {
			return tokens, ErrUnavailable
		}
		return tokens, ErrInvalid
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(data) > 65536 || json.Unmarshal(data, &tokens) != nil || tokens.Access == "" || tokens.Refresh == "" || !strings.EqualFold(tokens.Type, "bearer") {
		return browserTokens{}, ErrInvalid
	}
	return tokens, nil
}

func (b *Browser) idToken(ctx context.Context, tokens browserTokens, nonce, subject string) error {
	if tokens.ID == "" {
		return ErrInvalid
	}
	token, err := b.provider.verifier.Verify(oidc.ClientContext(ctx, b.provider.client), tokens.ID)
	if err != nil || token.Subject != subject || (nonce != "" && token.Nonce != nonce) || (token.AccessTokenHash != "" && token.VerifyAccessToken(tokens.Access) != nil) {
		return ErrInvalid
	}
	return nil
}

func (b *Browser) callback(w http.ResponseWriter, r *http.Request) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	state := q.Get("state")
	if err != nil || len(q["state"]) != 1 || uniqueCookie(r, stateCookie) == "" || subtle.ConstantTimeCompare([]byte(state), []byte(uniqueCookie(r, stateCookie))) != 1 {
		authError(w, ErrInvalid)
		return
	}
	b.mu.Lock()
	transaction, found := b.transactions[state]
	delete(b.transactions, state)
	b.mu.Unlock()
	b.cookie(w, stateCookie, "", -1)
	if !found || !b.now().Before(transaction.expires) || len(q["code"]) != 1 || len(q.Get("code")) > 4096 || q.Get("code") == "" || q.Get("error") != "" {
		authError(w, ErrInvalid)
		return
	}
	tokens, err := b.exchange(r.Context(), url.Values{"grant_type": {"authorization_code"}, "code": {q.Get("code")}, "redirect_uri": {b.config.CallbackURL}, "code_verifier": {transaction.verifier}})
	if err != nil {
		authError(w, err)
		return
	}
	principal, err := b.provider.Resolve(r.Context(), tokens.Access)
	if err == nil && b.BindAuthority != nil {
		principal, err = b.BindAuthority(r.Context(), principal)
	}
	if err == nil {
		err = b.idToken(r.Context(), tokens, transaction.nonce, principal.Binding.Subject)
	}
	if err == nil {
		err = b.validate(r.Context(), principal)
	}
	if err != nil {
		authError(w, err)
		return
	}
	id := randomID()
	s := &browserSession{principal: principal, refresh: tokens.Refresh, csrf: randomID(), expires: b.now().Add(30 * time.Minute)}
	b.mu.Lock()
	b.cleanupLocked()
	if len(b.sessions) >= browserCapacity {
		b.mu.Unlock()
		authError(w, ErrUnavailable)
		return
	}
	if old := b.sessions[uniqueCookie(r, sessionCookie)]; old != nil {
		old.revoked.Store(true)
		delete(b.sessions, uniqueCookie(r, sessionCookie))
	}
	b.sessions[id] = s
	b.mu.Unlock()
	b.cookie(w, sessionCookie, id, 1800)
	http.Redirect(w, r, transaction.destination, http.StatusSeeOther)
}

func (b *Browser) session(r *http.Request) *browserSession {
	id := uniqueCookie(r, sessionCookie)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cleanupLocked()
	return b.sessions[id]
}

func sameAuthority(a, c Principal) bool {
	x, y := slices.Clone(a.Claims.Shops), slices.Clone(c.Claims.Shops)
	slices.Sort(x)
	slices.Sort(y)
	return a.Binding == c.Binding && a.Claims.Role == c.Claims.Role && a.Claims.Revision == c.Claims.Revision && slices.Equal(x, y)
}

func (b *Browser) Resolve(r *http.Request) (Principal, error) {
	var result Principal
	if len(r.Header.Values("Authorization")) != 0 {
		return result, ErrInvalid
	}
	s := b.session(r)
	if s == nil {
		return result, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revoked.Load() || !b.now().Before(s.expires) {
		return result, ErrInvalid
	}
	if err := s.principal.Check(r.Context()); err != nil {
		// Expiry is handled by refresh below; live revocation and outages are
		// never repaired by refreshing a still-valid token.
		if b.now().Before(s.principal.Expires) {
			if !errors.Is(err, ErrUnavailable) {
				s.revoked.Store(true)
			}
			return result, err
		}
	}
	if !b.now().Add(30 * time.Second).Before(s.principal.Expires) {
		tokens, err := b.exchange(r.Context(), url.Values{"grant_type": {"refresh_token"}, "refresh_token": {s.refresh}})
		if err != nil {
			if !errors.Is(err, ErrUnavailable) {
				s.revoked.Store(true)
			}
			return result, err
		}
		principal, err := b.provider.Resolve(r.Context(), tokens.Access)
		if err == nil && b.BindAuthority != nil {
			principal, err = b.BindAuthority(r.Context(), principal)
		}
		if err == nil && !sameAuthority(s.principal, principal) {
			err = ErrRevoked
		}
		if err == nil && tokens.ID != "" {
			err = b.idToken(r.Context(), tokens, "", principal.Binding.Subject)
		}
		if err == nil {
			err = b.validate(r.Context(), principal)
		}
		if err != nil {
			if !errors.Is(err, ErrUnavailable) {
				s.revoked.Store(true)
			}
			return result, err
		}
		s.principal, s.refresh = principal, tokens.Refresh
	}
	if err := b.validate(r.Context(), s.principal); err != nil {
		if !errors.Is(err, ErrUnavailable) {
			s.revoked.Store(true)
		}
		return result, err
	}
	result = s.principal
	if s.revoked.Load() {
		return Principal{}, ErrRevoked
	}
	check := result.Check
	result.Check = func(ctx context.Context) error {
		if s.revoked.Load() || !b.now().Before(s.expires) {
			return ErrRevoked
		}
		if err := check(ctx); err != nil {
			return err
		}
		if s.revoked.Load() || !b.now().Before(s.expires) {
			return ErrRevoked
		}
		return nil
	}
	return result, nil
}

func (b *Browser) ValidateWrite(r *http.Request) error {
	if r.Method == "GET" || r.Method == "HEAD" || r.Method == "OPTIONS" {
		return nil
	}
	s := b.session(r)
	if s == nil {
		return ErrInvalid
	}
	origins, tokens := r.Header.Values("Origin"), r.Header.Values("X-CSRF-Token")
	if len(origins) != 1 || (origins[0] != b.config.CustomerOrigin && origins[0] != b.config.AdminOrigin) || len(tokens) != 1 || subtle.ConstantTimeCompare([]byte(tokens[0]), []byte(s.csrf)) != 1 {
		return ErrRevoked
	}
	return nil
}

func (b *Browser) signout(w http.ResponseWriter, r *http.Request) {
	if err := b.ValidateWrite(r); err != nil {
		authError(w, err)
		return
	}
	s := b.session(r)
	if s == nil {
		authError(w, ErrInvalid)
		return
	}
	s.revoked.Store(true)
	b.mu.Lock()
	delete(b.sessions, uniqueCookie(r, sessionCookie))
	b.mu.Unlock()
	b.cookie(w, sessionCookie, "", -1)
	s.mu.Lock()
	refresh := s.refresh
	s.refresh = ""
	s.mu.Unlock()
	req, err := http.NewRequestWithContext(r.Context(), "POST", b.logout, strings.NewReader(url.Values{"client_id": {b.provider.config.ClientID}, "refresh_token": {refresh}}.Encode()))
	if err != nil {
		authError(w, ErrUnavailable)
		return
	}
	req.SetBasicAuth(b.provider.config.ClientID, b.provider.secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := b.provider.client.Do(req)
	if err != nil {
		authError(w, ErrUnavailable)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 204 && response.StatusCode != 200 {
		authError(w, ErrUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
