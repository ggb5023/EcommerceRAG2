package customer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

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
	staffA := Staff{Tenant: tenant, User: agentA, Shops: []string{"shop-test"}, Role: "operator", SessionHash: "session-a"}
	staffB := staffA
	staffB.User = agentB
	staffB.SessionHash = "session-b"
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
