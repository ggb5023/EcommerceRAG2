package gateway

import (
	"context"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	"github.com/ggb5023/EcommerceRAG/go/internal/facts"
	"github.com/ggb5023/EcommerceRAG/go/internal/identity"
	"github.com/ggb5023/EcommerceRAG/go/internal/localauth"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/ggb5023/EcommerceRAG/go/internal/transport"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// wireDependencies assembles the gateway: database pool, identity adapter,
// scope-aware RPC clients, facts provider, signing key and the local-account
// wiring. It returns the connections it opened as one cleanup function so the
// caller keeps ownership of their lifetime.
func wireDependencies(ctx context.Context) (*gateway, *pgxpool.Pool, string, *identity.Browser, func()) {
	if env := os.Getenv("APP_ENV"); env == "selfhost" || env == "production" {
		profile := os.Getenv("V2_IDENTITY_PROFILE")
		if (profile != "oidc_browser" && profile != "local") || os.Getenv("V2_RPC_TLS") != "mtls" || os.Getenv("V2_CUSTOMER_ORIGIN") == "" || os.Getenv("V2_FACTS_BASE_URL") != "" || os.Getenv("RAG_PROFILE") != "v2_langchain_bailian" {
			log.Fatal("selfhost requires local or browser identity, mTLS, customer origin and real Provider; facts are disabled")
		}
	} else if os.Getenv("APP_ENV") != "development" && os.Getenv("APP_ENV") != "test" {
		log.Fatal("an identity adapter is required; development/test adapter is explicit")
	}
	profile := os.Getenv("RAG_PROFILE")
	if !isV2Profile(profile) {
		log.Fatal("an explicit V2 RAG_PROFILE is required")
	}
	dsn, ragAddr, ingestAddr := os.Getenv("V2_GATEWAY_DSN"), os.Getenv("V2_GRPC_ADDR"), os.Getenv("V2_INGEST_GRPC_ADDR")
	if dsn == "" || ragAddr == "" || ingestAddr == "" {
		log.Fatal("explicit Gateway database, AI RPC and ingestion RPC configurations are required")
	}
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatal("database pool configuration failed")
	}
	var database, role string
	var superuser bool
	if err = db.QueryRow(ctx, `SELECT current_database(),current_user,rolsuper FROM pg_roles WHERE rolname=current_user`).Scan(&database, &role, &superuser); err != nil || superuser || role != "rag_v2_gateway" || !strings.Contains(database, "v2") {
		log.Fatal("Gateway requires its restricted role and independent V2 database")
	}
	var session mockSession
	var adapter IdentityAdapter
	var browser *identity.Browser
	var localStore *localauth.Store
	var localToken string
	switch os.Getenv("V2_IDENTITY_PROFILE") {
	case "", "development":
		session, err = (developmentIdentityAdapter{db: db}).Resolve(ctx)
		if err != nil {
			log.Fatal("development identity unavailable")
		}
	case "local":
		store := localauth.NewStore(db)
		adapter = localIdentityAdapter{store: store}
		localStore = store
		localToken = randomToken()
	case "oidc", "oidc_browser":
		config, configErr := identity.LoadConfig(os.Getenv("V2_OIDC_CONFIG_FILE"))
		if configErr != nil {
			log.Fatal("protected OIDC configuration unavailable")
		}
		provider, providerErr := identity.New(ctx, config)
		if providerErr != nil {
			log.Fatal("OIDC discovery unavailable")
		}
		oidcAdapter := oidcIdentityAdapter{provider: provider, db: db}
		adapter = oidcAdapter
		if os.Getenv("V2_IDENTITY_PROFILE") == "oidc_browser" {
			browser, err = identity.NewBrowser(provider, func(ctx context.Context, principal identity.Principal) error {
				_, _, failure := oidcAdapter.resolvePrincipal(ctx, principal)
				if failure == nil {
					return nil
				}
				if status.Code(failure) == codes.Unavailable {
					return identity.ErrUnavailable
				}
				return identity.ErrRevoked
			})
			if err != nil {
				log.Fatal("browser identity configuration unavailable")
			}
			if config.AuthorizationSource == "database" {
				browser.BindAuthority = func(ctx context.Context, p identity.Principal) (identity.Principal, error) {
					session, _, err := oidcAdapter.resolvePrincipal(ctx, p)
					if err != nil {
						return p, identity.ErrRevoked
					}
					p.Claims.Role = session.Role
					p.Claims.Shops = slices.Clone(session.AllowedShops)
					p.Claims.Revision = session.PermissionRevision
					return p, nil
				}
			}
			adapter = browserIdentityAdapter{browser: browser, adapter: oidcAdapter}
		}
	default:
		log.Fatal("an explicit supported identity profile is required")
	}
	rpcCredentials, err := transport.ClientCredentials()
	if err != nil {
		log.Fatal("internal RPC credentials unavailable")
	}
	conn, err := grpc.NewClient(ragAddr, grpc.WithTransportCredentials(rpcCredentials))
	if err != nil {
		log.Fatal("AI RPC configuration failed")
	}
	ingestConn, err := grpc.NewClient(ingestAddr, grpc.WithTransportCredentials(rpcCredentials))
	if err != nil {
		log.Fatal("ingest RPC configuration failed")
	}
	factsProvider := facts.Provider(facts.UnavailableProvider{})
	if base := strings.TrimSpace(os.Getenv("V2_FACTS_BASE_URL")); base != "" {
		version := strings.TrimSpace(os.Getenv("V2_FACTS_VERSION"))
		if version == "" {
			version = "v1"
		}
		factsProvider = facts.HTTPProvider{BaseURL: base, Version: version}
	}
	g := &gateway{db: db, session: session, profile: profile, rag: ragv1.NewRagServiceClient(conn), ingest: ragv1.NewIngestServiceClient(ingestConn), grpcConn: conn, facts: factsProvider, cancels: map[string]context.CancelFunc{}, identity: adapter, browser: browser}
	g.signingKey, err = auth.LoadSigningKey()
	if err != nil {
		log.Fatal("restricted scope signing key is unavailable")
	}
	if localStore != nil {
		g.localAuth = &localauth.Handler{
			Store:           localStore,
			Limiter:         localauth.NewLimiter(nil),
			Key:             g.signingKey,
			Origins:         []string{os.Getenv("V2_CUSTOMER_ORIGIN")},
			Secure:          func(r *http.Request) bool { return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" },
			LockoutAttempts: 5,
			LockoutFor:      15 * time.Minute,
		}
		g.localSetup = &localauth.Setup{Store: localStore, Token: localToken, TokenIssued: time.Now()}
		// The one-time install token is printed once at startup; it is never
		// written to the database or returned by an API.
		log.Printf("SETUP_TOKEN=%s", localToken)
	}
	if adapter == nil {
		if err = g.authority().Refresh(ctx); err != nil {
			log.Fatal("identity scope rejected")
		}
	} else {
		g.scopeIdentities = &scopeIdentityRegistry{}
	}
	cleanup := func() {
		ingestConn.Close()
		conn.Close()
		db.Close()
	}
	return g, db, profile, browser, cleanup
}
