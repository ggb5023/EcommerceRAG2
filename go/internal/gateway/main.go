package gateway

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ggb5023/EcommerceRAG/go/internal/admin"
	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	"github.com/ggb5023/EcommerceRAG/go/internal/customer"
	"github.com/ggb5023/EcommerceRAG/go/internal/edge"
	"github.com/ggb5023/EcommerceRAG/go/internal/facts"
	"github.com/ggb5023/EcommerceRAG/go/internal/identity"
	"github.com/ggb5023/EcommerceRAG/go/internal/localauth"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const turnTimeout = 60 * time.Second

var orderIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var queryResourcePattern = regexp.MustCompile(`[A-Za-z0-9_-]+`)

func routedOrderResource(response *ragv1.UnderstandResponse, query string) (string, bool) {
	if response == nil {
		return "", false
	}
	ids := make([]string, 0, 1)
	for _, entity := range response.Entities {
		if entity.GetName() != "order_id" {
			continue
		}
		confidence := entity.GetConfidence()
		if math.IsNaN(confidence) || math.IsInf(confidence, 0) || confidence < 0.5 || confidence > 1 {
			return "", false
		}
		id := strings.TrimSpace(entity.GetValue())
		if id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) != 1 || !orderIDPattern.MatchString(ids[0]) {
		return "", false
	}
	if !slices.Contains(queryResourcePattern.FindAllString(query, -1), ids[0]) {
		return "", false
	}
	return ids[0], true
}

func isV2Profile(profile string) bool {
	return profile == "v2_langgraph_fixture" || profile == "v2_langchain_mock" || profile == "v2_langchain_bailian"
}

func isMockProfile(profile string) bool {
	return profile != "v2_langchain_bailian"
}

type mockSession = auth.Session

type developmentIdentityAdapter struct{ db *pgxpool.Pool }

func (a developmentIdentityAdapter) Resolve(ctx context.Context) (mockSession, error) {
	var session mockSession
	tenant, user := os.Getenv("V2_IDENTITY_TENANT"), os.Getenv("V2_IDENTITY_USER")
	if tenant == "" || user == "" {
		return session, errors.New("explicit development identity is required")
	}
	err := a.db.QueryRow(ctx, `SELECT t.id,u.id,t.external_id,u.external_id,u.Role,COALESCE(u.shop_ids,ARRAY[]::text[]),u.permission_revision,u.revoked_at
        FROM tenant t JOIN app_user u ON u.tenant_id=t.id WHERE t.external_id=$1 AND u.external_id=$2 AND t.status='active'`, tenant, user).Scan(&session.TenantID, &session.UserID, &session.Tenant, &session.User, &session.Role, &session.AllowedShops, &session.PermissionRevision, &session.RevokedAt)
	if err != nil {
		return session, err
	}
	session.Shop = os.Getenv("V2_IDENTITY_SHOP")
	if session.Shop == "" && len(session.AllowedShops) > 0 {
		session.Shop = session.AllowedShops[0]
	}
	return session, nil
}

type gateway struct {
	db              *pgxpool.Pool
	rag             ragv1.RagServiceClient
	ingest          ragv1.IngestServiceClient
	facts           facts.Provider
	grpcConn        *grpc.ClientConn
	session         mockSession
	signingKey      []byte
	profile         string
	mu              sync.Mutex
	revisionMu      sync.RWMutex
	ready           atomic.Bool
	cancels         map[string]context.CancelFunc
	root            *gateway
	identity        IdentityAdapter
	identityCheck   func(context.Context) error
	scopeIdentities *scopeIdentityRegistry
	browser         *identity.Browser
	localAuth       *localauth.Handler
	localSetup      *localauth.Setup
	customer        *customer.Service
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (g *gateway) authorizeHTTP(w http.ResponseWriter, r *http.Request) bool {
	if g.db == nil { // unit tests construct a handler-only gateway
		return true
	}
	if err := g.authority().Refresh(r.Context()); err != nil {
		code := http.StatusForbidden
		if status.Code(err) == codes.Unavailable {
			code = http.StatusServiceUnavailable
		}
		writeError(w, code, auth.FailureCode(err), "authorization failed")
		return false
	}
	return true
}

func Main() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	g, db, profile, browser, closeWired := wireDependencies(ctx)
	defer closeWired()
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	owner, err := g.acquireExecutionOwner(ctx)
	if err != nil {
		log.Fatal("Gateway execution ownership unavailable")
	}
	defer owner.Close(context.Background())
	if origin := os.Getenv("V2_CUSTOMER_ORIGIN"); origin != "" {
		config := customer.Config{Origin: origin, AllowLoopbackHTTP: os.Getenv("V2_CUSTOMER_LOOPBACK_HTTP") == "1"}
		for key, target := range map[string]*int{"V2_CUSTOMER_VISITOR_RPM": &config.VisitorRPM, "V2_CUSTOMER_IP_RPM": &config.IPRPM} {
			if raw := os.Getenv(key); raw != "" {
				value, e := strconv.Atoi(raw)
				if e != nil || value < 1 {
					log.Fatal("invalid customer rate limit")
				}
				*target = value
			}
		}
		if host := os.Getenv("V2_CUSTOMER_PROXY_HOST"); host != "" {
			config.TrustedProxyIPs, err = net.LookupIP(host)
			if err != nil {
				log.Fatal("trusted customer proxy unavailable")
			}
		}
		g.customer, err = customer.New(db, g.rag, g.signingKey, profile, config)
		if err != nil || g.customer.Recover(ctx) != nil {
			log.Fatal("customer channel initialization failed")
		}
		go g.customer.Reap(context.Background())
	}
	authorityRPC, err := g.startAuthorizationRPC()
	if err != nil {
		log.Fatal("authority RPC initialization failed")
	}
	if authorityRPC != nil {
		defer authorityRPC.Stop()
	}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if !executionOwnerAlive(owner) {
				g.ready.Store(false)
				log.Fatal("Gateway execution ownership lost")
			}
		}
	}()
	handler := g.Routes()
	secure := false
	if root := strings.TrimSpace(os.Getenv("WEB_ROOT")); root != "" {
		// The gateway takes over the edge role: one HTTPS origin, static
		// frontend fallback and the optional identity proxy, with the same
		// host check and CSP as before.
		host := strings.TrimSpace(os.Getenv("ECR_HOSTNAME"))
		if host == "" {
			log.Fatal("WEB_ROOT requires ECR_HOSTNAME")
		}
		port := os.Getenv("ECR_HTTPS_PORT")
		if port == "" {
			port = "8443"
		}
		authority := host
		if port != "443" {
			authority = net.JoinHostPort(host, port)
		}
		var identityProxy http.Handler
		if browser != nil {
			target := strings.TrimSpace(os.Getenv("V2_IDENTITY_PROXY_URL"))
			if target == "" {
				target = "http://keycloak:8080"
			}
			identityProxy = edge.Proxy(target)
		}
		handler = edge.Routes(handler, identityProxy, edge.SPA(root, ""), authority)
		secure = true
	}
	server := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	if secure {
		server.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS13}
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal("Gateway bind failed")
	}
	if recovered, recoveryErr := g.recoverInterruptedExecutions(ctx); recoveryErr != nil {
		_ = listener.Close()
		log.Fatal("Gateway interrupted execution recovery failed")
	} else if recovered > 0 {
		log.Printf("Gateway recovered %d interrupted executions", recovered)
	}
	g.ready.Store(true)
	log.Printf("V2 Gateway listening on %s", addr)
	if secure {
		cert, key := os.Getenv("WEB_TLS_CERT"), os.Getenv("WEB_TLS_KEY")
		if cert == "" {
			cert = "/run/web/tls.crt"
		}
		if key == "" {
			key = "/run/web/tls.key"
		}
		err = server.ServeTLS(listener, cert, key)
	} else {
		err = server.Serve(listener)
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal("Gateway stopped unexpectedly")
	}
}

