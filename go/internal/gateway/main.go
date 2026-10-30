package gateway

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	"github.com/ggb5023/EcommerceRAG/go/internal/customer"
	"github.com/ggb5023/EcommerceRAG/go/internal/edge"
	"github.com/ggb5023/EcommerceRAG/go/internal/facts"
	"github.com/ggb5023/EcommerceRAG/go/internal/identity"
	"github.com/ggb5023/EcommerceRAG/go/internal/localauth"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/domain"
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
	return domain.RoutedOrderResource(response, query)
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
	runtime, closeAll := buildGateway()
	defer closeAll()
	mountRoutes(runtime)
	serve(runtime)
}

// gatewayRuntime carries the values the mount and serve phases need.
type gatewayRuntime struct {
	ctx     context.Context
	g       *gateway
	browser *identity.Browser
	addr    string
	handler http.Handler
	secure  bool
}

// buildGateway wires the dependencies and starts everything that must outlive a
// request: execution ownership, the customer channel, the authority RPC and its
// watchdog. The returned function closes them in reverse order.
func buildGateway() (*gatewayRuntime, func()) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	g, db, profile, browser, closeWired := wireDependencies(ctx)
	runtime := &gatewayRuntime{ctx: ctx, g: g, browser: browser}
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	runtime.addr = addr
	if err := validateDeploymentForm(os.Getenv("WEB_ROOT"), addr, os.Getenv("V2_CUSTOMER_PROXY_HOST")); err != nil {
		log.Fatal(err.Error())
	}
	owner, err := g.acquireExecutionOwner(ctx)
	if err != nil {
		log.Fatal("Gateway execution ownership unavailable")
	}
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
	cleanup := func() {
		if authorityRPC != nil {
			authorityRPC.Stop()
		}
		owner.Close(context.Background())
		closeWired()
		cancel()
	}
	return runtime, cleanup
}

// mountRoutes builds the handler chain, adding the built-in edge for D1.
func mountRoutes(runtime *gatewayRuntime) {
	g := runtime.g
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
		if runtime.browser != nil {
			target := strings.TrimSpace(os.Getenv("V2_IDENTITY_PROXY_URL"))
			if target == "" {
				target = "http://keycloak:8080"
			}
			identityProxy = edge.Proxy(target)
		}
		handler = edge.Routes(handler, identityProxy, edge.SPA(root, ""), authority)
		secure = true
	}
	runtime.handler = handler
	runtime.secure = secure
}

// serve binds the listener, recovers interrupted executions and serves.
func serve(runtime *gatewayRuntime) {
	g := runtime.g
	server := &http.Server{Addr: runtime.addr, Handler: runtime.handler, ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	if runtime.secure {
		server.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS13}
	}
	listener, err := net.Listen("tcp", runtime.addr)
	if err != nil {
		log.Fatal("Gateway bind failed")
	}
	if recovered, recoveryErr := g.recoverInterruptedExecutions(runtime.ctx); recoveryErr != nil {
		_ = listener.Close()
		log.Fatal("Gateway interrupted execution recovery failed")
	} else if recovered > 0 {
		log.Printf("Gateway recovered %d interrupted executions", recovered)
	}
	g.ready.Store(true)
	log.Printf("V2 Gateway listening on %s", runtime.addr)
	if runtime.secure {
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
	g.registerHealthRoutes(mux)
	g.registerCustomerRoutes(mux)
	g.registerAdminRoutes(mux)
	return mux
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

// resumeTurn continues the existing v2 ASKING turn with a new execution.
// The original turn and checkpoint thread remain stable; only Go creates the
// new request/execution identity and Python consumes the minimal checkpoint.

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
	writeEvent, finish := newTurnSink(ctx, g.db, ident, mockOutput)
	guard := turnGuard{ctx: ctx, authority: g.authority(), finish: finish}
	if !guard.authorized() {
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
	ensureScopeAuthorized := func() bool { return guard.scopeAuthorized(base) }
	if parentTurnID != 0 && isV2Profile(g.profile) {
		parentScope, scopeErr := g.authority().IssueScopeForTurn(ctx, requestID, conversationID, parentTurnID, parentExecutionID)
		if scopeErr != nil {
			finish("FAILED", auth.FailureCode(scopeErr), "")
			return
		}
		if resumeFailure := resumeParentCheckpoint(ctx, g.rag, parentScope); resumeFailure != nil {
			finish(resumeFailure.state, resumeFailure.code, "")
			return
		}
	}

	history := loadHistorySummary(ctx, g.db, g.session.TenantID, g.authority().CurrentRevision(), conversationID, turnID)
	routed, routeFailure := understandTurn(ctx, g.rag, base, query, history)
	if routeFailure != nil {
		finish(routeFailure.state, routeFailure.code, "")
		return
	}
	factResourceID, snapshotFallback := resolveFactIntent(routed, query, history)
	if !ensureScopeAuthorized() {
		return
	}
	factResults, factFailure, factAborted := runFactStage(ctx, factProvider{provider: g.facts}, g.db,
		g.session.TenantID, requestID, routed.RequiredFactTools, factResourceID, base,
		ensureScopeAuthorized, writeEvent)
	if factAborted {
		return
	}
	if factFailure != nil {
		finish(factFailure.state, factFailure.code, "")
		return
	}
	if decision := decideClarification(routed, snapshotFallback); decision.Needed {
		if !ensureScopeAuthorized() {
			return
		}
		if checkpointFailure := saveAskingCheckpoint(ctx, g.rag, g.profile, base); checkpointFailure != nil {
			finish(checkpointFailure.state, checkpointFailure.code, "")
			return
		}
		finish("ASKING", "", decision.Text)
		return
	}
	if !ensureScopeAuthorized() {
		return
	}
	evidence, retrievalFailure, retrievalAborted := runRetrievalStage(ctx, g.rag, g.db, g.session.TenantID, executionID,
		base, routed.RewrittenQuery, mockOutput, ensureScopeAuthorized,
		func(item *ragv1.Evidence) (bool, error) { return g.authority().EvidenceAllowed(ctx, item) })
	if retrievalAborted {
		return
	}
	if retrievalFailure != nil {
		finish(retrievalFailure.state, retrievalFailure.code, "")
		return
	}
	if snapshotFallback && len(evidence) == 0 {
		if checkpointFailure := saveAskingCheckpoint(ctx, g.rag, g.profile, base); checkpointFailure != nil {
			finish(checkpointFailure.state, checkpointFailure.code, "")
			return
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
	answer, generateFailure, generateAborted := runGenerationStage(ctx, g.rag, g.db, g.session.TenantID, executionID,
		&ragv1.GenerateRequest{Context: base, Query: query, Evidence: evidence, Facts: factResults,
			ResponsePurpose: "customer_reply", HistorySummary: history},
		mockOutput, snapshotFallback, evidence, ensureScopeAuthorized,
		func(item *ragv1.Evidence) (bool, error) { return g.authority().EvidenceAllowed(ctx, item) }, writeEvent)
	if generateAborted {
		return
	}
	if generateFailure != nil {
		finish(generateFailure.state, generateFailure.code, "")
		return
	}
	if !ensureScopeAuthorized() {
		return
	}
	finish("DONE", "", answer)
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
