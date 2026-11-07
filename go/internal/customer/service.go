// Package customer owns the customer channel and human conversation control.
// It never writes ingestion tables or trusts an AI recommendation as authority.
package customer

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	"github.com/ggb5023/EcommerceRAG/go/internal/domain"
	"github.com/ggb5023/EcommerceRAG/go/internal/facts"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5/pgxpool"
)

const CookieName = "ecr_customer"

// orderIDPattern mirrors the CHECK constraint in migration 0058. The order is
// bound at conversation creation and is never accepted from the model.
var orderIDPattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{3,39}$`)

// skuPattern mirrors the resource binding the facts contract accepts.
var skuPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var skuTokenPattern = regexp.MustCompile(`[A-Za-z0-9]+[-_][A-Za-z0-9_-]+`)

// normalizeServiceLane applies the two-lane rule: pre-sales never carries an
// order and after-sales always does. The returned code is an API error code.
func normalizeServiceLane(serviceType, orderID string) (string, string, string) {
	serviceType = strings.TrimSpace(serviceType)
	if serviceType == "" {
		serviceType = "pre_sales"
	}
	if serviceType != "pre_sales" && serviceType != "after_sales" {
		return "", "", "invalid_service_type"
	}
	orderID = strings.ToUpper(strings.TrimSpace(orderID))
	if serviceType == "pre_sales" {
		if orderID != "" {
			return "", "", "invalid_order_id"
		}
		return serviceType, "", ""
	}
	if !orderIDPattern.MatchString(orderID) {
		return "", "", "invalid_order_id"
	}
	return serviceType, orderID, ""
}

type Config struct {
	TrustedProxyIPs   []net.IP
	Origin            string
	AllowLoopbackHTTP bool
	VisitorRPM, IPRPM int
}
type Service struct {
	DB         *pgxpool.Pool
	Rag        ragv1.RagServiceClient
	Facts      facts.Provider
	SigningKey []byte
	Profile    string
	Config     Config
	mu         sync.Mutex
	limits     map[string]counter
	cancels    map[string]activeRun
	aiSlots    chan struct{}
}
type activeRun struct {
	execution string
	cancel    context.CancelFunc
}
type counter struct {
	start time.Time
	count int
}
type visitor struct {
	Tenant                        int64
	Shop, Customer, Session, CSRF string
}
type conversation struct {
	ID             string     `json:"id"`
	Tenant         int64      `json:"-"`
	Shop           string     `json:"shop_id"`
	Customer       string     `json:"-"`
	State          string     `json:"state"`
	Epoch          int64      `json:"epoch"`
	ServiceType    string     `json:"service_type"`
	OrderID        string     `json:"order_id,omitempty"`
	Owner          *int64     `json:"owner_user_id,omitempty"`
	Lease          *time.Time `json:"lease_until,omitempty"`
	Active         *string    `json:"active_execution_id,omitempty"`
	Clarifications int        `json:"-"`
}
type Staff struct {
	Tenant, User      int64
	Shops             []string
	Role, SessionHash string
	Check             func(context.Context) error
}

func New(db *pgxpool.Pool, rag ragv1.RagServiceClient, key []byte, profile string, config Config) (*Service, error) {
	u, err := url.Parse(config.Origin)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("customer origin must be an exact origin")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && config.AllowLoopbackHTTP && ip != nil && ip.IsLoopback()) {
		return nil, errors.New("customer origin requires HTTPS")
	}
	if config.VisitorRPM == 0 {
		config.VisitorRPM = 5
	}
	if config.IPRPM == 0 {
		config.IPRPM = 20
	}
	if config.VisitorRPM < 1 || config.IPRPM < 1 {
		return nil, errors.New("customer limits must be positive")
	}
	return &Service{DB: db, Rag: rag, SigningKey: key, Profile: profile, Config: config, limits: map[string]counter{}, cancels: map[string]activeRun{}, aiSlots: make(chan struct{}, 4)}, nil
}
func ID() string {
	data := make([]byte, 24)
	if _, err := rand.Read(data); err != nil {
		panic("secure randomness unavailable")
	}
	return hex.EncodeToString(data)
}
func Hash(s string) string { digest := sha256.Sum256([]byte(s)); return hex.EncodeToString(digest[:]) }
func StaffSessionHash(r *http.Request) string {
	if cookie, err := r.Cookie("ecr_v2_session"); err == nil {
		return Hash(cookie.Value)
	}
	return Hash(r.Header.Get("Authorization"))
}
func output(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}
func failure(w http.ResponseWriter, code int, reason string) {
	output(w, code, map[string]string{"code": reason, "message": reason})
}
func body(w http.ResponseWriter, r *http.Request, dst any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(dst) != nil || decoder.Decode(new(any)) != io.EOF {
		failure(w, 400, "invalid_request")
		return false
	}
	return true
}
func key(r *http.Request) string {
	k := r.Header.Get("Idempotency-Key")
	if len(k) < 1 || len(k) > 128 || strings.ContainsAny(k, "\r\n\t ") {
		return ""
	}
	return k
}
func (s *Service) allow(bucket string, max int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if len(s.limits) > 8192 {
		for id, v := range s.limits {
			if now.Sub(v.start) >= time.Minute {
				delete(s.limits, id)
			}
		}
		if len(s.limits) > 8192 {
			return false
		}
	}
	v := s.limits[bucket]
	if now.Sub(v.start) >= time.Minute {
		v = counter{start: now}
	}
	v.count++
	s.limits[bucket] = v
	return v.count <= max
}
func (s *Service) remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "invalid"
	}
	remote := net.ParseIP(host)
	for _, trusted := range s.Config.TrustedProxyIPs {
		if trusted.Equal(remote) {
			if ip := net.ParseIP(r.Header.Get("X-Real-IP")); ip != nil {
				return ip.String()
			}
			return "invalid"
		}
	}
	return host
}
func (s *Service) originOK(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Origin") != s.Config.Origin {
		failure(w, 403, "origin_rejected")
		return false
	}
	return true
}
func (s *Service) resolve(ctx context.Context, r *http.Request) (visitor, error) {
	cookie, err := r.Cookie(CookieName)
	if err != nil || len(cookie.Value) != 48 {
		return visitor{}, errors.New("customer_session_required")
	}
	v, err := customerSessionByToken(ctx, s.DB, Hash(cookie.Value))
	if err == nil {
		err = touchCustomerSession(ctx, s.DB, v.Session)
	}
	return v, err
}

func (s *Service) Routes() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /v1/customer/shops/{slug}", s.shop)
	m.HandleFunc("POST /v1/customer/session", s.createSession)
	m.HandleFunc("GET /v1/customer/session", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.resolve(r.Context(), r)
		if e != nil {
			failure(w, 401, "customer_session_required")
			return
		}
		output(w, 200, map[string]any{"authenticated": true, "csrf_token": v.CSRF, "shop_id": v.Shop})
	})
	m.HandleFunc("POST /v1/customer/conversations", s.createConversation)
	m.HandleFunc("GET /v1/customer/conversations", s.listConversations)
	m.HandleFunc("GET /v1/customer/conversations/{id}", s.getConversation)
	m.HandleFunc("POST /v1/customer/conversations/{id}/messages", s.send)
	m.HandleFunc("POST /v1/customer/conversations/{id}/handoff", s.handoff)
	m.HandleFunc("GET /v1/customer/conversations/{id}/events", s.events)
	m.HandleFunc("GET /v1/customer/conversations/{id}/citations/{message}", s.citations)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != "GET" && !s.originOK(w, r) {
			return
		}
		if r.URL.Path != "/v1/customer/session" && !strings.HasPrefix(r.URL.Path, "/v1/customer/shops/") {
			v, err := s.resolve(r.Context(), r)
			if err != nil {
				failure(w, 401, "customer_session_required")
				return
			}
			if r.Method != "GET" && subtle.ConstantTimeCompare([]byte(v.CSRF), []byte(r.Header.Get("X-CSRF-Token"))) != 1 {
				failure(w, 403, "csrf_rejected")
				return
			}
			if r.Method != "GET" && key(r) == "" {
				failure(w, 400, "idempotency_key_required")
				return
			}
		}
		m.ServeHTTP(w, r)
	})
}
func (s *Service) shop(w http.ResponseWriter, r *http.Request) {
	name, welcome, err := publicShop(r.Context(), s.DB, r.PathValue("slug"))
	if err != nil {
		failure(w, 404, "shop_unavailable")
		return
	}
	output(w, 200, map[string]string{"name": name, "welcome_message": welcome})
}
func (s *Service) createSession(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Shop string `json:"shop_slug"`
	}
	if !body(w, r, &input) {
		return
	}
	ip := s.remoteIP(r)
	if !s.allow("bootstrap:"+Hash(ip), 10) {
		failure(w, 429, "session_rate_limited")
		return
	}
	tenant, shop, err := sessionShop(r.Context(), s.DB, input.Shop)
	if err != nil {
		failure(w, 404, "shop_unavailable")
		return
	}
	if v, e := s.resolve(r.Context(), r); e == nil && v.Tenant == tenant && v.Shop == shop {
		output(w, 200, map[string]any{"csrf_token": v.CSRF, "shop_id": shop})
		return
	}
	token, customer, session, csrf := ID(), ID(), ID(), ID()
	if err = createCustomerSession(r.Context(), s.DB, tenant, shop, customer, session, Hash(token), csrf); err != nil {
		failure(w, 503, "database_unavailable")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: token, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(s.Config.Origin, "https://"), SameSite: http.SameSiteLaxMode, MaxAge: 7 * 24 * 60 * 60})
	output(w, 201, map[string]any{"csrf_token": csrf, "shop_id": shop})
}
func (s *Service) createConversation(w http.ResponseWriter, r *http.Request) {
	v, resolveErr := s.resolve(r.Context(), r)
	if resolveErr != nil {
		failure(w, 401, "customer_session_required")
		return
	}
	var input struct {
		ServiceType string `json:"service_type"`
		OrderID     string `json:"order_id"`
	}
	if !body(w, r, &input) {
		return
	}
	serviceType, orderID, code := normalizeServiceLane(input.ServiceType, input.OrderID)
	if code != "" {
		failure(w, 400, code)
		return
	}
	id := Hash(v.Customer + ":" + key(r))[:48]
	created, existingType, existingOrder, err := upsertCustomerConversation(r.Context(), s.DB, id, v.Tenant, v.Shop, v.Customer, serviceType, orderID)
	if err != nil {
		failure(w, 503, "database_unavailable")
		return
	}
	if !created && (existingType != serviceType || existingOrder != orderID) {
		failure(w, 409, "conversation_idempotency_conflict")
		return
	}
	output(w, 201, map[string]string{"conversation_id": id, "service_type": serviceType, "order_id": orderID})
}
func (s *Service) listConversations(w http.ResponseWriter, r *http.Request) {
	v, resolveErr := s.resolve(r.Context(), r)
	if resolveErr != nil {
		failure(w, 401, "customer_session_required")
		return
	}
	result, err := listCustomerConversations(r.Context(), s.DB, v.Tenant, v.Shop, v.Customer)
	if err != nil {
		failure(w, 503, "database_unavailable")
		return
	}
	output(w, 200, map[string]any{"conversations": result})
}

func (s *Service) visitorConversation(ctx context.Context, r *http.Request) (visitor, conversation, error) {
	v, err := s.resolve(ctx, r)
	if err != nil {
		return v, conversation{}, err
	}
	c, err := loadConversation(ctx, s.DB, r.PathValue("id"))
	if err != nil || c.Tenant != v.Tenant || c.Shop != v.Shop || c.Customer != v.Customer {
		return v, c, errors.New("not_found")
	}
	return v, c, nil
}
func (s *Service) getConversation(w http.ResponseWriter, r *http.Request) {
	_, c, err := s.visitorConversation(r.Context(), r)
	if err != nil {
		failure(w, 404, "not_found")
		return
	}
	messages, err := s.messages(r.Context(), c)
	if err != nil {
		failure(w, 503, "database_unavailable")
		return
	}
	view := map[string]any{"conversation": c, "messages": messages}
	ticket, err := activeTicket(r.Context(), s.DB, c.Tenant, c.ID)
	if err != nil {
		failure(w, 503, "database_unavailable")
		return
	}
	if ticket != nil {
		view["ticket"] = ticket
	}
	output(w, 200, view)
}
func (s *Service) citations(w http.ResponseWriter, r *http.Request) {
	_, c, err := s.visitorConversation(r.Context(), r)
	if err != nil {
		failure(w, 404, "not_found")
		return
	}
	message, err := s.publicMessage(r.Context(), c, r.PathValue("message"))
	if err != nil {
		failure(w, 404, "not_found")
		return
	}
	output(w, 200, map[string]any{"citations": message["citations"]})
}
func (s *Service) send(w http.ResponseWriter, r *http.Request) {
	v, c, err := s.visitorConversation(r.Context(), r)
	if err != nil {
		failure(w, 404, "not_found")
		return
	}
	var input struct {
		Text string `json:"text"`
	}
	if !body(w, r, &input) {
		return
	}
	input.Text = strings.TrimSpace(input.Text)
	if input.Text == "" || len(input.Text) > 8192 {
		failure(w, 400, "invalid_text")
		return
	}
	ip := s.remoteIP(r)
	if !s.allow("visitor:"+v.Customer, s.Config.VisitorRPM) || !s.allow("ip:"+Hash(ip), s.Config.IPRPM) {
		failure(w, 429, "message_rate_limited")
		return
	}
	id, execution, replay, err := s.submit(r.Context(), v, c.ID, key(r), input.Text)
	if err != nil {
		failure(w, 409, "conversation_conflict")
		return
	}
	if execution != "" && !replay {
		go s.run(execution, c.ID, v.Session, input.Text)
	}
	output(w, 202, map[string]any{"message_id": id, "execution_id": execution, "conversation_id": c.ID, "replayed": replay})
}
func (s *Service) handoff(w http.ResponseWriter, r *http.Request) {
	v, c, err := s.visitorConversation(r.Context(), r)
	if err != nil {
		failure(w, 404, "not_found")
		return
	}
	result, err := s.change(r.Context(), c.ID, "customer:"+v.Customer, key(r), "handoff", 0, "", 0)
	if err != nil {
		failure(w, 409, "conversation_conflict")
		return
	}
	s.cancel(c.ID)
	output(w, 200, result)
}

func (s *Service) events(w http.ResponseWriter, r *http.Request) {
	_, c, err := s.visitorConversation(r.Context(), r)
	if err != nil {
		failure(w, 404, "not_found")
		return
	}
	s.stream(w, r, c, func() bool { _, _, e := s.visitorConversation(r.Context(), r); return e == nil })
}
func (s *Service) stream(w http.ResponseWriter, r *http.Request, c conversation, authorized func() bool) {
	last := int64(0)
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		var err error
		last, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || last < 0 {
			failure(w, 400, "invalid_event_id")
			return
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	flush, ok := w.(http.Flusher)
	if !ok {
		return
	}
	w.WriteHeader(200)
	flush.Flush()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(25 * time.Second)
	defer deadline.Stop()
	for {
		if !authorized() {
			return
		}
		batch, err := loadCustomerEvents(r.Context(), s.DB, c.Tenant, c.ID, last, 100)
		if err != nil {
			return
		}
		for _, e := range batch {
			var data map[string]any
			if json.Unmarshal(e.data, &data) != nil {
				return
			}
			if id, ok := data["message_id"].(string); ok {
				message, err := s.publicMessage(r.Context(), c, id)
				if err != nil {
					return
				}
				data["message"] = message
			}
			data["seq"] = e.seq
			encoded, _ := json.Marshal(data)
			if _, err = io.WriteString(w, "id: "+strconv.FormatInt(e.seq, 10)+"\nevent: "+e.kind+"\ndata: "+string(encoded)+"\n\n"); err != nil {
				return
			}
			last = e.seq
		}
		flush.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			return
		case <-ticker.C:
		}
	}
}

// All data used here is rechecked at read time, including persisted SSE replay.
func (s *Service) publicMessage(ctx context.Context, c conversation, id string) (map[string]any, error) {
	source, content, citations, created, state, behavior, mock, facts, err := loadCustomerMessage(ctx, s.DB, c.Tenant, c.ID, id)
	if err != nil {
		return nil, err
	}
	items := []*ragv1.Evidence{}
	if json.Unmarshal(citations, &items) != nil {
		return nil, errors.New("invalid_citations")
	}
	view := []map[string]any{}
	redacted := false
	for _, item := range items {
		allowed, err := auth.CustomerEvidenceAllowed(ctx, s.DB, c.Tenant, c.Shop, item)
		if err != nil {
			return nil, err
		}
		if !allowed {
			redacted = true
			break
		}
		title, err := loadDocumentTitle(ctx, s.DB, c.Tenant, item.DocumentId)
		if err != nil {
			return nil, err
		}
		view = append(view, map[string]any{"evidence_id": item.Id, "document_id": item.DocumentId, "version_id": item.VersionId, "citation_index": item.Rank, "title": title, "snippet": item.Content})
	}
	if redacted {
		content = "此回答的来源已撤回或权限已变化，请重新咨询或联系人工客服。"
		view = []map[string]any{}
	}
	canCopy, blocked := false, "not_copyable"
	if source == "assistant" {
		canCopy, blocked = publicCopyDecision(state, behavior, len(view), nonMockFactCount(facts), mock, redacted)
	}
	return map[string]any{"id": id, "source": source, "text": content, "citations": view, "redacted": redacted, "created_at": created,
		"can_copy": canCopy, "blocked_reason": blocked}, nil
}

// nonMockFactCount counts usable, non-mock fact snapshots. A fact without a
// tool_name is not a fact; a missing is_mock is a real (non-mock) fact.
func nonMockFactCount(raw []byte) int {
	var saved []struct {
		ToolName string `json:"tool_name"`
		IsMock   bool   `json:"is_mock"`
	}
	if json.Unmarshal(raw, &saved) != nil {
		return 0
	}
	count := 0
	for _, fact := range saved {
		if fact.ToolName != "" && !fact.IsMock {
			count++
		}
	}
	return count
}

// publicCopyDecision keeps redacted answers uncopyable even when a fact
// snapshot survives; the displayed content is a withdrawal notice, not an answer.
func publicCopyDecision(state, behavior string, citations, nonMockFacts int, mock, redacted bool) (bool, string) {
	if redacted {
		return false, "not_copyable"
	}
	return domain.CanCopyReply(state, behavior, citations, nonMockFacts, mock)
}

func (s *Service) messages(ctx context.Context, c conversation) ([]map[string]any, error) {
	ids, err := loadCustomerMessageIDs(ctx, s.DB, c.Tenant, c.ID)
	if err != nil {
		return nil, err
	}
	result := []map[string]any{}
	for i := len(ids) - 1; i >= 0; i-- {
		message, err := s.publicMessage(ctx, c, ids[i])
		if err != nil {
			return nil, err
		}
		result = append(result, message)
	}
	return result, nil
}
func (s *Service) cancel(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if run, ok := s.cancels[id]; ok {
		run.cancel()
	}
}

func (s *Service) StaffRoutes(staff Staff) http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /v1/support/queue", func(w http.ResponseWriter, r *http.Request) { s.queue(w, r, staff) })
	m.HandleFunc("GET /v1/support/conversations/{id}", func(w http.ResponseWriter, r *http.Request) {
		c, err := s.staffConversation(r.Context(), r, staff)
		if err != nil {
			failure(w, 404, "not_found")
			return
		}
		messages, err := s.messages(r.Context(), c)
		if err != nil {
			failure(w, 503, "database_unavailable")
			return
		}
		owned, _ := customerConversationOwned(r.Context(), s.DB, c.ID, staff.User, staff.SessionHash)
		view := map[string]any{"conversation": c, "messages": messages, "can_reply": owned}
		ticket, err := activeTicket(r.Context(), s.DB, c.Tenant, c.ID)
		if err != nil {
			failure(w, 503, "database_unavailable")
			return
		}
		if ticket != nil {
			view["ticket"] = ticket
		}
		output(w, 200, view)
	})
	m.HandleFunc("GET /v1/support/conversations/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		c, err := s.staffConversation(r.Context(), r, staff)
		if err != nil {
			failure(w, 404, "not_found")
			return
		}
		s.stream(w, r, c, func() bool { _, err := s.staffConversation(r.Context(), r, staff); return err == nil })
	})
	m.HandleFunc("POST /v1/support/conversations/{id}/{action}", func(w http.ResponseWriter, r *http.Request) { s.staffAction(w, r, staff) })
	return m
}
func (s *Service) staffConversation(ctx context.Context, r *http.Request, staff Staff) (conversation, error) {
	if staff.Check != nil {
		if err := staff.Check(ctx); err != nil {
			return conversation{}, err
		}
	}
	c, err := loadConversation(ctx, s.DB, r.PathValue("id"))
	allowed := false
	for _, shop := range staff.Shops {
		allowed = allowed || shop == c.Shop
	}
	if err != nil || staff.Role == "viewer" || c.Tenant != staff.Tenant || !allowed {
		return c, errors.New("not_found")
	}
	return c, nil
}
func (s *Service) queue(w http.ResponseWriter, r *http.Request, staff Staff) {
	if staff.Check != nil && staff.Check(r.Context()) != nil {
		failure(w, 403, "permission_changed")
		return
	}
	items, err := loadStaffQueue(r.Context(), s.DB, staff.Tenant, staff.Shops, staff.User)
	if err != nil {
		failure(w, 503, "database_unavailable")
		return
	}
	output(w, 200, map[string]any{"conversations": items})
}
func (s *Service) staffAction(w http.ResponseWriter, r *http.Request, staff Staff) {
	c, err := s.staffConversation(r.Context(), r, staff)
	if err != nil {
		failure(w, 404, "not_found")
		return
	}
	if key(r) == "" {
		failure(w, 400, "idempotency_key_required")
		return
	}
	var input struct {
		Epoch int64  `json:"epoch"`
		Text  string `json:"text,omitempty"`
	}
	if !body(w, r, &input) {
		return
	}
	action := r.PathValue("action")
	if action == "messages" {
		id, err := s.humanMessage(r.Context(), c.ID, staff, key(r), input.Epoch, strings.TrimSpace(input.Text))
		if err != nil {
			failure(w, 409, "claim_stale")
			return
		}
		output(w, 201, map[string]any{"message_id": id})
		return
	}
	if action == "requeue" && staff.Role != "owner" && staff.Role != "admin" {
		failure(w, 403, "role_forbidden")
		return
	}
	result, err := s.change(r.Context(), c.ID, "staff:"+strconv.FormatInt(staff.User, 10), key(r), action, staff.User, staff.SessionHash, input.Epoch)
	if err != nil {
		failure(w, 409, "claim_stale")
		return
	}
	output(w, 200, result)
}

// LastCustomerMessage returns the latest customer message for a support
// conversation after the same staff tenant/shop/role checks as the staff view.
func (s *Service) LastCustomerMessage(ctx context.Context, staff Staff, conversationID string) (string, error) {
	if staff.Check != nil {
		if err := staff.Check(ctx); err != nil {
			return "", err
		}
	}
	if staff.Role == "viewer" {
		return "", ErrSupportConversationNotFound
	}
	tenantID, shop, err := loadConversationScope(ctx, s.DB, conversationID)
	if err != nil {
		return "", ErrSupportConversationNotFound
	}
	allowed := false
	for _, candidate := range staff.Shops {
		allowed = allowed || candidate == shop
	}
	if tenantID != staff.Tenant || !allowed {
		return "", ErrSupportConversationNotFound
	}
	return lastCustomerMessage(ctx, s.DB, tenantID, shop, conversationID)
}
