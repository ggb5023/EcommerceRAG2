package identity

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

func TestOIDCSignatureClaimsAndLiveRevocation(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var active atomic.Bool
	active.Store(true)
	var unavailable atomic.Bool
	var calls atomic.Int64
	var issuer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/realm/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/auth", "token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys", "introspection_endpoint": issuer + "/introspect", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/realm/keys":
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "fixture-key", Algorithm: "RS256", Use: "sig"}}})
		case "/realm/introspect":
			calls.Add(1)
			user, password, ok := r.BasicAuth()
			if !ok || user != "fixture-client" || password != "synthetic-client-secret" {
				t.Error("introspection authentication missing")
				w.WriteHeader(401)
				return
			}
			if unavailable.Load() {
				w.WriteHeader(503)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"active": active.Load()})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	issuer = server.URL + "/realm"
	secret := filepath.Join(t.TempDir(), "client-secret")
	if err = os.WriteFile(secret, []byte("synthetic-client-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	provider, err := New(context.Background(), Config{Issuer: issuer, ClientID: "fixture-client", SecretFile: secret, Bindings: []Binding{{Subject: "subject-one", Tenant: "tenant-test", User: "user-test", Shop: "shop-test"}}})
	if err != nil {
		t.Fatal(err)
	}
	sign := func(change func(map[string]any), signingKey *rsa.PrivateKey) string {
		claims := map[string]any{"iss": issuer, "sub": "subject-one", "aud": "fixture-client", "azp": "fixture-client", "exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Unix(), "typ": "Bearer", "tenant_id": "tenant-test", "user_id": "user-test", "role": "operator", "shop_ids": []string{"shop-test"}, "permission_revision": "1", "revoked": false}
		change(claims)
		data, _ := json.Marshal(claims)
		signer, e := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: signingKey}, (&jose.SignerOptions{}).WithHeader("kid", "fixture-key"))
		if e != nil {
			t.Fatal(e)
		}
		signed, e := signer.Sign(data)
		if e != nil {
			t.Fatal(e)
		}
		token, e := signed.CompactSerialize()
		if e != nil {
			t.Fatal(e)
		}
		return token
	}
	good := sign(func(map[string]any) {}, key)
	principal, err := provider.Resolve(context.Background(), good)
	if err != nil || principal.Binding.User != "user-test" {
		t.Fatalf("valid identity: %v", err)
	}
	for name, change := range map[string]func(map[string]any){
		"issuer":             func(c map[string]any) { c["iss"] = issuer + "-other" },
		"audience":           func(c map[string]any) { c["aud"] = "other-client" },
		"expiry":             func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() },
		"future_nbf":         func(c map[string]any) { c["nbf"] = time.Now().Add(time.Minute).Unix() },
		"subject":            func(c map[string]any) { c["sub"] = "unmapped-subject" },
		"tenant":             func(c map[string]any) { c["tenant_id"] = "other-tenant" },
		"shop":               func(c map[string]any) { c["shop_ids"] = []string{"other-shop"} },
		"id_token":           func(c map[string]any) { c["typ"] = "ID" },
		"missing_revocation": func(c map[string]any) { delete(c, "revoked") },
	} {
		t.Run(name, func(t *testing.T) {
			before := calls.Load()
			_, e := provider.Resolve(context.Background(), sign(change, key))
			if !errors.Is(e, ErrInvalid) {
				t.Fatalf("accepted invalid claims: %v", e)
			}
			if calls.Load() != before {
				t.Fatal("invalid JWT reached introspection")
			}
		})
	}
	wrong, _ := rsa.GenerateKey(rand.Reader, 2048)
	if _, err = provider.Resolve(context.Background(), sign(func(map[string]any) {}, wrong)); !errors.Is(err, ErrInvalid) {
		t.Fatal("wrong signature accepted")
	}
	active.Store(false)
	if !errors.Is(principal.Check(context.Background()), ErrRevoked) {
		t.Fatal("existing principal survived live revocation")
	}
	active.Store(true)
	unavailable.Store(true)
	if !errors.Is(principal.Check(context.Background()), ErrUnavailable) {
		t.Fatal("introspection outage failed open")
	}
}

func TestProtectedOIDCConfiguration(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(p, []byte(`{"issuer":"http://127.0.0.1/realm"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(p); err == nil {
		t.Fatal("public configuration accepted")
	}
	if err := os.Chmod(p, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(p); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"unknown":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(p); err == nil {
		t.Fatal("unknown configuration accepted")
	}
}