func (g *gateway) businessRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", g.health)
	mux.HandleFunc("POST /internal/authorization/validate", g.validateScopeHTTP)
	mux.HandleFunc("GET /v1/session", g.getSession)
	mux.HandleFunc("GET /v1/conversations", g.listConversations)
	mux.HandleFunc("POST /v1/conversations", g.createConversation)
	mux.HandleFunc("GET /v1/conversations/{id}", g.getConversation)
	mux.HandleFunc("POST /v1/conversations/{id}/turns", g.createTurn)
	mux.HandleFunc("POST /v1/turns/{id}/resume", g.resumeTurn)
	mux.HandleFunc("GET /v1/turns/{id}", g.getTurn)
	mux.HandleFunc("GET /v1/turns/{id}/events", g.events)
	mux.HandleFunc("POST /v1/turns/{id}/cancel", g.cancelTurn)
	if g.customer != nil {
		mux.HandleFunc("/v1/support/", func(w http.ResponseWriter, r *http.Request) {
			if !g.requireWriteRole(w) {
				return
			}
			g.customer.StaffRoutes(customer.Staff{Tenant: g.session.TenantID, User: g.session.UserID, Shops: g.session.AllowedShops, Role: g.session.Role, SessionHash: customer.StaffSessionHash(r), Check: g.authority().Refresh}).ServeHTTP(w, r)
		})
	}
	customerOrigin := ""
	if g.customer != nil {
		customerOrigin = g.customer.Config.Origin
	}
	(&admin.Handler{DB: g.db, Session: &g.session, Authority: g.authority(), Ingest: g.ingest, AIConnection: g.grpcConn, Profile: g.profile, SigningKey: g.signingKey, CustomerOrigin: customerOrigin}).RegisterRoutes(mux)
	return mux
}

func (g *gateway) Routes() http.Handler {
	mux := g.businessRoutes()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.db != nil && r.URL.Path != "/internal/authorization/validate" && !g.ready.Load() {
			writeError(w, http.StatusServiceUnavailable, "service_starting", "service is starting")
			return
		}
		if r.URL.Path == "/setup" && g.localSetup != nil {
			g.localSetup.Handle(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/auth/") {
			if g.localAuth != nil {
				g.localAuth.Handle(w, r)
				return
			}
			if g.browser != nil {
				if r.Method == "POST" && r.URL.Path == "/v1/auth/logout" && g.customer != nil && g.browser.ValidateWrite(r) == nil {
					_ = g.customer.ReleaseSession(r.Context(), customer.StaffSessionHash(r))
				}
				g.browser.Handle(w, r)
				return
			}
			if r.Method == "GET" && r.URL.Path == "/v1/auth/session" {
				w.Header().Set("Cache-Control", "no-store")
				writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "authenticated": g.identity == nil})
				return
			}
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/customer/") {
			if g.customer == nil {
				http.NotFound(w, r)
				return
			}
			g.customer.Routes().ServeHTTP(w, r)
			return
		}
		if r.URL.Path != "/healthz" && r.URL.Path != "/internal/authorization/validate" {
			if g.browser != nil {
				if err := g.browser.ValidateWrite(r); err != nil {
					code := http.StatusForbidden
					if errors.Is(err, identity.ErrInvalid) {
						code = http.StatusUnauthorized
					}
					writeError(w, code, "browser_request_rejected", "browser request rejected")
					return
				}
			}
			if g.localAuth != nil {
				if err := g.localAuth.ValidateWrite(r); err != nil {
					code := http.StatusForbidden
					if errors.Is(err, localauth.ErrSessionInvalid) {
						code = http.StatusUnauthorized
					}
					writeError(w, code, "local_request_rejected", "request rejected")
					return
				}
			}
			if g.identity != nil {
				session, check, err := g.identity.Resolve(r)
				if err != nil {
					code := http.StatusUnauthorized
					if status.Code(err) == codes.PermissionDenied {
						code = http.StatusForbidden
					} else if status.Code(err) == codes.Unavailable {
						code = http.StatusServiceUnavailable
					}
					writeError(w, code, "identity_rejected", "identity rejected")
					return
				}
				if session.MustChangePassword {
					writeError(w, http.StatusForbidden, "password_change_required", "password change required")
					return
				}
				requestGateway := g.scoped(session, check)
				if !requestGateway.authorizeHTTP(w, r) {
					return
				}
				requestGateway.businessRoutes().ServeHTTP(w, r)
				return
			}
			if !g.authorizeHTTP(w, r) {
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func (g *gateway) health(w http.ResponseWriter, r *http.Request) {
	if err := g.db.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "component": "gateway", "stage": "v2"})
}

func (g *gateway) getSession(w http.ResponseWriter, _ *http.Request) {
	if g.db != nil {
		if err := g.authority().Refresh(context.Background()); err != nil {
			statusCode := http.StatusForbidden
			if status.Code(err) == codes.Unavailable {
				statusCode = http.StatusServiceUnavailable
			}
			writeError(w, statusCode, auth.FailureCode(err), "authorization failed")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tenantId": g.session.Tenant, "userId": g.session.User, "role": g.session.Role,
		"shopId": g.session.Shop, "shopName": g.session.Shop, "displayName": g.session.User, "profile": g.profile,
		"allowedShopIds": g.session.AllowedShops, "allShops": g.session.AllShops,
		"permission_revision": g.authority().CurrentRevision(),
		"is_mock":             isMockProfile(g.profile),
	})
}

