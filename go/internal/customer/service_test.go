package customer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/commercesim"
	"github.com/ggb5023/EcommerceRAG/go/internal/facts"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCustomerOriginAndDeliveryFailClosed(t *testing.T) {
	for _, origin := range []string{"http://shop.example", "https://shop.example/path", "https://user@shop.example", "https://shop.example?x=1"} {
		if _, err := New(nil, nil, nil, "", Config{Origin: origin}); err == nil {
			t.Fatalf("accepted invalid origin %s", origin)
		}
	}
	e := &ragv1.Evidence{Id: "fixture", Rank: 5, CustomerEligible: true, DisclosureClass: "external_allowed"}
	p := proposal{Answer: "有证据的说明", Decision: &ragv1.ReplyDecision{Behavior: ragv1.ReplyBehavior_REPLY_BEHAVIOR_ANSWER, ReasonCode: "grounded", EvidenceIds: []string{"fixture"}}, Evidence: []*ragv1.Evidence{e}}
	if ValidateProposal(p) != nil {
		t.Fatal("valid scoped citation rejected")
	}
	p.Decision.EvidenceIds = []string{"other"}
	if ValidateProposal(p) == nil {
		t.Fatal("accepted unsupplied citation")
	}
	p.Decision.EvidenceIds = []string{"fixture"}
	p.Mock = true
	if ValidateProposal(p) == nil {
		t.Fatal("published mock answer")
	}
	p.Mock = false
	p.Decision = nil
	if ValidateProposal(p) == nil {
		t.Fatal("published without decision")
	}
}

func TestCustomerOriginProxyAndSubmissionLimits(t *testing.T) {
	s, err := New(nil, nil, nil, "", Config{Origin: "https://shop.example", TrustedProxyIPs: []net.IP{net.ParseIP("127.0.0.1")}})
	if err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"", "https://other.example", "https://shop.example.evil", "https://shop.example/"} {
		r := httptest.NewRequest("POST", "/v1/customer/session", nil)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		s.Routes().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("cross-origin write reached session creation")
		}
	}
	r := httptest.NewRequest("POST", "/", nil)
	r.RemoteAddr = "198.51.100.10:1234"
	r.Header.Set("X-Real-IP", "192.0.2.1")
	if s.remoteIP(r) != "198.51.100.10" {
		t.Fatal("untrusted peer spoofed IP rate bucket")
	}
	r.RemoteAddr = "127.0.0.1:1234"
	if s.remoteIP(r) != "192.0.2.1" {
		t.Fatal("trusted edge IP was not retained")
	}
	for i := 0; i < 5; i++ {
		if !s.allow("visitor-a", s.Config.VisitorRPM) {
			t.Fatal("default visitor budget rejected early")
		}
	}
	if s.allow("visitor-a", s.Config.VisitorRPM) || !s.allow("visitor-b", s.Config.VisitorRPM) {
		t.Fatal("visitor limit did not isolate subjects")
	}
}

func TestServiceLaneRules(t *testing.T) {
	cases := []struct {
		service, order, wantType, wantOrder, wantCode string
	}{
		{"", "", "pre_sales", "", ""},
		{"pre_sales", "", "pre_sales", "", ""},
		{"pre_sales", "ORD-2609-0005", "", "", "invalid_order_id"},
		{"after_sales", "", "", "", "invalid_order_id"},
		{"after_sales", "ord-2609-0005", "after_sales", "ORD-2609-0005", ""},
		{"after_sales", "ORD", "", "", "invalid_order_id"},
		{"after_sales", "ORD-2609-0005;DROP", "", "", "invalid_order_id"},
		{"after_sale", "ORD-2609-0005", "", "", "invalid_service_type"},
	}
	for _, item := range cases {
		service, order, code := normalizeServiceLane(item.service, item.order)
		if service != item.wantType || order != item.wantOrder || code != item.wantCode {
			t.Fatalf("normalizeServiceLane(%q,%q)=(%q,%q,%q), want (%q,%q,%q)",
				item.service, item.order, service, order, code, item.wantType, item.wantOrder, item.wantCode)
		}
	}
}

