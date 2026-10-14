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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const CookieName = "ecr_customer"

type Config struct {
	Origin            string
	AllowLoopbackHTTP bool
	VisitorRPM, IPRPM int
}
type Service struct {
	DB         *pgxpool.Pool
	Rag        ragv1.RagServiceClient
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
func (s *Service) originOK(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Origin") != s.Config.Origin {
		failure(w, 403, "origin_rejected")
		return false
	}
	return true
}
func (s *Service) resolve(ctx context.Context, r *http.Request) (visitor, error) {
	var v visitor
	cookie, err := r.Cookie(CookieName)
	if err != nil || len(cookie.Value) != 48 {
		return v, errors.New("customer_session_required")
	}
	err = s.DB.QueryRow(ctx, `SELECT cs.tenant_id,cs.shop_id,cs.customer_id,cs.id,cs.csrf_token
 FROM customer_session cs JOIN tenant t ON t.id=cs.tenant_id JOIN shop sh ON sh.tenant_id=cs.tenant_id AND sh.id=cs.shop_id
 WHERE cs.token_sha256=$1 AND cs.revoked_at IS NULL AND cs.expires_at>now() AND cs.idle_until>now()
 AND t.status='active' AND sh.status='active' AND sh.customer_chat_enabled`, Hash(cookie.Value)).Scan(&v.Tenant, &v.Shop, &v.Customer, &v.Session, &v.CSRF)
	if err == nil {
		_, err = s.DB.Exec(ctx, `UPDATE customer_session SET idle_until=LEAST(expires_at,now()+interval '24 hours') WHERE id=$1 AND idle_until<now()+interval '23 hours 55 minutes'`, v.Session)
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
	var name, welcome string
	err := s.DB.QueryRow(r.Context(), `SELECT sh.name,sh.welcome_message FROM shop sh JOIN tenant t ON t.id=sh.tenant_id
 WHERE sh.public_slug=$1 AND sh.customer_chat_enabled AND sh.status='active' AND t.status='active'`, r.PathValue("slug")).Scan(&name, &welcome)
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
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !s.allow("bootstrap:"+Hash(ip), 10) {
		failure(w, 429, "session_rate_limited")
		return
	}
	var tenant int64
	var shop string
	err := s.DB.QueryRow(r.Context(), `SELECT sh.tenant_id,sh.id FROM shop sh JOIN tenant t ON t.id=sh.tenant_id WHERE sh.public_slug=$1 AND sh.customer_chat_enabled AND sh.status='active' AND t.status='active'`, input.Shop).Scan(&tenant, &shop)
	if err != nil {
		failure(w, 404, "shop_unavailable")
		return
	}
	if v, e := s.resolve(r.Context(), r); e == nil && v.Tenant == tenant && v.Shop == shop {
		output(w, 200, map[string]any{"csrf_token": v.CSRF, "shop_id": shop})
		return
	}
	token, customer, session, csrf := ID(), ID(), ID(), ID()
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		failure(w, 503, "database_unavailable")
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `INSERT INTO customer_identity(id,tenant_id,shop_id) VALUES($1,$2,$3)`, customer, tenant, shop)
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO customer_session(id,tenant_id,shop_id,customer_id,token_sha256,csrf_token,expires_at,idle_until)
 VALUES($1,$2,$3,$4,$5,$6,now()+interval '7 days',now()+interval '24 hours')`, session, tenant, shop, customer, Hash(token), csrf)
	}
	if err != nil || tx.Commit(r.Context()) != nil {
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
	id := Hash(v.Customer + ":" + key(r))[:48]
	_, err := s.DB.Exec(r.Context(), `INSERT INTO customer_conversation(id,tenant_id,shop_id,customer_id) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO NOTHING`, id, v.Tenant, v.Shop, v.Customer)
	if err != nil {
		failure(w, 503, "database_unavailable")
		return
	}
	output(w, 201, map[string]string{"conversation_id": id})
}
func (s *Service) listConversations(w http.ResponseWriter, r *http.Request) {
	v, resolveErr := s.resolve(r.Context(), r)
	if resolveErr != nil {
		failure(w, 401, "customer_session_required")
		return
	}
	rows, err := s.DB.Query(r.Context(), `SELECT id,state,epoch FROM customer_conversation WHERE tenant_id=$1 AND shop_id=$2 AND customer_id=$3 ORDER BY updated_at DESC LIMIT 100`, v.Tenant, v.Shop, v.Customer)
	if err != nil {
		failure(w, 503, "database_unavailable")
		return
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var id, state string
		var epoch int64
		if rows.Scan(&id, &state, &epoch) != nil {
			failure(w, 503, "database_unavailable")
			return
		}
		result = append(result, map[string]any{"id": id, "state": state, "epoch": epoch})
	}
	output(w, 200, map[string]any{"conversations": result})
}

const conversationSQL = `SELECT id,tenant_id,shop_id,customer_id,state,epoch,owner_user_id,lease_until,active_execution,clarification_count FROM customer_conversation WHERE id=$1`

func scanConversation(row pgx.Row) (conversation, error) {
	var c conversation
	err := row.Scan(&c.ID, &c.Tenant, &c.Shop, &c.Customer, &c.State, &c.Epoch, &c.Owner, &c.Lease, &c.Active, &c.Clarifications)
	return c, err
}
func (s *Service) visitorConversation(ctx context.Context, r *http.Request) (visitor, conversation, error) {
	v, err := s.resolve(ctx, r)
	if err != nil {
		return v, conversation{}, err
	}
	c, err := scanConversation(s.DB.QueryRow(ctx, conversationSQL, r.PathValue("id")))
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
	output(w, 200, map[string]any{"conversation": c, "messages": messages})
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
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
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
		rows, err := s.DB.Query(r.Context(), `SELECT sequence,event_type,data_json FROM customer_event WHERE tenant_id=$1 AND conversation_id=$2 AND sequence>$3 ORDER BY sequence LIMIT 100`, c.Tenant, c.ID, last)
		if err != nil {
			return
		}
		type event struct {
			seq  int64
			kind string
			data []byte
		}
		batch := []event{}
		for rows.Next() {
			var e event
			if rows.Scan(&e.seq, &e.kind, &e.data) != nil {
				rows.Close()
				return
			}
			batch = append(batch, e)
		}
		err = rows.Err()
		rows.Close()
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
	var source, content string
	var citations []byte
	var created time.Time
	err := s.DB.QueryRow(ctx, `SELECT source,content,citations_json,created_at FROM customer_message WHERE tenant_id=$1 AND conversation_id=$2 AND id=$3`, c.Tenant, c.ID, id).Scan(&source, &content, &citations, &created)
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
		var title string
		if err = s.DB.QueryRow(ctx, `SELECT title FROM document WHERE tenant_id=$1 AND logical_key=$2`, c.Tenant, item.DocumentId).Scan(&title); err != nil {
			return nil, err
		}
		view = append(view, map[string]any{"evidence_id": item.Id, "document_id": item.DocumentId, "version_id": item.VersionId, "citation_index": item.Rank, "title": title, "snippet": item.Content})
	}
	if redacted {
		content = "此回答的来源已撤回或权限已变化，请重新咨询或联系人工客服。"
		view = []map[string]any{}
	}
	return map[string]any{"id": id, "source": source, "text": content, "citations": view, "redacted": redacted, "created_at": created}, nil
}
func (s *Service) messages(ctx context.Context, c conversation) ([]map[string]any, error) {
	rows, err := s.DB.Query(ctx, `SELECT id FROM customer_message WHERE tenant_id=$1 AND conversation_id=$2 ORDER BY created_at DESC,id DESC LIMIT 500`, c.Tenant, c.ID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := []map[string]any{}
	for i := len(ids) - 1; i >= 0; i-- {
		id := ids[i]
		message, err := s.publicMessage(ctx, c, id)
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
		var owned bool
		_ = s.DB.QueryRow(r.Context(), `SELECT state='HUMAN' AND owner_user_id=$2 AND owner_session_hash=$3 AND lease_until>now() FROM customer_conversation WHERE id=$1`, c.ID, staff.User, staff.SessionHash).Scan(&owned)
		output(w, 200, map[string]any{"conversation": c, "messages": messages, "can_reply": owned})
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
	c, err := scanConversation(s.DB.QueryRow(ctx, conversationSQL, r.PathValue("id")))
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
	rows, err := s.DB.Query(r.Context(), `SELECT id,state,epoch,owner_user_id,lease_until,updated_at FROM customer_conversation WHERE tenant_id=$1 AND shop_id=ANY($2::text[]) AND (state='WAITING_HUMAN' OR (state='HUMAN' AND owner_user_id=$3)) ORDER BY updated_at LIMIT 100`, staff.Tenant, staff.Shops, staff.User)
	if err != nil {
		failure(w, 503, "database_unavailable")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, state string
		var epoch int64
		var owner *int64
		var lease *time.Time
		var updated time.Time
		if rows.Scan(&id, &state, &epoch, &owner, &lease, &updated) != nil {
			failure(w, 503, "database_unavailable")
			return
		}
		items = append(items, map[string]any{"id": id, "state": state, "epoch": epoch, "owner_user_id": owner, "lease_until": lease, "updated_at": updated})
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