func (g *gateway) listConversations(w http.ResponseWriter, r *http.Request) {
	rows, err := g.db.Query(r.Context(), `SELECT id, COALESCE(title,''), shop_id, created_at
		FROM conversation c WHERE tenant_id=$1 AND user_id=$2 AND shop_id=$3
        AND NOT EXISTS(SELECT 1 FROM turn t JOIN conversation_execution e ON e.tenant_id=t.tenant_id AND e.turn_id=t.id
			WHERE t.tenant_id=c.tenant_id AND t.conversation_id=c.id AND e.permission_revision IS DISTINCT FROM $4)
		ORDER BY created_at DESC LIMIT 100`, g.session.TenantID, g.session.UserID, g.session.Shop, g.authority().CurrentRevision())
	if err != nil {
		writeError(w, 503, "database_unavailable", "could not load conversations")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var title, shop string
		var created time.Time
		if err := rows.Scan(&id, &title, &shop, &created); err != nil {
			writeError(w, 503, "database_unavailable", "could not load conversations")
			return
		}
		items = append(items, map[string]any{"id": strconv.FormatInt(id, 10), "title": title, "shop_id": shop,
			"created_at": created, "messages": []any{}})
	}
	if rows.Err() != nil {
		writeError(w, 503, "database_unavailable", "could not load conversations")
		return
	}
	if err := g.authority().Refresh(r.Context()); err != nil {
		writeError(w, authorizationHTTPStatus(err), auth.FailureCode(err), "authorization failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (g *gateway) createConversation(w http.ResponseWriter, r *http.Request) {
	if !g.requireWriteRole(w) {
		return
	}
	var in struct {
		Title  string `json:"title"`
		ShopID string `json:"shop_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_json", "invalid request body")
		return
	}
	if in.ShopID != "" && in.ShopID != g.session.Shop {
		writeError(w, 403, "forbidden", "shop is outside the current session")
		return
	}
	if strings.TrimSpace(in.Title) == "" {
		in.Title = "新会话"
	}
	var id int64
	err := g.db.QueryRow(r.Context(), `INSERT INTO conversation(tenant_id,user_id,shop_id,title)
		VALUES($1,$2,$3,NULLIF($4,'')) RETURNING id`, g.session.TenantID, g.session.UserID,
		g.session.Shop, in.Title).Scan(&id)
	if err != nil {
		writeError(w, 503, "database_unavailable", "could not create conversation")
		return
	}
	writeJSON(w, 201, map[string]any{"id": strconv.FormatInt(id, 10), "title": in.Title, "shop_id": g.session.Shop, "messages": []any{}})
}

func (g *gateway) getConversation(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, 404, "not_found", "conversation not found")
		return
	}
	var title string
	err = g.db.QueryRow(r.Context(), `SELECT COALESCE(title,'') FROM conversation
		WHERE tenant_id=$1 AND user_id=$2 AND shop_id=$3 AND id=$4`,
		g.session.TenantID, g.session.UserID, g.session.Shop, id).Scan(&title)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "conversation not found")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable", "could not load conversation")
		return
	}
	if !g.authorizeConversationHistory(w, r, id) {
		return
	}
	rows, err := g.db.Query(r.Context(), `SELECT m.Role, COALESCE(m.content,''), t.request_id,t.id,COALESCE((SELECT MAX(e.execution_no) FROM conversation_execution e WHERE e.tenant_id=t.tenant_id AND e.turn_id=t.id),1)
		FROM turn t JOIN message m ON m.tenant_id=t.tenant_id AND m.turn_id=t.id
		WHERE t.tenant_id=$1 AND t.conversation_id=$2 ORDER BY t.turn_no,m.created_at`, g.session.TenantID, id)
	if err != nil {
		writeError(w, 503, "database_unavailable", "could not load messages")
		return
	}
	defer rows.Close()
	messages := make([]map[string]any, 0)
	for rows.Next() {
		var role, content, requestID string
		var turnID int64
		var executionNo int
		if err := rows.Scan(&role, &content, &requestID, &turnID, &executionNo); err != nil {
			writeError(w, 503, "database_unavailable", "could not load messages")
			return
		}
		messages = append(messages, map[string]any{"role": role, "content": content, "request_id": requestID, "turn_id": strconv.FormatInt(turnID, 10), "execution_no": executionNo, "mock": role == "assistant"})
	}
	if err = rows.Err(); err != nil {
		writeError(w, 503, "database_unavailable", "could not load messages")
		return
	}
	rows.Close()
	var latestRequest string
	err = g.db.QueryRow(r.Context(), `SELECT e.request_id FROM turn t JOIN conversation_execution e ON e.tenant_id=t.tenant_id AND e.turn_id=t.id
		WHERE t.tenant_id=$1 AND t.conversation_id=$2 ORDER BY t.turn_no DESC,e.execution_no DESC LIMIT 1`, g.session.TenantID, id).Scan(&latestRequest)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 503, "database_unavailable", "could not load execution")
		return
	}
	result := map[string]any{"id": r.PathValue("id"), "title": title, "messages": messages}
	if latestRequest != "" {
		last, loadErr := g.executionResult(r.Context(), latestRequest)
		if loadErr != nil {
			writeError(w, 503, "database_unavailable", "could not load execution")
			return
		}
		result["last_turn"] = last
		result["evidence"] = last["evidence"]
		result["citations"] = last["citations"]
		result["lastReply"] = last["answer"]
		result["lastReplyCopyable"] = false
	}
	if err := g.authority().Refresh(r.Context()); err != nil {
		writeError(w, authorizationHTTPStatus(err), auth.FailureCode(err), "authorization failed")
		return
	}
	writeJSON(w, 200, result)
}

func (g *gateway) createTurn(w http.ResponseWriter, r *http.Request) {
	if !g.requireWriteRole(w) {
		return
	}
	conversationID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, 404, "not_found", "conversation not found")
		return
	}
	var in struct {
		Text         string `json:"text"`
		Query        string `json:"query"`
		ParentTurnID string `json:"parent_turn_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_json", "invalid request body")
		return
	}
	query := strings.TrimSpace(in.Text)
	if query == "" {
		query = strings.TrimSpace(in.Query)
	}
	if query == "" {
		writeError(w, 400, "invalid_query", "query is required")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeError(w, 400, "idempotency_key_required", "Idempotency-Key is required")
		return
	}
	canonicalPayload, _ := json.Marshal(map[string]string{"query": query, "parent_turn_id": strings.TrimSpace(in.ParentTurnID)})
	hash := sha256.Sum256(canonicalPayload)
	payloadHash := hex.EncodeToString(hash[:])
	tx, err := g.db.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		writeError(w, 503, "database_unavailable", "could not start turn")
		return
	}
	defer tx.Rollback(r.Context())
	var shopID string
	err = tx.QueryRow(r.Context(), `SELECT shop_id FROM conversation
		WHERE tenant_id=$1 AND user_id=$2 AND id=$3 FOR UPDATE`,
		g.session.TenantID, g.session.UserID, conversationID).Scan(&shopID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "conversation not found")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable", "could not lock conversation")
		return
	}
	if shopID != g.session.Shop {
		writeError(w, 403, "forbidden", "shop is outside the current session")
		return
	}
	if !g.authorizeConversationHistory(w, r, conversationID) {
		return
	}
	var parentTurnID *int64
	var parentExecutionID int64
	if strings.TrimSpace(in.ParentTurnID) != "" {
		var parentID int64
		parentID, err = strconv.ParseInt(strings.TrimSpace(in.ParentTurnID), 10, 64)
		if err != nil {
			err = tx.QueryRow(r.Context(), `SELECT id FROM turn WHERE tenant_id=$1 AND conversation_id=$2 AND request_id=$3`,
				g.session.TenantID, conversationID, strings.TrimSpace(in.ParentTurnID)).Scan(&parentID)
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, 400, "invalid_parent_turn", "parent turn is invalid")
				return
			}
		}
		var parentStatus string
		err = tx.QueryRow(r.Context(), `SELECT status FROM turn WHERE tenant_id=$1 AND conversation_id=$2 AND id=$3`,
			g.session.TenantID, conversationID, parentID).Scan(&parentStatus)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "parent_turn_not_found", "parent turn not found")
			return
		}
		if err != nil {
			writeError(w, 503, "database_unavailable", "could not load parent turn")
			return
		}
		if parentStatus != "ASKING" {
			writeError(w, 409, "parent_turn_not_asking", "parent turn is not awaiting clarification")
			return
		}
		err = tx.QueryRow(r.Context(), `SELECT e.id FROM conversation_execution e
			WHERE e.tenant_id=$1 AND e.turn_id=$2 ORDER BY e.execution_no DESC LIMIT 1`,
			g.session.TenantID, parentID).Scan(&parentExecutionID)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 409, "parent_checkpoint_missing", "parent turn has no execution checkpoint")
			return
		}
		if err != nil {
			writeError(w, 503, "database_unavailable", "could not load parent execution")
			return
		}
		parentTurnID = &parentID
	}
	var oldHash, oldRequest, oldStatus string
	var oldTurnID int64
	var oldExecutionNo int
	err = tx.QueryRow(r.Context(), `SELECT i.payload_hash,e.request_id,e.status,t.id,e.execution_no FROM turn_idempotency i
		JOIN turn t ON t.tenant_id=i.tenant_id AND t.id=i.turn_id
		JOIN conversation_execution e ON e.tenant_id=t.tenant_id AND e.turn_id=t.id
			AND ((i.execution_id IS NOT NULL AND e.id=i.execution_id)
				OR (i.execution_id IS NULL AND e.execution_no=1))
		WHERE i.tenant_id=$1 AND i.conversation_id=$2 AND i.idempotency_key=$3`,
		g.session.TenantID, conversationID, key).Scan(&oldHash, &oldRequest, &oldStatus, &oldTurnID, &oldExecutionNo)
	if err == nil {
		if oldHash != payloadHash {
			writeError(w, 409, "idempotency_conflict", "idempotency key was used with a different query")
			return
		}
		writeJSON(w, 200, map[string]any{"id": oldRequest, "request_id": oldRequest, "status": oldStatus, "turn_id": strconv.FormatInt(oldTurnID, 10), "execution_no": oldExecutionNo, "conversation_id": strconv.FormatInt(conversationID, 10),
			"events_url": "/v1/turns/" + oldRequest + "/events", "is_mock": isMockProfile(g.profile)})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 503, "database_unavailable", "could not check idempotency")
		return
	}
	var active bool
	err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM turn WHERE tenant_id=$1
		AND conversation_id=$2 AND status IN ('PENDING','EXECUTING','CANCEL_REQUESTED'))`,
		g.session.TenantID, conversationID).Scan(&active)
	if err != nil {
		writeError(w, 503, "database_unavailable", "could not check active turn")
		return
	}
	if active {
		writeError(w, 409, "turn_in_progress", "conversation already has an active turn")
		return
	}
	requestID := newRequestID()
	var turnID, turnNo, executionID int64
	err = tx.QueryRow(r.Context(), `SELECT COALESCE(MAX(turn_no),0)+1 FROM turn
		WHERE tenant_id=$1 AND conversation_id=$2`, g.session.TenantID, conversationID).Scan(&turnNo)
	if err == nil {
		err = tx.QueryRow(r.Context(), `INSERT INTO turn(tenant_id,conversation_id,turn_no,request_id,user_query,status,parent_turn_id)
			VALUES($1,$2,$3,$4,$5,'EXECUTING',$6) RETURNING id`, g.session.TenantID,
			conversationID, turnNo, requestID, query, parentTurnID).Scan(&turnID)
	}
	deadline := time.Now().Add(turnTimeout)
	if err == nil {
		err = tx.QueryRow(r.Context(), `INSERT INTO conversation_execution(tenant_id,turn_id,execution_no,request_id,status,is_mock,deadline_at,permission_revision)
			VALUES($1,$2,1,$3,'EXECUTING',$6,$4,$5) RETURNING id`,
			g.session.TenantID, turnID, requestID, deadline, g.authority().CurrentRevision(), isMockProfile(g.profile)).Scan(&executionID)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO turn_idempotency(tenant_id,conversation_id,idempotency_key,payload_hash,turn_id,execution_id)
			VALUES($1,$2,$3,$4,$5,$6)`, g.session.TenantID, conversationID, key, payloadHash, turnID, executionID)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO message(tenant_id,turn_id,role,content) VALUES($1,$2,'user',$3)`, g.session.TenantID, turnID, query)
	}
	if err != nil {
		writeError(w, 503, "database_unavailable", "could not persist turn")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 503, "database_unavailable", "could not commit turn")
		return
	}
	_, _ = g.db.Exec(r.Context(), `UPDATE conversation SET title=CASE WHEN title IS NULL OR title IN ('','新会话') THEN $3 ELSE title END
		WHERE tenant_id=$1 AND id=$2`, g.session.TenantID, conversationID, first24(query))
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	registry := g.executionRegistryOwner()
	registry.mu.Lock()
	registry.cancels[requestID] = cancel
	registry.mu.Unlock()
	go func() {
		var parentID, parentExecution int64
		if parentTurnID != nil {
			parentID = *parentTurnID
			parentExecution = parentExecutionID
		}
		g.runTurn(ctx, requestID, conversationID, turnID, executionID, query, parentID, parentExecution, 1)
	}()
	writeJSON(w, 202, map[string]any{"id": requestID, "request_id": requestID, "status": "EXECUTING",
		"turn_id": strconv.FormatInt(turnID, 10), "execution_no": 1, "conversation_id": strconv.FormatInt(conversationID, 10),
		"is_mock": isMockProfile(g.profile), "events_url": "/v1/turns/" + requestID + "/events"})
}

// resumeTurn continues the existing v2 ASKING turn with a new execution.
// The original turn and checkpoint thread remain stable; only Go creates the
// new request/execution identity and Python consumes the minimal checkpoint.
func (g *gateway) resumeTurn(w http.ResponseWriter, r *http.Request) {
	if !g.requireWriteRole(w) {
		return
	}
	if err := g.authority().Refresh(r.Context()); err != nil {
		writeError(w, authorizationHTTPStatus(err), auth.FailureCode(err), "authorization failed")
		return
	}
	if !isV2Profile(g.profile) {
		writeError(w, http.StatusConflict, "checkpoint_resume_unavailable", "checkpoint resume is available only for the v2 graph profile")
		return
	}
	turnID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "turn not found")
		return
	}
	var in struct {
		Text   string `json:"text"`
		Answer string `json:"answer"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "invalid request body")
		return
	}
	query := strings.TrimSpace(in.Text)
	if query == "" {
		query = strings.TrimSpace(in.Answer)
	}
	if query == "" {
		writeError(w, http.StatusBadRequest, "invalid_query", "answer is required")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeError(w, http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key is required")
		return
	}
	canonicalPayload, _ := json.Marshal(map[string]string{"answer": query})
	hash := sha256.Sum256(canonicalPayload)
	payloadHash := hex.EncodeToString(hash[:])
	storageKey := "resume:" + key
	tx, err := g.db.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "could not start resume")
		return
	}
	defer tx.Rollback(r.Context())
	var conversationID int64
	var shopID, turnStatus string
	err = tx.QueryRow(r.Context(), `SELECT t.conversation_id,c.shop_id,t.status
		FROM turn t JOIN conversation c ON c.tenant_id=t.tenant_id AND c.id=t.conversation_id
		WHERE t.tenant_id=$1 AND t.id=$2 AND c.user_id=$3 FOR UPDATE OF t,c`,
		g.session.TenantID, turnID, g.session.UserID).Scan(&conversationID, &shopID, &turnStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "turn not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "could not lock turn")
		return
	}
	if shopID != g.session.Shop {
		writeError(w, http.StatusForbidden, "forbidden", "turn is outside the current session")
		return
	}
	if !g.authorizeConversationHistory(w, r, conversationID) {
		return
	}
	var oldHash string
	var oldTurnID, oldExecutionID int64
	err = tx.QueryRow(r.Context(), `SELECT payload_hash,turn_id,COALESCE(execution_id,
		(SELECT e.id FROM conversation_execution e
		 WHERE e.tenant_id=turn_idempotency.tenant_id AND e.turn_id=turn_idempotency.turn_id
		 ORDER BY e.execution_no ASC LIMIT 1))
		FROM turn_idempotency
		WHERE tenant_id=$1 AND conversation_id=$2 AND idempotency_key=$3`,
		g.session.TenantID, conversationID, storageKey).Scan(&oldHash, &oldTurnID, &oldExecutionID)
	if err == nil {
		if oldHash != payloadHash || oldTurnID != turnID {
			writeError(w, http.StatusConflict, "idempotency_conflict", "idempotency key was used with a different resume")
			return
		}
		var oldRequest, oldStatus string
		var oldExecutionNo int64
		err = tx.QueryRow(r.Context(), `SELECT request_id,status,execution_no FROM conversation_execution
			WHERE tenant_id=$1 AND id=$2`, g.session.TenantID, oldExecutionID).Scan(&oldRequest, &oldStatus, &oldExecutionNo)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "database_unavailable", "could not load resumed execution")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": oldRequest, "request_id": oldRequest,
			"status": oldStatus, "turn_id": strconv.FormatInt(turnID, 10), "execution_no": oldExecutionNo,
			"conversation_id": strconv.FormatInt(conversationID, 10), "events_url": "/v1/turns/" + oldRequest + "/events", "is_mock": isMockProfile(g.profile)})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "could not check resume idempotency")
		return
	}
	if turnStatus != "ASKING" {
		writeError(w, http.StatusConflict, "turn_not_asking", "turn is not awaiting clarification")
		return
	}
	var askingExecutionID int64
	var askingExecutionNo int64
	err = tx.QueryRow(r.Context(), `SELECT id,execution_no FROM conversation_execution
		WHERE tenant_id=$1 AND turn_id=$2 AND status='ASKING'
		ORDER BY execution_no DESC LIMIT 1 FOR UPDATE`, g.session.TenantID, turnID).Scan(&askingExecutionID, &askingExecutionNo)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusConflict, "parent_checkpoint_missing", "turn has no pending checkpoint execution")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "could not load checkpoint execution")
		return
	}
	requestID := newRequestID()
	executionNo := askingExecutionNo + 1
	deadline := time.Now().Add(turnTimeout)
	var executionID int64
	err = tx.QueryRow(r.Context(), `INSERT INTO conversation_execution(tenant_id,turn_id,execution_no,request_id,status,is_mock,deadline_at,permission_revision)
			VALUES($1,$2,$3,$4,'EXECUTING',$7,$5,$6) RETURNING id`,
		g.session.TenantID, turnID, executionNo, requestID, deadline, g.authority().CurrentRevision(), isMockProfile(g.profile)).Scan(&executionID)
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE turn SET status='EXECUTING',answer=NULL WHERE tenant_id=$1 AND id=$2`, g.session.TenantID, turnID)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO turn_idempotency(tenant_id,conversation_id,idempotency_key,payload_hash,turn_id,execution_id)
			VALUES($1,$2,$3,$4,$5,$6)`, g.session.TenantID, conversationID, storageKey, payloadHash, turnID, executionID)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO message(tenant_id,turn_id,role,content) VALUES($1,$2,'user',$3)`, g.session.TenantID, turnID, query)
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "could not persist resume")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "could not commit resume")
		return
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	registry := g.executionRegistryOwner()
	registry.mu.Lock()
	registry.cancels[requestID] = cancel
	registry.mu.Unlock()
	go func() {
		g.runTurn(ctx, requestID, conversationID, turnID, executionID, query, turnID, askingExecutionID, executionNo)
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"id": requestID, "request_id": requestID,
		"status": "EXECUTING", "turn_id": strconv.FormatInt(turnID, 10), "execution_no": executionNo,
		"conversation_id": strconv.FormatInt(conversationID, 10), "is_mock": isMockProfile(g.profile),
		"events_url": "/v1/turns/" + requestID + "/events"})
}

func (g *gateway) historySummary(ctx context.Context, conversationID, currentTurnID int64) string {
	rows, err := g.db.Query(ctx, `SELECT m.Role, COALESCE(m.content,'')
		FROM message m JOIN turn t ON t.tenant_id=m.tenant_id AND t.id=m.turn_id
		WHERE t.tenant_id=$1 AND t.conversation_id=$2 AND t.id<>$3 AND t.status IN ('DONE','ASKING') AND EXISTS (SELECT 1 FROM conversation_execution e WHERE e.tenant_id=t.tenant_id AND e.turn_id=t.id AND e.permission_revision=$4 AND e.execution_no=(SELECT max(latest.execution_no) FROM conversation_execution latest WHERE latest.tenant_id=t.tenant_id AND latest.turn_id=t.id))
		ORDER BY t.turn_no DESC,m.created_at DESC LIMIT 6`, g.session.TenantID, conversationID, currentTurnID, g.authority().CurrentRevision())
	if err != nil {
		return ""
	}
	defer rows.Close()
	parts := make([]historyMessage, 0, 6)
	for rows.Next() {
		var role, content string
		if rows.Scan(&role, &content) == nil && strings.TrimSpace(content) != "" {
			parts = append(parts, historyMessage{Role: role, Content: content})
		}
	}
	for left, right := 0, len(parts)-1; left < right; left, right = left+1, right-1 {
		parts[left], parts[right] = parts[right], parts[left]
	}
	if rows.Err() != nil {
		return ""
	}
	return boundedHistory(parts)
}

func withSnapshotQualifier(text string) string {
	trimmed := strings.TrimSpace(text)
	if strings.Contains(trimmed, "快照") || strings.Contains(trimmed, "不能保证") {
		return text
	}
	if trimmed == "" {
		return "来自快照，不能保证当前状态。"
	}
	return text + "（来自快照，不能保证当前状态）"
}

func (g *gateway) runTurn(ctx context.Context, requestID string, conversationID, turnID, executionID int64, query string, parentTurnID, parentExecutionID, executionNo int64) {
	mockOutput := isMockProfile(g.profile)
	defer func() {
		registry := g.executionRegistryOwner()
		registry.mu.Lock()
		delete(registry.cancels, requestID)
		registry.mu.Unlock()
	}()
	ident := turnEventIdentity{
		tenantID: g.session.TenantID, executionID: executionID, turnID: turnID,
		conversationID: conversationID, executionNo: executionNo, requestID: requestID,
	}
	writeEvent := func(kind string, payload any) error {
		return writeTurnEvent(g.db, ident, kind, payload)
	}
	finish := func(state, code, answer string) {
		finishTurn(ctx, g.db, ident, state, code, answer, mockOutput)
	}
	ensureAuthorized := func() bool {
		if ctx.Err() != nil {
			state, code := grpcFailure(ctx, ctx.Err())
			finish(state, code, "")
			return false
		}
		if err := g.authority().Refresh(ctx); err != nil {
			finish("FAILED", auth.FailureCode(err), "")
			return false
		}
		return true
	}
	if !ensureAuthorized() {
		return
	}
	if requestPolicyDenied(query) {
		finish("FAILED", "permission_denied", "")
		return
	}
	_ = writeEvent("status", map[string]any{"type": "status", "status": "EXECUTING", "is_mock": mockOutput})
	base, err := g.authority().IssueScopeForTurn(ctx, requestID, conversationID, turnID, executionID)
	if err != nil {
		finish("FAILED", auth.FailureCode(err), "")
		return
	}
	ensureScopeAuthorized := func() bool {
		if !ensureAuthorized() {
			return false
		}
		if g.authority().CurrentRevision() != base.PermissionRevision {
			finish("FAILED", "permission_changed", "")
			return false
		}
		return true
	}
	if parentTurnID != 0 && isV2Profile(g.profile) {
		parentScope, scopeErr := g.authority().IssueScopeForTurn(ctx, requestID, conversationID, parentTurnID, parentExecutionID)
		if scopeErr != nil {
			finish("FAILED", auth.FailureCode(scopeErr), "")
			return
		}
		resumed, resumeErr := g.rag.ResumeCheckpoint(ctx, &ragv1.ResumeCheckpointRequest{
			Context: parentScope, PendingKind: "ASKING",
		}, grpc.WaitForReady(true))
		if resumeErr != nil || resumed == nil || !resumed.Accepted {
			if resumeErr != nil {
				state, code := grpcFailure(ctx, resumeErr)
				finish(state, code, "")
			} else {
				finish("FAILED", "checkpoint_resume_failed", "")
			}
			return
		}
	}

	history := g.historySummary(ctx, conversationID, turnID)
	routed, err := g.rag.Understand(ctx, &ragv1.UnderstandRequest{Context: base, Query: query,
		HistorySummary: history}, grpc.WaitForReady(true))
	if err != nil {
		state, code := grpcFailure(ctx, err)
		finish(state, code, "")
		return
	}
	if routed == nil {
		finish("FAILED", "invalid_routing_response", "")
		return
	}
	routeKnowledgeQuestion(routed, query)
	resolveHistoryReference(routed, query, history)
	orderToolsRequired := slices.Contains(routed.RequiredFactTools, "get_order_status") || slices.Contains(routed.RequiredFactTools, "get_logistics")
	productToolsRequired := slices.Contains(routed.RequiredFactTools, "get_stock") || slices.Contains(routed.RequiredFactTools, "get_price")
	factResourceID := ""
	snapshotFallback := false
	if orderToolsRequired && productToolsRequired {
		routed.Intent, routed.Clarification = "clarification", "请分别确认商品 SKU 和订单号后查询。"
		routed.RequiredFactTools = nil
	} else if orderToolsRequired {
		orderID, valid := routedOrderResource(routed, query)
		if !valid {
			routed.Intent = "clarification"
			routed.Clarification = "请提供有效订单号后再查询订单或物流状态。"
			routed.Confidence = 0.2
			routed.RequiredFactTools = nil
			snapshotFallback = true
		} else {
			factResourceID = orderID
		}
	} else if productToolsRequired {
		sku, valid := routedProductResource(routed, query, history)
		if !valid {
			routed.Intent, routed.Clarification = "clarification", "请确认唯一商品 SKU，再通过授权事实接口查询当前库存或价格；历史库存和商品描述不能代替当前事实。"
			routed.RequiredFactTools = nil
			snapshotFallback = true
		} else {
			factResourceID = sku
		}
	}
	if !ensureScopeAuthorized() {
		return
	}
	factResults, factErr := facts.Lookup(ctx, g.facts, base, routed.RequiredFactTools, factResourceID)
	if factErr != nil {
		for _, toolName := range routed.RequiredFactTools {
			_ = recordToolCall(context.Background(), g.db, g.session.TenantID, requestID, toolName, facts.FailureCode(factErr), true)
		}
		finish("FAILED", facts.FailureCode(factErr), "")
		return
	}
	for _, fact := range factResults {
		if !ensureScopeAuthorized() {
			return
		}
		valueHash := sha256.Sum256([]byte(fact.ValueJson))
		if err := writeEvent("fact_read", map[string]any{
			"type": "fact_read", "tool_name": fact.ToolName, "resource_id": fact.ResourceId,
			"observed_at": fact.ObservedAt, "value_sha256": hex.EncodeToString(valueHash[:]), "is_mock": fact.IsMock,
		}); err != nil {
			finish("FAILED", "fact_evidence_persistence_failed", "")
			return
		}
		_ = recordToolCall(ctx, g.db, g.session.TenantID, requestID, fact.ToolName, "ok", false)
	}
	if !snapshotFallback && (routed.Intent == "clarification" || strings.TrimSpace(routed.Clarification) != "" && routed.Confidence < 0.5) {
		if !ensureScopeAuthorized() {
			return
		}
		clarification := routed.Clarification
		if clarification == "" {
			clarification = "请补充更多问题细节。"
		}
		if isV2Profile(g.profile) {
			saved, saveErr := g.rag.SaveCheckpoint(ctx, &ragv1.SaveCheckpointRequest{
				Context: base, PendingKind: "ASKING",
			})
			if saveErr != nil {
				state, code := grpcFailure(ctx, saveErr)
				finish(state, code, "")
				return
			}
			if saved == nil || !saved.Accepted {
				finish("FAILED", "checkpoint_save_failed", "")
				return
			}
		}
		finish("ASKING", "", clarification)
		return
	}
	if !ensureScopeAuthorized() {
		return
	}
	rawEvidence, searchFailure, aborted := retrieveEvidence(ctx, g.rag, base, routed.RewrittenQuery, mockOutput, ensureScopeAuthorized)
	if aborted {
		return
	}
	if searchFailure != nil {
		finish(searchFailure.state, searchFailure.code, "")
		return
	}
	grants, grantFailure := authorizeEvidence(func(item *ragv1.Evidence) (bool, error) {
		return g.authority().EvidenceAllowed(ctx, item)
	}, rawEvidence)
	if grantFailure != nil {
		finish(grantFailure.state, grantFailure.code, "")
		return
	}
	evidence, filterErr := filterEvidence(rawEvidence, grants, true)
	if filterErr != nil {
		finish("FAILED", "evidence_contract_violated", "")
		return
	}
	if validateEvidenceRanks(evidence) != nil {
		finish("FAILED", "invalid_evidence_labels", "")
		return
	}
	if evidenceAborted, persistFailure := persistEvidence(ctx, g.db, g.session.TenantID, executionID, evidence, ensureScopeAuthorized); evidenceAborted {
		return
	} else if persistFailure != nil {
		finish(persistFailure.state, persistFailure.code, "")
		return
	}
	if snapshotFallback && len(evidence) == 0 {
		if isV2Profile(g.profile) {
			saved, saveErr := g.rag.SaveCheckpoint(ctx, &ragv1.SaveCheckpointRequest{Context: base, PendingKind: "ASKING"})
			if saveErr != nil {
				state, code := grpcFailure(ctx, saveErr)
				finish(state, code, "")
				return
			}
			if saved == nil || !saved.Accepted {
				finish("FAILED", "checkpoint_save_failed", "")
				return
			}
		}
		finish("ASKING", "", routed.Clarification)
		return
	}
	if err = writeEvent("evidence", map[string]any{"type": "evidence", "evidence": toEvidence(evidence), "is_mock": mockOutput}); err != nil {
		finish("FAILED", "persistence_failed", "")
		return
	}
	if !ensureScopeAuthorized() {
		return
	}
	// Retrieval may use a rewrite; generation must retain the user's conditions
	// and requested workflow, which a retrieval-oriented rewrite can omit.
	generated, generateFailure, generateAborted := generateAnswer(ctx, g.rag, &ragv1.GenerateRequest{
		Context: base, Query: query, Evidence: evidence, Facts: factResults,
		ResponsePurpose: "customer_reply", HistorySummary: history,
	}, mockOutput, ensureScopeAuthorized, generationHooks{
		onDelta: func(text string) *stageFailure {
			if err := writeEvent("delta", map[string]any{"type": "delta", "text": text}); err != nil {
				return &stageFailure{state: "FAILED", code: "persistence_failed"}
			}
			return nil
		},
		onCitation: func(citation *ragv1.Evidence) *stageFailure {
			allowed, authErr := g.authority().EvidenceAllowed(ctx, citation)
			if authErr != nil {
				return &stageFailure{state: "FAILED", code: auth.FailureCode(authErr)}
			}
			if !allowed || !validateCitation(citation, evidence) {
				return &stageFailure{state: "FAILED", code: "invalid_citation"}
			}
			if citation.DisclosureClass == "external_allowed" && citation.CustomerEligible {
				if err := persistCitation(ctx, g.db, g.session.TenantID, executionID, citation); err != nil {
					return &stageFailure{state: "FAILED", code: "persistence_failed"}
				}
			}
			return nil
		},
	})
	if generateAborted {
		return
	}
	if generateFailure != nil {
		finish(generateFailure.state, generateFailure.code, "")
		return
	}
	text := finalAnswer(generated, snapshotFallback)
	if !ensureScopeAuthorized() {
		return
	}
	finish("DONE", "", text)
}

// authorizationCacheKey is the stable shape required for any future result
// cache. Callers must provide a hash for query/resource inputs; the current M1
// gateway does not enable an application result cache. Permission revisions
// are part of the key so a revoked or changed scope cannot reuse old data.
func authorizationCacheKey(tenant, user, permissionRevision, shop, resource, inputHash string) string {
	return strings.Join([]string{"ecr:v1", "auth", tenant, user, permissionRevision, shop, resource, inputHash}, ":")
}

func (g *gateway) getTurn(w http.ResponseWriter, r *http.Request) {
	if !g.authorizeExecution(w, r, r.PathValue("id")) {
		return
	}
	result, err := g.executionResult(r.Context(), r.PathValue("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "turn not found")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable", "could not load execution")
		return
	}
	if err = g.authority().Refresh(r.Context()); err != nil {
		writeError(w, authorizationHTTPStatus(err), auth.FailureCode(err), "authorization failed")
		return
	}
	writeJSON(w, 200, result)
}

func (g *gateway) events(w http.ResponseWriter, r *http.Request) {
	requestID := r.PathValue("id")
	if !g.authorizeExecution(w, r, requestID) {
		return
	}
	var executionID int64
	var deadline time.Time
	err := g.db.QueryRow(r.Context(), `SELECT e.id,e.deadline_at FROM conversation_execution e
		JOIN turn t ON t.tenant_id=e.tenant_id AND t.id=e.turn_id
		JOIN conversation c ON c.tenant_id=t.tenant_id AND c.id=t.conversation_id
		WHERE e.tenant_id=$1 AND e.request_id=$2 AND c.user_id=$3 AND c.shop_id=$4`,
		g.session.TenantID, requestID, g.session.UserID, g.session.Shop).Scan(&executionID, &deadline)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "turn not found")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable", "could not load events")
		return
	}
	lastID, _ := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
	if header := r.URL.Query().Get("last_event_id"); header != "" && lastID == 0 {
		lastID, _ = strconv.ParseInt(header, 10, 64)
	}
	stream := newSSEStream(w)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := g.authority().Refresh(r.Context()); err != nil {
			stream.errorFrameWhenFlushable(auth.FailureCode(err), "authorization failed", requestID, authorizationHTTPStatus(err))
			return
		}
		rows, queryErr := g.db.Query(r.Context(), `SELECT sequence,event_type,data_json::text FROM sse_event
			WHERE tenant_id=$1 AND execution_id=$2 AND sequence>$3 ORDER BY sequence`, g.session.TenantID, executionID, lastID)
		if queryErr != nil {
			return
		}
		count := 0
		for rows.Next() {
			var seq int64
			var kind, data string
			if rows.Scan(&seq, &kind, &data) != nil {
				rows.Close()
				return
			}
			if authErr := g.authority().Refresh(r.Context()); authErr != nil {
				rows.Close()
				stream.errorFrame(auth.FailureCode(authErr), "authorization failed", requestID, authorizationHTTPStatus(authErr))
				return
			}
			stream.frame(seq, kind, data)
			lastID, count = seq, count+1
		}
		rows.Close()
		if count > 0 {
			stream.flush()
		}
		var state string
		if err := g.db.QueryRow(r.Context(), `SELECT status FROM conversation_execution WHERE tenant_id=$1 AND id=$2`, g.session.TenantID, executionID).Scan(&state); err != nil {
			return
		}
		if terminal(state) && count == 0 {
			return
		}
		if !deadline.IsZero() && time.Now().After(deadline.Add(5*time.Second)) {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if count == 0 {
				stream.keepAlive()
			}
		}
	}
}

func (g *gateway) cancelTurn(w http.ResponseWriter, r *http.Request) {
	if !g.requireWriteRole(w) {
		return
	}
	requestID := r.PathValue("id")
	if !g.authorizeExecution(w, r, requestID) {
		return
	}
	tx, err := g.db.Begin(r.Context())
	if err != nil {
		writeError(w, 503, "database_unavailable", "could not cancel turn")
		return
	}
	defer tx.Rollback(r.Context())
	var state string
	var executionID, turnID int64
	err = tx.QueryRow(r.Context(), `SELECT e.status,e.id,e.turn_id FROM conversation_execution e
		JOIN turn t ON t.tenant_id=e.tenant_id AND t.id=e.turn_id JOIN conversation c ON c.tenant_id=t.tenant_id AND c.id=t.conversation_id
		WHERE e.tenant_id=$1 AND e.request_id=$2 AND c.user_id=$3 AND c.shop_id=$4 FOR UPDATE OF e`, g.session.TenantID, requestID, g.session.UserID, g.session.Shop).Scan(&state, &executionID, &turnID)
	if err != nil {
		writeError(w, 503, "database_unavailable", "could not cancel turn")
		return
	}
	if terminal(state) {
		writeJSON(w, 200, map[string]any{"request_id": requestID, "status": state})
		return
	}
	_, err = tx.Exec(r.Context(), `UPDATE conversation_execution SET cancel_requested=true,status='CANCEL_REQUESTED' WHERE tenant_id=$1 AND id=$2`, g.session.TenantID, executionID)
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE turn SET status='CANCEL_REQUESTED' WHERE tenant_id=$1 AND id=$2`, g.session.TenantID, turnID)
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		writeError(w, 503, "database_unavailable", "could not persist cancellation")
		return
	}
	registry := g.executionRegistryOwner()
	registry.mu.Lock()
	stop := registry.cancels[requestID]
	registry.mu.Unlock()
	if stop != nil {
		stop()
	}
	writeJSON(w, 200, map[string]any{"request_id": requestID, "status": "CANCEL_REQUESTED"})
}

