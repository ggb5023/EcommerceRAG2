// Package identity verifies local OIDC credentials without granting business permissions.
package identity

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

var (
	ErrInvalid     = errors.New("identity_invalid")
	ErrRevoked     = errors.New("identity_revoked")
	ErrUnavailable = errors.New("identity_unavailable")
)

// Bindings are provisioned by the operator, never derived from browser input.
type Binding struct {
	Subject string `json:"subject"`
	Tenant  string `json:"tenant"`
	User    string `json:"user"`
	Shop    string `json:"shop"`
}

type Config struct {
	AuthorizationSource string         `json:"authorization_source,omitempty"`
	Issuer              string         `json:"issuer"`
	ClientID            string         `json:"client_id"`
	SecretFile          string         `json:"secret_file"`
	Bindings            []Binding      `json:"bindings"`
	Browser             *BrowserConfig `json:"browser,omitempty"`
}

func protectedFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, ErrInvalid
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || owner.Uid != uint32(os.Geteuid()) || info.Size() > 65536 {
		return nil, ErrInvalid
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, ErrInvalid
	}
	return data, nil
}

func LoadConfig(path string) (Config, error) {
	var config Config
	data, err := protectedFile(path)
	if err != nil {
		return config, err
	}
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if d.Decode(&config) != nil || d.Decode(new(any)) != io.EOF {
		return config, ErrInvalid
	}
	return config, nil
}

type Claims struct {
	Tenant    string   `json:"tenant_id"`
	User      string   `json:"user_id"`
	Role      string   `json:"role"`
	Shops     []string `json:"shop_ids"`
	Revision  string   `json:"permission_revision"`
	Revoked   *bool    `json:"revoked"`
	Type      string   `json:"typ"`
	Party     string   `json:"azp"`
	NotBefore int64    `json:"nbf"`
}

type Principal struct {
	DatabaseAuthorization bool
	Binding               Binding
	Claims                Claims
	Expires               time.Time
	Check                 func(context.Context) error
}

type Provider struct {
	verifier      *oidc.IDTokenVerifier
	client        *http.Client
	config        Config
	secret        string
	introspection string
	bindings      map[string]Binding
	discovery     *oidc.Provider
}

func New(ctx context.Context, config Config) (*Provider, error) {
	u, err := url.Parse(config.Issuer)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.HasSuffix(config.Issuer, "/") {
		return nil, ErrInvalid
	}
	local := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && local != nil && local.IsLoopback()) {
		return nil, ErrInvalid
	}
	if config.AuthorizationSource != "" && config.AuthorizationSource != "token" && config.AuthorizationSource != "database" {
		return nil, ErrInvalid
	}
	if config.ClientID == "" || len(config.Bindings) == 0 {
		return nil, ErrInvalid
	}
	secret, err := protectedFile(config.SecretFile)
	if err != nil || strings.TrimSpace(string(secret)) == "" {
		return nil, ErrInvalid
	}
	bindings := make(map[string]Binding)
	for _, b := range config.Bindings {
		if b.Subject == "" || b.Tenant == "" || b.User == "" || b.Shop == "" {
			return nil, ErrInvalid
		}
		if _, found := bindings[b.Subject]; found {
			return nil, ErrInvalid
		}
		bindings[b.Subject] = b
	}
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, client), config.Issuer)
	if err != nil {
		return nil, ErrUnavailable
	}
	var discovery struct {
		Introspection string `json:"introspection_endpoint"`
		JWKS          string `json:"jwks_uri"`
	}
	if provider.Claims(&discovery) != nil {
		return nil, ErrInvalid
	}
	for _, endpoint := range []string{discovery.Introspection, discovery.JWKS} {
		e, parseErr := url.Parse(endpoint)
		if parseErr != nil || e.Scheme != u.Scheme || e.Host != u.Host || e.User != nil || e.RawQuery != "" || e.Fragment != "" || !strings.HasPrefix(e.Path, u.Path+"/") {
			return nil, ErrInvalid
		}
	}
	return &Provider{verifier: provider.Verifier(&oidc.Config{ClientID: config.ClientID, SupportedSigningAlgs: []string{"RS256"}}), client: client, config: config, secret: strings.TrimSpace(string(secret)), introspection: discovery.Introspection, bindings: bindings, discovery: provider}, nil
}

func (p *Provider) Resolve(ctx context.Context, bearer string) (Principal, error) {
	var principal Principal
	if bearer == "" || len(bearer) > 16384 {
		return principal, ErrInvalid
	}
	token, err := p.verifier.Verify(oidc.ClientContext(ctx, p.client), bearer)
	if err != nil {
		return principal, ErrInvalid
	}
	var claims Claims
	if token.Claims(&claims) != nil || token.Subject == "" || claims.Type != "Bearer" || claims.Party != p.config.ClientID || claims.NotBefore > time.Now().Unix() {
		return principal, ErrInvalid
	}
	if claims.Revoked != nil && *claims.Revoked {
		return principal, ErrRevoked
	}
	binding, found := p.bindings[token.Subject]
	if !found {
		return principal, ErrInvalid
	}
	if p.config.AuthorizationSource != "database" && (claims.Tenant != binding.Tenant || claims.User != binding.User || claims.Revoked == nil || claims.Revision == "" || claims.Role == "" || len(claims.Shops) == 0) {
		return principal, ErrInvalid
	}
	seen, shopFound := make(map[string]bool), false
	for _, shop := range claims.Shops {
		if shop == "" || seen[shop] {
			return principal, ErrInvalid
		}
		seen[shop] = true
		shopFound = shopFound || shop == binding.Shop
	}
	if p.config.AuthorizationSource != "database" && !shopFound {
		return principal, ErrInvalid
	}
	if p.config.AuthorizationSource == "database" {
		claims.Role = ""
		claims.Shops = nil
		claims.Revision = ""
	}
	check := func(ctx context.Context) error {
		if !time.Now().Before(token.Expiry) {
			return ErrInvalid
		}
		return p.active(ctx, bearer)
	}
	if err = check(ctx); err != nil {
		return principal, err
	}
	return Principal{DatabaseAuthorization: p.config.AuthorizationSource == "database", Binding: binding, Claims: claims, Expires: token.Expiry, Check: check}, nil
}

func (p *Provider) active(ctx context.Context, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.introspection, strings.NewReader(url.Values{"token": {token}, "token_type_hint": {"access_token"}}.Encode()))
	if err != nil {
		return ErrInvalid
	}
	req.SetBasicAuth(p.config.ClientID, p.secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := p.client.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(data) > 65536 {
		return ErrUnavailable
	}
	var result struct {
		Active *bool `json:"active"`
	}
	if json.Unmarshal(data, &result) != nil || result.Active == nil {
		return ErrUnavailable
	}
	if !*result.Active {
		return ErrRevoked
	}
	return nil
}
