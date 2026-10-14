package identity

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

type browserFixture struct {
	b                         *Browser
	server                    *httptest.Server
	nonce, challenge          string
	badNonce, changed, outage atomic.Bool
	refreshes                 atomic.Int64
	key                       *rsa.PrivateKey
	issuer                    string
}

func newBrowserFixture(t *testing.T) *browserFixture {
	t.Helper()
	f := &browserFixture{}
	f.key, _ = rsa.GenerateKey(rand.Reader, 2048)
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/realm/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": f.issuer, "authorization_endpoint": f.issuer + "/auth", "token_endpoint": f.issuer + "/token", "jwks_uri": f.issuer + "/keys", "introspection_endpoint": f.issuer + "/introspect", "end_session_endpoint": f.issuer + "/logout", "code_challenge_methods_supported": []string{"S256"}, "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/realm/keys":
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &f.key.PublicKey, KeyID: "fixture-key", Algorithm: "RS256", Use: "sig"}}})
		case "/realm/introspect":
			if f.outage.Load() {
				w.WriteHeader(503)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]bool{"active": true})
		case "/realm/token":
			user, password, ok := r.BasicAuth()
			if !ok || user != "fixture-browser" || password != "synthetic-client-secret" {
				t.Error("confidential token authentication missing")
				w.WriteHeader(401)
				return
			}
			_ = r.ParseForm()
			if r.Form.Get("grant_type") == "authorization_code" {
				hash := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
				if base64.RawURLEncoding.EncodeToString(hash[:]) != f.challenge || r.Form.Get("redirect_uri") != f.b.config.CallbackURL {
					t.Error("PKCE/callback mismatch")
					w.WriteHeader(400)
					return
				}
			} else {
				f.refreshes.Add(1)
			}
			claims := map[string]any{"iss": f.issuer, "sub": "subject-one", "aud": "fixture-browser", "azp": "fixture-browser", "exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Unix(), "typ": "Bearer", "tenant_id": "tenant-test", "user_id": "user-test", "role": "operator", "shop_ids": []string{"shop-test"}, "permission_revision": "1", "revoked": false}
			if f.changed.Load() {
				claims["permission_revision"] = "2"
			}
			access := f.sign(t, claims)
			claims["typ"] = "ID"
			claims["nonce"] = f.nonce
			if f.badNonce.Load() {
				claims["nonce"] = "other-nonce"
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": access, "id_token": f.sign(t, claims), "refresh_token": "synthetic-refresh", "token_type": "Bearer"})
		case "/realm/logout":
			if f.outage.Load() {
				w.WriteHeader(503)
				return
			}
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.server.Close)
	f.issuer = f.server.URL + "/realm"
	secret := filepath.Join(t.TempDir(), "secret")
	_ = os.WriteFile(secret, []byte("synthetic-client-secret"), 0600)
	p, err := New(context.Background(), Config{Issuer: f.issuer, ClientID: "fixture-browser", SecretFile: secret, Bindings: []Binding{{Subject: "subject-one", Tenant: "tenant-test", User: "user-test", Shop: "shop-test"}}, Browser: &BrowserConfig{CustomerOrigin: "http://127.0.0.1:5173", AdminOrigin: "http://127.0.0.1:5174", CallbackURL: "http://127.0.0.1:5173/v1/auth/callback", AllowLoopbackHTTP: true}})
	if err != nil {
		t.Fatal(err)
	}
	f.b, err = NewBrowser(p, func(context.Context, Principal) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *browserFixture) sign(t *testing.T, claims map[string]any) string {
	t.Helper()
	data, _ := json.Marshal(claims)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: f.key}, (&jose.SignerOptions{}).WithHeader("kid", "fixture-key"))
	if err != nil {
		t.Fatal(err)
	}
	signed, _ := signer.Sign(data)
	token, _ := signed.CompactSerialize()
	return token
}

func (f *browserFixture) login(t *testing.T) (*http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	out := httptest.NewRecorder()
	f.b.Handle(out, httptest.NewRequest("GET", "/v1/auth/login?app=customer&return_to=/chat", nil))
	if out.Code != 302 {
		t.Fatal("login failed")
	}
	u, _ := url.Parse(out.Header().Get("Location"))
	q := u.Query()
	f.nonce, f.challenge = q.Get("nonce"), q.Get("code_challenge")
	if q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" {
		t.Fatal("authorization flow mismatch")
	}
	req := httptest.NewRequest("GET", "/v1/auth/callback?code=synthetic-code&state="+q.Get("state"), nil)
	req.AddCookie(out.Result().Cookies()[0])
	return req, out
}

func (f *browserFixture) authenticated(t *testing.T) *http.Cookie {
	t.Helper()
	req, _ := f.login(t)
	out := httptest.NewRecorder()
	f.b.Handle(out, req)
	if out.Code != 303 {
		t.Fatalf("callback status %d", out.Code)
	}
	for _, c := range out.Result().Cookies() {
		if c.Name == sessionCookie {
			if !c.HttpOnly || c.Domain != "" || c.SameSite != http.SameSiteLaxMode {
				t.Fatal("unsafe cookie")
			}
			return c
		}
	}
	t.Fatal("session cookie missing")
	return nil
}