func grpcFailure(ctx context.Context, err error) (string, string) {
	if errors.Is(ctx.Err(), context.Canceled) || status.Code(err) == codes.Canceled {
		return "CANCELLED", "cancelled"
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded {
		return "FAILED", "deadline_exceeded"
	}
	switch status.Code(err) {
	case codes.InvalidArgument:
		return "FAILED", "invalid_argument"
	case codes.PermissionDenied, codes.Unauthenticated:
		return "FAILED", "permission_denied"
	case codes.ResourceExhausted:
		return "FAILED", "rate_limited"
	case codes.Unavailable:
		return "FAILED", "rag_unavailable"
	default:
		return "FAILED", "rag_failed"
	}
}

func toEvidence(items []*ragv1.Evidence) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{"id": item.Id, "title": item.DocumentId, "snippet": item.Content,
			"sourceType": item.SourceType, "documentId": item.DocumentId, "versionId": item.VersionId,
			"sourceRef": item.SourceRef, "score": item.RawScore, "customerEligible": item.CustomerEligible, "citationIndex": item.Rank, "shopId": item.ShopId})
	}
	return out
}

func newRequestID() string { return fmt.Sprintf("v2-%d", time.Now().UnixNano()) }
func terminal(state string) bool {
	switch state {
	case "DONE", "FAILED", "CANCELLED", "ASKING":
		return true
	default:
		return false
	}
}
func first24(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > 24 {
		runes = runes[:24]
	}
	return string(runes)
}
func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return errors.New("request body too large")
	}
	return json.Unmarshal(data, dst)
}
func writeJSON(w http.ResponseWriter, statusCode int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, statusCode int, code, message string) {
	writeJSON(w, statusCode, apiError{Code: code, Message: message})
}