func TestDatabaseCustomerHumanLifecycle(t *testing.T) {
	path := os.Getenv("V2_INTEGRATION_ENV")
	if path == "" {
		t.Skip("V2_INTEGRATION_ENV required")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatal("protected integration configuration required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("configuration unavailable")
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			values[k] = strings.Trim(v, "\"'")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	connect := func(key string) *pgxpool.Pool {
		p, e := pgxpool.New(ctx, values[key])
		if e != nil {
			t.Fatal("database unavailable")
		}
		t.Cleanup(p.Close)
		var name string
		var super, bypass bool
		if p.QueryRow(ctx, `SELECT current_database(),rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&name, &super, &bypass) != nil || !strings.HasPrefix(name, "ecr_v2_test_") || super || bypass {
			t.Fatal("isolated restricted database required")
		}
		return p
	}
	migration, db := connect("V2_MIGRATION_DSN"), connect("V2_GATEWAY_DSN")
	slug := "customer-test-" + ID()
	var tenant, agentA, agentB int64
	if migration.QueryRow(ctx, `INSERT INTO tenant(name,external_id) VALUES($1,$1) RETURNING id`, slug).Scan(&tenant) != nil {
		t.Fatal("tenant fixture failed")
	}
	if _, err = migration.Exec(ctx, `INSERT INTO shop(tenant_id,id,name,public_slug,customer_chat_enabled) VALUES($1,'shop-test','Synthetic shop',$2,true)`, tenant, slug); err != nil {
		t.Fatal("shop fixture failed")
	}
	for i, p := range []*int64{&agentA, &agentB} {
		if migration.QueryRow(ctx, `INSERT INTO app_user(tenant_id,external_id,role,shop_ids) VALUES($1,$2,'operator',ARRAY['shop-test']) RETURNING id`, tenant, fmt.Sprint("agent-", i)).Scan(p) != nil {
			t.Fatal("staff fixture failed")
		}
	}
	s, err := New(db, nil, bytes.Repeat([]byte{1}, 32), "v2_langchain_bailian", Config{Origin: "http://127.0.0.1", AllowLoopbackHTTP: true, VisitorRPM: 100, IPRPM: 100})
	if err != nil {
		t.Fatal(err)
	}
	api := s.Routes()
	request := func(handler http.Handler, method, path string, input any, cookie *http.Cookie, csrf, idem string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(input)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(ctx)
		r.Header.Set("Origin", s.Config.Origin)
		r.Header.Set("X-CSRF-Token", csrf)
		r.Header.Set("Idempotency-Key", idem)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	decode := func(w *httptest.ResponseRecorder, expected int) map[string]any {
		t.Helper()
		if w.Code != expected {
			t.Fatalf("HTTP %d expected %d: %s", w.Code, expected, w.Body.String())
		}
		var v map[string]any
		if json.Unmarshal(w.Body.Bytes(), &v) != nil {
			t.Fatal("invalid response")
		}
		return v
	}
	bootstrap := request(api, "POST", "/v1/customer/session", map[string]string{"shop_slug": slug}, nil, "", "")
	session := decode(bootstrap, 201)
	cookie := bootstrap.Result().Cookies()[0]
	csrf := session["csrf_token"].(string)
	var digest string
	if db.QueryRow(ctx, `SELECT token_sha256 FROM customer_session WHERE token_sha256=$1`, Hash(cookie.Value)).Scan(&digest) != nil || digest == cookie.Value {
		t.Fatal("cookie credential not hashed")
	}
	if request(api, "POST", "/v1/customer/conversations", nil, cookie, "wrong", "bad").Code != 403 {
		t.Fatal("CSRF bypass")
	}
	conv := decode(request(api, "POST", "/v1/customer/conversations", nil, cookie, csrf, "create"), 201)["conversation_id"].(string)
	pathConv := "/v1/customer/conversations/" + conv
	message := decode(request(api, "POST", pathConv+"/messages", map[string]string{"text": "未提供的说明"}, cookie, csrf, "message-1"), 202)
	replayed := decode(request(api, "POST", pathConv+"/messages", map[string]string{"text": "未提供的说明"}, cookie, csrf, "message-1"), 202)
	if replayed["message_id"] != message["message_id"] || replayed["replayed"] != true {
		t.Fatal("submission replay created duplicate")
	}
	if request(api, "POST", pathConv+"/messages", map[string]string{"text": "不同的问题"}, cookie, csrf, "message-1").Code != 409 {
		t.Fatal("idempotency payload collision accepted")
	}
	var c conversation
	for i := 0; i < 100; i++ {
		c, err = scanConversation(db.QueryRow(ctx, conversationSQL, conv))
		if err == nil && c.State == "WAITING_HUMAN" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if c.State != "WAITING_HUMAN" {
		t.Fatal("missing knowledge did not create durable queue")
	}
	other := request(api, "POST", "/v1/customer/session", map[string]string{"shop_slug": slug}, nil, "", "")
	decode(other, 201)
	if request(api, "GET", pathConv, nil, other.Result().Cookies()[0], "", "").Code != 404 {
		t.Fatal("cross visitor conversation leak")
	}
	if _, err = migration.Exec(ctx, `INSERT INTO shop(tenant_id,id,name,public_slug,customer_chat_enabled) VALUES($1,'shop-other','Other synthetic shop',$2,true)`, tenant, slug+"-other"); err != nil {
		t.Fatal("second shop fixture failed")
	}
	shopSession := request(api, "POST", "/v1/customer/session", map[string]string{"shop_slug": slug + "-other"}, nil, "", "")
	decode(shopSession, 201)
	for _, suffix := range []string{"", "/events", "/citations/unknown"} {
		if request(api, "GET", pathConv+suffix, nil, shopSession.Result().Cookies()[0], "", "").Code != 404 {
			t.Fatal("cross-shop customer access reached messages or replay")
		}
	}
	staffA := Staff{Tenant: tenant, User: agentA, Shops: []string{"shop-test"}, Role: "operator", SessionHash: "session-a"}
	staffB := staffA
	staffB.User = agentB
	staffB.SessionHash = "session-b"
	unauthorized := staffA
	unauthorized.Shops = []string{"shop-other"}
	if request(s.StaffRoutes(unauthorized), "POST", "/v1/support/conversations/"+conv+"/claim", map[string]any{"epoch": c.Epoch}, nil, "", "cross-shop").Code != 404 {
		t.Fatal("staff crossed shop boundary to claim")
	}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	results := make(chan struct {
		staff  Staff
		result map[string]any
	}, 2)
	for _, staff := range []Staff{staffA, staffB} {
		wg.Add(1)
		go func(staff Staff) {
			defer wg.Done()
			w := request(s.StaffRoutes(staff), "POST", "/v1/support/conversations/"+conv+"/claim", map[string]any{"epoch": c.Epoch}, nil, "", "claim")
			codes <- w.Code
			if w.Code == 200 {
				var v map[string]any
				_ = json.Unmarshal(w.Body.Bytes(), &v)
				results <- struct {
					staff  Staff
					result map[string]any
				}{staff, v}
			}
		}(staff)
	}
	wg.Wait()
	close(codes)
	wins := 0
	for code := range codes {
		if code == 200 {
			wins++
		} else if code != 409 {
			t.Fatalf("unexpected claim code %d", code)
		}
	}
	if wins != 1 {
		t.Fatal("claim did not have exactly one owner")
	}
	winner := <-results
	epoch := int64(winner.result["epoch"].(float64))
	staffAPI := s.StaffRoutes(winner.staff)
	staffPath := "/v1/support/conversations/" + conv
	reply := decode(request(staffAPI, "POST", staffPath+"/messages", map[string]any{"epoch": epoch, "text": "人工说明：需要确认资料。"}, nil, "", "reply"), 201)
	if reply["message_id"] == "" {
		t.Fatal("human reply not persisted")
	}
	history := decode(request(api, "GET", pathConv, nil, cookie, "", ""), 200)
	messages := history["messages"].([]any)
	if messages[len(messages)-1].(map[string]any)["source"] != "agent" {
		t.Fatal("customer did not receive human message")
	}
	// A late generation completion may not change a human-owned conversation.
	if s.finish(ctx, message["execution_id"].(string), conv, nil, proposal{}, "") != nil {
		t.Fatal("late completion failed")
	}
	decode(request(staffAPI, "POST", staffPath+"/heartbeat", map[string]any{"epoch": epoch}, nil, "", "heartbeat"), 200)
	if err = s.ReleaseSession(ctx, winner.staff.SessionHash); err != nil {
		t.Fatal("logout requeue failed")
	}
	if request(staffAPI, "POST", staffPath+"/messages", map[string]any{"epoch": epoch, "text": "过期回复"}, nil, "", "stale").Code != 409 {
		t.Fatal("logged out claim could reply")
	}
	c, err = scanConversation(db.QueryRow(ctx, conversationSQL, conv))
	if err != nil || c.State != "WAITING_HUMAN" {
		t.Fatal("logout did not release queue")
	}
	claimed := decode(request(staffAPI, "POST", staffPath+"/claim", map[string]any{"epoch": c.Epoch}, nil, "", "claim-again"), 200)
	epoch = int64(claimed["epoch"].(float64))
	if _, err = migration.Exec(ctx, `UPDATE app_user SET revoked_at=now() WHERE id=$1`, winner.staff.User); err != nil {
		t.Fatal("revocation fixture failed")
	}
	if request(staffAPI, "POST", staffPath+"/messages", map[string]any{"epoch": epoch, "text": "撤权回复"}, nil, "", "revoked").Code != 409 {
		t.Fatal("revoked staff replied")
	}
	if err = s.Recover(ctx); err != nil {
		t.Fatal("restart recovery failed")
	}
	restarted, err := New(db, nil, s.SigningKey, s.Profile, s.Config)
	if err != nil {
		t.Fatal(err)
	}
	decode(request(restarted.Routes(), "GET", pathConv, nil, cookie, "", ""), 200)
	var count int
	if db.QueryRow(ctx, `SELECT count(*) FROM customer_message WHERE conversation_id=$1 AND source='agent'`, conv).Scan(&count) != nil || count != 1 {
		t.Fatal("late or duplicate human delivery")
	}
}

func TestDatabaseAfterSalesTicketLifecycle(t *testing.T) {
	path := os.Getenv("V2_INTEGRATION_ENV")
	if path == "" {
		t.Skip("V2_INTEGRATION_ENV required")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatal("protected integration configuration required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("configuration unavailable")
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			values[k] = strings.Trim(v, "\"'")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	connect := func(key string) *pgxpool.Pool {
		p, e := pgxpool.New(ctx, values[key])
		if e != nil {
			t.Fatal("database unavailable")
		}
		t.Cleanup(p.Close)
		var name string
		var super, bypass bool
		if p.QueryRow(ctx, `SELECT current_database(),rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&name, &super, &bypass) != nil || !strings.HasPrefix(name, "ecr_v2_test_") || super || bypass {
			t.Fatal("isolated restricted database required")
		}
		return p
	}
	migration, db := connect("V2_MIGRATION_DSN"), connect("V2_GATEWAY_DSN")
	slug := "ticket-test-" + ID()
	var tenant, agent int64
	if migration.QueryRow(ctx, `INSERT INTO tenant(name,external_id) VALUES($1,$1) RETURNING id`, slug).Scan(&tenant) != nil {
		t.Fatal("tenant fixture failed")
	}
	if _, err = migration.Exec(ctx, `INSERT INTO shop(tenant_id,id,name,public_slug,customer_chat_enabled) VALUES($1,'shop-test','Synthetic shop',$2,true)`, tenant, slug); err != nil {
		t.Fatal("shop fixture failed")
	}
	if migration.QueryRow(ctx, `INSERT INTO app_user(tenant_id,external_id,role,shop_ids) VALUES($1,'ticket-agent','operator',ARRAY['shop-test']) RETURNING id`, tenant).Scan(&agent) != nil {
		t.Fatal("staff fixture failed")
	}
	s, err := New(db, nil, bytes.Repeat([]byte{2}, 32), "v2_lingchain_mock", Config{Origin: "http://127.0.0.1", AllowLoopbackHTTP: true, VisitorRPM: 100, IPRPM: 100})
	if err != nil {
		t.Fatal(err)
	}
	api := s.Routes()
	request := func(handler http.Handler, method, path string, input any, cookie *http.Cookie, csrf, idem string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(input)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(ctx)
		r.Header.Set("Origin", s.Config.Origin)
		r.Header.Set("X-CSRF-Token", csrf)
		r.Header.Set("Idempotency-Key", idem)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	decode := func(w *httptest.ResponseRecorder, expected int) map[string]any {
		t.Helper()
		if w.Code != expected {
			t.Fatalf("HTTP %d expected %d: %s", w.Code, expected, w.Body.String())
		}
		var v map[string]any
		if json.Unmarshal(w.Body.Bytes(), &v) != nil {
			t.Fatal("invalid response")
		}
		return v
	}
	bootstrap := request(api, "POST", "/v1/customer/session", map[string]string{"shop_slug": slug}, nil, "", "")
	session := decode(bootstrap, 201)
	cookie := bootstrap.Result().Cookies()[0]
	csrf := session["csrf_token"].(string)
	ticketCount := func(conversation string) int {
		var count int
		if db.QueryRow(ctx, `SELECT count(*) FROM customer_ticket WHERE conversation_id=$1`, conversation).Scan(&count) != nil {
			t.Fatal("ticket query failed")
		}
		return count
	}
	if request(api, "POST", "/v1/customer/conversations", map[string]string{"service_type": "after_sales"}, cookie, csrf, "no-order").Code != 400 {
		t.Fatal("after-sales without an order was accepted")
	}
	if request(api, "POST", "/v1/customer/conversations", map[string]string{"service_type": "pre_sales", "order_id": "ORD-2609-0005"}, cookie, csrf, "pre-order").Code != 400 {
		t.Fatal("pre-sales with an order was accepted")
	}
	created := decode(request(api, "POST", "/v1/customer/conversations",
		map[string]string{"service_type": "after_sales", "order_id": "ord-2609-0005"}, cookie, csrf, "lane"), 201)
	conversation := created["conversation_id"].(string)
	if created["service_type"] != "after_sales" || created["order_id"] != "ORD-2609-0005" {
		t.Fatal("lane was not bound to the conversation")
	}
	if request(api, "POST", "/v1/customer/conversations",
		map[string]string{"service_type": "after_sales", "order_id": "ORD-2609-0006"}, cookie, csrf, "lane").Code != 409 {
		t.Fatal("idempotency key reuse across orders was accepted")
	}
	replay := decode(request(api, "POST", "/v1/customer/conversations",
		map[string]string{"service_type": "after_sales", "order_id": "ORD-2609-0005"}, cookie, csrf, "lane"), 201)
	if replay["conversation_id"] != conversation {
		t.Fatal("same key and lane created a second conversation")
	}
	pathConv := "/v1/customer/conversations/" + conversation
	handoff := decode(request(api, "POST", pathConv+"/handoff", map[string]any{}, cookie, csrf, "handoff"), 200)
	epoch := int64(handoff["epoch"].(float64))
	// The customer handoff and the staff actions share the same durable ticket.
	var status, order string
	if db.QueryRow(ctx, `SELECT status,customer_id FROM customer_ticket WHERE conversation_id=$1`, conversation).Scan(&status, &order) != nil || status != "OPEN" {
		t.Fatalf("handoff did not open a ticket: %s", status)
	}
	if ticketCount(conversation) != 1 {
		t.Fatal("handoff created more than one ticket")
	}
	staff := Staff{Tenant: tenant, User: agent, Shops: []string{"shop-test"}, Role: "operator", SessionHash: "ticket-session"}
	staffAPI := s.StaffRoutes(staff)
	staffPath := "/v1/support/conversations/" + conversation
	claimed := decode(request(staffAPI, "POST", staffPath+"/claim", map[string]any{"epoch": epoch}, nil, "", "claim"), 200)
	epoch = int64(claimed["epoch"].(float64))
	var assignee *int64
	if db.QueryRow(ctx, `SELECT status,assignee_user_id FROM customer_ticket WHERE conversation_id=$1`, conversation).Scan(&status, &assignee) != nil || status != "ASSIGNED" || assignee == nil || *assignee != agent {
		t.Fatal("claim did not assign the ticket")
	}
	staffDetail := decode(request(staffAPI, "GET", staffPath, nil, nil, "", ""), 200)
	staffTicket, ok := staffDetail["ticket"].(map[string]any)
	if !ok || staffTicket["status"] != "ASSIGNED" {
		t.Fatalf("staff detail omitted the active ticket: %v", staffDetail["ticket"])
	}
	queue := decode(request(staffAPI, "GET", "/v1/support/queue", nil, nil, "", ""), 200)
	items := queue["conversations"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["ticket_status"] != "ASSIGNED" ||
		items[0].(map[string]any)["order_id"] != "ORD-2609-0005" || items[0].(map[string]any)["service_type"] != "after_sales" {
		t.Fatalf("staff queue did not expose the active ticket: %v", items)
	}
	released := decode(request(staffAPI, "POST", staffPath+"/release", map[string]any{"epoch": epoch}, nil, "", "release"), 200)
	epoch = int64(released["epoch"].(float64))
	if db.QueryRow(ctx, `SELECT status,assignee_user_id FROM customer_ticket WHERE conversation_id=$1`, conversation).Scan(&status, &assignee) != nil || status != "OPEN" || assignee != nil {
		t.Fatal("release did not reopen the ticket")
	}
	claimed = decode(request(staffAPI, "POST", staffPath+"/claim", map[string]any{"epoch": epoch}, nil, "", "claim-2"), 200)
	epoch = int64(claimed["epoch"].(float64))
	if request(staffAPI, "POST", staffPath+"/resolve", map[string]any{"epoch": epoch}, nil, "", "resolve").Code != 200 {
		t.Fatal("resolve failed")
	}
	var ticketStatus, conversationState string
	var resolved *time.Time
	if db.QueryRow(ctx, `SELECT status,resolved_at FROM customer_ticket WHERE conversation_id=$1`, conversation).Scan(&ticketStatus, &resolved) != nil || ticketStatus != "RESOLVED" || resolved == nil {
		t.Fatal("resolve did not close the ticket as RESOLVED")
	}
	if db.QueryRow(ctx, `SELECT state FROM customer_conversation WHERE id=$1`, conversation).Scan(&conversationState) != nil || conversationState != "CLOSED" {
		t.Fatal("resolve did not close the conversation")
	}
	resumeConversation := decode(request(api, "POST", "/v1/customer/conversations",
		map[string]string{"service_type": "after_sales", "order_id": "ORD-2609-0007"}, cookie, csrf, "resume"), 201)["conversation_id"].(string)
	resumePath := "/v1/customer/conversations/" + resumeConversation
	resumeEpoch := int64(decode(request(api, "POST", resumePath+"/handoff", map[string]any{}, cookie, csrf, "resume-handoff"), 200)["epoch"].(float64))
	resumeStaffPath := "/v1/support/conversations/" + resumeConversation
	resumeClaim := decode(request(staffAPI, "POST", resumeStaffPath+"/claim", map[string]any{"epoch": resumeEpoch}, nil, "", "resume-claim"), 200)
	resumeEpoch = int64(resumeClaim["epoch"].(float64))
	decode(request(staffAPI, "POST", resumeStaffPath+"/resume-ai", map[string]any{"epoch": resumeEpoch}, nil, "", "resume-ai"), 200)
	var resumeStatus string
	var resumeClosed *time.Time
	if db.QueryRow(ctx, `SELECT status,closed_at FROM customer_ticket WHERE conversation_id=$1`, resumeConversation).Scan(&resumeStatus, &resumeClosed) != nil || resumeStatus != "CLOSED" || resumeClosed == nil {
		t.Fatalf("resume-ai left the ticket active: %s %v", resumeStatus, resumeClosed)
	}
	if db.QueryRow(ctx, `SELECT state FROM customer_conversation WHERE id=$1`, resumeConversation).Scan(&conversationState) != nil || conversationState != "AI" {
		t.Fatalf("resume-ai did not return the conversation to AI: %s", conversationState)
	}
	preSales := decode(request(api, "POST", "/v1/customer/conversations", map[string]string{}, cookie, csrf, "pre"), 201)["conversation_id"].(string)
	preHandoff := decode(request(api, "POST", "/v1/customer/conversations/"+preSales+"/handoff", map[string]any{}, cookie, csrf, "pre-handoff"), 200)
	if ticketCount(preSales) != 0 {
		t.Fatal("pre-sales handoff created a ticket")
	}
	preEpoch := int64(preHandoff["epoch"].(float64))
	preClaim := decode(request(staffAPI, "POST", "/v1/support/conversations/"+preSales+"/claim", map[string]any{"epoch": preEpoch}, nil, "", "pre-claim"), 200)
	preEpoch = int64(preClaim["epoch"].(float64))
	resolvePreSales := request(staffAPI, "POST", "/v1/support/conversations/"+preSales+"/resolve", map[string]any{"epoch": preEpoch}, nil, "", "pre-resolve")
	if resolvePreSales.Code != 409 {
		t.Fatalf("pre-sales resolve = %d: %s", resolvePreSales.Code, resolvePreSales.Body.String())
	}
	var resolveError map[string]any
	if json.Unmarshal(resolvePreSales.Body.Bytes(), &resolveError) != nil || resolveError["code"] != "resolve_requires_after_sales" {
		t.Fatalf("pre-sales resolve error: %s", resolvePreSales.Body.String())
	}
	if db.QueryRow(ctx, `SELECT state FROM customer_conversation WHERE id=$1`, preSales).Scan(&conversationState) != nil || conversationState != "HUMAN" {
		t.Fatalf("pre-sales resolve changed state: %s", conversationState)
	}
	if ticketCount(preSales) != 0 {
		t.Fatal("pre-sales resolve created a ticket")
	}
}

func TestAfterSalesFactPlanBindsOrderAndSKU(t *testing.T) {
	entities := func(values ...*ragv1.EntitySlot) []*ragv1.EntitySlot { return values }
	understood := func(tools []string, slots []*ragv1.EntitySlot) *ragv1.UnderstandResponse {
		return &ragv1.UnderstandResponse{RequiredFactTools: tools, Entities: slots}
	}
	orderEntity := &ragv1.EntitySlot{Name: "order_id", Value: "ORD-2609-0005", Confidence: 1}
	otherOrder := &ragv1.EntitySlot{Name: "order_id", Value: "ORD-2609-0006", Confidence: 1}
	skuEntity := &ragv1.EntitySlot{Name: "sku", Value: "CLB-DRK-002", Confidence: 1}
	cases := []struct {
		name    string
		request *ragv1.UnderstandResponse
		query   string
		reason  string
	}{
		{"order tool uses the bound order", understood([]string{"get_order_status"}, entities(orderEntity)), "订单到哪了", ""},
		{"order tool needs no repeated identifier", understood([]string{"get_logistics"}, nil), "物流到哪了", ""},
		{"different order asks for confirmation", understood([]string{"get_order_status"}, entities(otherOrder)), "订单到哪了", "order_clarification"},
		{"explicit bound order without an entity is accepted", understood([]string{"get_order_status"}, nil), "订单号 ORD-2609-0005 到哪了", ""},
		{"explicit different order without an entity is clarified", understood([]string{"get_order_status"}, nil), "订单号 ORD-2609-0006 到哪了", "order_clarification"},
		{"explicit different order overrides the bound entity", understood([]string{"get_order_status"}, entities(orderEntity)), "订单编号 ORD-2609-0006 到哪了", "order_clarification"},
		{"english order cue is compared case-insensitively", understood([]string{"get_logistics"}, nil), "order id ord-2609-0005 status", ""},
		{"a bare sku is not an order reference", understood([]string{"get_stock"}, entities(skuEntity)), "CLB-DRK-002 还有库存吗", ""},
		{"compare_products is outside the customer channel", understood([]string{"compare_products"}, nil), "对比一下", "unsupported_request"},
		{"realtime without a supported tool goes to staff", understood(nil, nil), "帮我退款", "unsupported_request"},
		{"stock tool needs an explicit sku", understood([]string{"get_stock"}, entities(skuEntity)), "CLB-DRK-002 还有库存吗", ""},
		{"sku not present in the current query", understood([]string{"get_stock"}, entities(skuEntity)), "这个还有库存吗", "sku_clarification"},
		{"two sku candidates are ambiguous", understood([]string{"get_stock"}, entities(skuEntity)), "CLB-DRK-002 和 CLB-DRK-003 哪个有库存", "sku_clarification"},
		{"the bound order is not mistaken for a sku", understood([]string{"get_stock"}, entities(skuEntity)), "订单 ORD-2609-0005 里的 CLB-DRK-002 有库存吗", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, reason := afterSalesFactPlan(tc.request, "ORD-2609-0005", tc.query)
			if reason != tc.reason {
				t.Fatalf("reason=%q want %q", reason, tc.reason)
			}
		})
	}
}

func TestValidateProposalAcceptsFactBackedAnswers(t *testing.T) {
	fact := &ragv1.Fact{ToolName: "get_logistics", ResourceId: "ORD-2609-0005",
		ValueJson: `{"order_id":"ORD-2609-0005","status":"in_transit"}`, ObservedAt: "2026-09-29T12:00:00Z"}
	proposalFor := func(facts []*ragv1.Fact) proposal {
		return proposal{Answer: "物流显示运输中。", Decision: &ragv1.ReplyDecision{
			Behavior: ragv1.ReplyBehavior_REPLY_BEHAVIOR_ANSWER, ReasonCode: "grounded"}, Facts: facts}
	}
	if ValidateProposal(proposalFor([]*ragv1.Fact{fact})) != nil {
		t.Fatal("fact-backed answer was rejected")
	}
	if ValidateProposal(proposalFor(nil)) == nil {
		t.Fatal("answer without evidence or facts was accepted")
	}
	unknown := &ragv1.Fact{ToolName: "compare_products", ResourceId: fact.ResourceId,
		ValueJson: fact.ValueJson, ObservedAt: fact.ObservedAt}
	if ValidateProposal(proposalFor([]*ragv1.Fact{unknown})) == nil {
		t.Fatal("unsupported fact tool was accepted")
	}
	mismatch := &ragv1.Fact{ToolName: fact.ToolName, ResourceId: "ORD-2609-0006",
		ValueJson: `{"order_id":"ORD-2609-0007","status":"in_transit"}`, ObservedAt: fact.ObservedAt}
	if ValidateProposal(proposalFor([]*ragv1.Fact{mismatch})) == nil {
		t.Fatal("fact value and resource mismatch was accepted")
	}
	many := []*ragv1.Fact{fact, fact, fact, fact, fact}
	if ValidateProposal(proposalFor(many)) == nil {
		t.Fatal("more than four facts were accepted")
	}
}

type stubFacts struct {
	fact *ragv1.Fact
	err  error
}

func (s stubFacts) Lookup(_ context.Context, tool string, _ *ragv1.RequestContext, resource string) (*ragv1.Fact, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &ragv1.Fact{ToolName: tool, ResourceId: resource, ValueJson: s.fact.ValueJson,
		ObservedAt: s.fact.ObservedAt, IsMock: s.fact.IsMock}, nil
}

func afterSalesScope() *ragv1.RequestContext {
	return &ragv1.RequestContext{TenantId: "v2-demo-tenant", UserId: "customer:CUST-0001",
		ShopId: "shop-demo", AllowedShopIds: []string{"shop-demo"},
		ServiceType: "after_sales", CustomerOrderId: "ORD-2609-0005"}
}

func TestCustomerFactsLookupUsesOnlyTheBoundOrder(t *testing.T) {
	fact := &ragv1.Fact{ValueJson: `{"order_id":"ORD-2609-0005","status":"shipped"}`,
		ObservedAt: "2026-09-29T12:00:00Z"}
	s := &Service{Facts: stubFacts{fact: fact}}
	state := &customerRunState{scope: afterSalesScope(), orderTools: []string{"get_order_status"}}
	if !s.lookupCustomerFacts(context.Background(), state) {
		t.Fatalf("lookup failed: %s", state.reason)
	}
	if len(state.facts) != 1 || state.facts[0].ResourceId != "ORD-2609-0005" || state.facts[0].ToolName != "get_order_status" {
		t.Fatalf("unexpected facts %+v", state.facts)
	}
	failed := &customerRunState{scope: afterSalesScope(), orderTools: []string{"get_logistics"}}
	s.Facts = stubFacts{err: facts.ErrUnauthorized}
	if s.lookupCustomerFacts(context.Background(), failed) {
		t.Fatal("provider failure was accepted")
	}
	if failed.reason != "fact_provider_unauthorized" {
		t.Fatalf("reason=%q", failed.reason)
	}
}

func TestFactRevalidationRejectsForeignResources(t *testing.T) {
	valid := &ragv1.Fact{ToolName: "get_logistics", ResourceId: "ORD-2609-0005",
		ValueJson: `{"order_id":"ORD-2609-0005","status":"in_transit"}`, ObservedAt: "2026-09-29T12:00:00Z"}
	s := &Service{Facts: stubFacts{fact: valid}}
	if reason := s.revalidateFacts(context.Background(), afterSalesScope(), []*ragv1.Fact{valid}); reason != "" {
		t.Fatalf("valid fact rejected: %s", reason)
	}
	foreign := &ragv1.Fact{ToolName: valid.ToolName, ResourceId: "ORD-2609-0006",
		ValueJson: `{"order_id":"ORD-2609-0006","status":"in_transit"}`, ObservedAt: valid.ObservedAt}
	if reason := s.revalidateFacts(context.Background(), afterSalesScope(), []*ragv1.Fact{foreign}); reason != "permission_changed" {
		t.Fatalf("foreign order reason=%q", reason)
	}
}

func TestCustomerFactsLookupAgainstTheCommerceSimulator(t *testing.T) {
	dataset, err := commercesim.Load(filepath.Join("..", "..", "..", "data", "synthetic", "commerce-lab-v1"))
	if err != nil {
		t.Fatalf("load commerce dataset: %v", err)
	}
	server := httptest.NewServer(commercesim.New(dataset).Handler())
	t.Cleanup(server.Close)
	scope := &ragv1.RequestContext{TenantId: dataset.Manifest.TenantID, UserId: "customer:CUST-0001",
		ShopId: dataset.Manifest.ShopID, AllowedShopIds: []string{dataset.Manifest.ShopID},
		ServiceType: "after_sales", CustomerOrderId: "ORD-2609-0005"}
	s := &Service{Facts: facts.HTTPProvider{BaseURL: server.URL, Version: dataset.Manifest.FactsVersion}}
	state := &customerRunState{scope: scope, orderTools: []string{"get_order_status"}}
	if !s.lookupCustomerFacts(context.Background(), state) {
		t.Fatalf("simulator lookup failed: %s", state.reason)
	}
	if len(state.facts) != 1 || state.facts[0].ResourceId != "ORD-2609-0005" || !state.facts[0].GetIsMock() {
		t.Fatalf("unexpected simulator fact %+v", state.facts)
	}
	state = &customerRunState{scope: scope, productTool: []string{"get_stock"}, sku: "CLB-DRK-002"}
	if !s.lookupCustomerFacts(context.Background(), state) {
		t.Fatalf("simulator stock lookup failed: %s", state.reason)
	}
	if len(state.facts) != 1 || state.facts[0].ResourceId != "CLB-DRK-002" {
		t.Fatalf("unexpected simulator stock fact %+v", state.facts)
	}
}