func TestBrowserCallbackBindsStateNonceAndOneUse(t *testing.T) {
	f := newBrowserFixture(t)
	req, _ := f.login(t)
	missing := httptest.NewRecorder()
	f.b.Handle(missing, httptest.NewRequest("GET", req.URL.String(), nil))
	if missing.Code != 401 {
		t.Fatal("missing state cookie accepted")
	}
	out := httptest.NewRecorder()
	f.b.Handle(out, req)
	if out.Code != 303 {
		t.Fatal("valid callback rejected")
	}
	replay := httptest.NewRecorder()
	f.b.Handle(replay, req)
	if replay.Code != 401 {
		t.Fatal("callback replay accepted")
	}
	f.badNonce.Store(true)
	req, _ = f.login(t)
	out = httptest.NewRecorder()
	f.b.Handle(out, req)
	if out.Code != 401 {
		t.Fatal("wrong nonce accepted")
	}
	req, _ = f.login(t)
	f.badNonce.Store(false)
	f.b.now = func() time.Time { return time.Now().Add(6 * time.Minute) }
	out = httptest.NewRecorder()
	f.b.Handle(out, req)
	if out.Code != 401 {
		t.Fatal("expired transaction accepted")
	}
}

func TestBrowserRejectsRedirectsAndPublicHTTP(t *testing.T) {
	f := newBrowserFixture(t)
	for _, path := range []string{"//other.example/chat", "https://other.example/chat", "/chat/../admin", "/chat?x=1", "/admin/overview"} {
		out := httptest.NewRecorder()
		f.b.Handle(out, httptest.NewRequest("GET", "/v1/auth/login?app=customer&return_to="+url.QueryEscape(path), nil))
		if out.Code != 401 {
			t.Fatal("unsafe redirect accepted")
		}
	}
	f.b.provider.config.Browser.CustomerOrigin = "http://192.0.2.1"
	if _, err := NewBrowser(f.b.provider, f.b.validate); err == nil {
		t.Fatal("public HTTP accepted")
	}
}

func TestBrowserRefreshSerializesAndPinsAuthority(t *testing.T) {
	f := newBrowserFixture(t)
	cookie := f.authenticated(t)
	req := httptest.NewRequest("GET", "/v1/session", nil)
	req.AddCookie(cookie)
	s := f.b.session(req)
	s.principal.Expires = time.Now().Add(10 * time.Second)
	var wait sync.WaitGroup
	for range 12 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := f.b.Resolve(req); err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
	if f.refreshes.Load() != 1 {
		t.Fatal("concurrent refresh duplicated")
	}
	f.changed.Store(true)
	s.principal.Expires = time.Now().Add(10 * time.Second)
	if _, err := f.b.Resolve(req); !errors.Is(err, ErrRevoked) {
		t.Fatal("refresh changed authority")
	}
	if _, err := f.b.Resolve(req); err == nil {
		t.Fatal("invalidated session survived")
	}
}

func TestBrowserCSRFLogoutAndScopeInvalidation(t *testing.T) {
	f := newBrowserFixture(t)
	cookie := f.authenticated(t)
	read := httptest.NewRequest("GET", "/v1/session", nil)
	read.AddCookie(cookie)
	principal, err := f.b.Resolve(read)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/v1/auth/logout", nil)
	req.AddCookie(cookie)
	if f.b.ValidateWrite(req) == nil {
		t.Fatal("missing CSRF accepted")
	}
	req.Header.Set("Origin", f.b.config.CustomerOrigin)
	req.Header.Set("X-CSRF-Token", f.b.session(read).csrf)
	req.Header.Set("Origin", "https://other.example")
	if f.b.ValidateWrite(req) == nil {
		t.Fatal("cross-origin CSRF accepted")
	}
	req.Header.Set("Origin", f.b.config.CustomerOrigin)
	f.outage.Store(true)
	out := httptest.NewRecorder()
	f.b.Handle(out, req)
	if out.Code != 503 {
		t.Fatal("upstream logout failure hidden")
	}
	if !errors.Is(principal.Check(context.Background()), ErrRevoked) {
		t.Fatal("old scope survived local logout")
	}
	if _, err = f.b.Resolve(read); err == nil {
		t.Fatal("logout session restored")
	}
}

func TestBrowserExpiryOutageAndMissingIdentity(t *testing.T) {
	f := newBrowserFixture(t)
	cookie := f.authenticated(t)
	req := httptest.NewRequest("GET", "/v1/session", nil)
	req.AddCookie(cookie)
	f.outage.Store(true)
	if _, err := f.b.Resolve(req); !errors.Is(err, ErrUnavailable) {
		t.Fatal("identity outage failed open")
	}
	f.outage.Store(false)
	req.Header.Set("Authorization", "Bearer synthetic")
	if _, err := f.b.Resolve(req); err == nil {
		t.Fatal("browser accepted bearer alternative")
	}
	req.Header.Del("Authorization")
	f.b.now = func() time.Time { return time.Now().Add(31 * time.Minute) }
	if _, err := f.b.Resolve(req); err == nil {
		t.Fatal("absolute lifetime extended")
	}
}
