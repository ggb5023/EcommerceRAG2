package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestV2HTTPIsolatedDatabaseAcceptance(t *testing.T) {
	if os.Getenv("ECR_V2_HTTP_INTEGRATION") != "1" {
		t.Skip("set ECR_V2_HTTP_INTEGRATION=1 with seeded isolated v2 database")
	}
	appDSN := strings.TrimSpace(os.Getenv("M1_APP_DATABASE_URL"))
	adminDSN := strings.TrimSpace(os.Getenv("M1_MIGRATION_DATABASE_URL"))
	if appDSN == "" || adminDSN == "" {
		t.Fatal("M1_APP_DATABASE_URL and M1_MIGRATION_DATABASE_URL are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	app, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("open v2 app pool: %v", err)
	}
	defer app.Close()
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("open v2 migration pool: %v", err)
	}
	defer admin.Close()
	var databaseName string
	if err = app.QueryRow(ctx, "SELECT current_database()").Scan(&databaseName); err != nil {
		t.Fatalf("check v2 database: %v", err)
	}
	if !strings.Contains(databaseName, "m1_test") {
		t.Fatalf("integration database is not isolated: %s", databaseName)
	}
	var checkpointTable bool
	if err = app.QueryRow(ctx, "SELECT to_regclass('public.v2_asking_checkpoint') IS NOT NULL").Scan(&checkpointTable); err != nil || !checkpointTable {
		t.Fatal("v2 checkpoint migration 0015 is not applied")
	}

	session, err := (v2FixtureIdentityAdapter{db: app}).Resolve(ctx)
	if err != nil {
		t.Fatalf("resolve v2 fixture identity: %v", err)
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		t.Fatal(err)
	}
	gateway := &gateway{
		db: app, session: session, signingKey: key, profile: "v2_langgraph_fixture",
		facts: unavailableFactProvider{}, cancels: map[string]context.CancelFunc{},
	}
	gateway.ready.Store(true)
	authority := httptest.NewServer(gateway.routes())
	defer authority.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	repo, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	python := filepath.Join(repo, "python/.venv/bin/python")
	process := exec.Command(python, "-m", "app.v2_grpc_server")
	process.Dir = repo
	process.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"PYTHONPATH=" + filepath.Join(repo, "python"),
		"APP_ENV=test",
		"RAG_PROFILE=v2_langgraph_fixture",
		"V2_GRPC_ADDR=" + address,
		"V2_GRPC_REFLECTION=0",
		"V2_CHECKPOINT_DSN=" + appDSN,
		"AUTHORITY_HTTP_BASE=" + authority.URL,
	}
	process.Stdout = os.Stdout
	process.Stderr = os.Stderr
	if err = process.Start(); err != nil {
		t.Fatalf("start v2 Python process: %v", err)
	}
	defer func() {
		_ = process.Process.Signal(os.Interrupt)
		_ = process.Wait()
	}()

	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	gateway.rag = ragv1.NewRagServiceClient(connection)
	probeContext, probeCancel := context.WithTimeout(ctx, 10*time.Second)
	defer probeCancel()
	probeScope, err := gateway.issueScope(probeContext, "v2-http-readiness")
	if err != nil {
		t.Fatalf("issue v2 readiness scope: %v", err)
	}
	for {
		_, probeErr := gateway.rag.Understand(probeContext, &ragv1.UnderstandRequest{Context: probeScope, Query: "保温杯容量是多少？"})
		if probeErr == nil {
			break
		}
		if probeContext.Err() != nil {
			t.Fatalf("v2 Python process did not become ready: %v", probeErr)
		}
		time.Sleep(50 * time.Millisecond)
	}
	httpServer := httptest.NewServer(gateway.routes())
	defer httpServer.Close()

	client := httpServer.Client()
	doJSON := func(method, path string, body any, headers map[string]string) (int, map[string]any) {
		var reader io.Reader
		if body != nil {
			payload, marshalErr := json.Marshal(body)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			reader = bytes.NewReader(payload)
		}
		request, requestErr := http.NewRequestWithContext(ctx, method, httpServer.URL+path, reader)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		response, requestErr := client.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		defer response.Body.Close()
		var decoded map[string]any
		if decodeErr := json.NewDecoder(response.Body).Decode(&decoded); decodeErr != nil {
			t.Fatalf("decode %s %s: %v", method, path, decodeErr)
		}
		return response.StatusCode, decoded
	}
	poll := func(requestID string) map[string]any {
		for attempt := 0; attempt < 100; attempt++ {
			status, result := doJSON(http.MethodGet, "/v1/turns/"+requestID, nil, nil)
			if status != http.StatusOK {
				t.Fatalf("get execution status: %d %#v", status, result)
			}
			switch result["status"] {
			case "DONE", "FAILED", "CANCELLED", "ASKING":
				return result
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("execution %s did not reach a terminal state", requestID)
		return nil
	}
	newConversation := func() string {
		status, result := doJSON(http.MethodPost, "/v1/conversations", map[string]string{"shop_id": "shop-demo"}, nil)
		if status != http.StatusCreated {
			t.Fatalf("create conversation: %d %#v", status, result)
		}
		id, ok := result["id"].(string)
		if !ok || id == "" {
			t.Fatalf("conversation id missing: %#v", result)
		}
		return id
	}

	status, sessionResult := doJSON(http.MethodGet, "/v1/session", nil, nil)
	if status != http.StatusOK || sessionResult["tenantId"] != "v2-demo-tenant" {
		t.Fatalf("v2 session failed: %d %#v", status, sessionResult)
	}

	conversationID := newConversation()
	status, turn := doJSON(http.MethodPost, "/v1/conversations/"+conversationID+"/turns", map[string]string{"query": "保温杯容量是多少？"}, map[string]string{"Idempotency-Key": "v2-http-knowledge-integration"})
	if status != http.StatusAccepted {
		t.Fatalf("create knowledge turn: %d %#v", status, turn)
	}
	requestID := turn["request_id"].(string)
	knowledge := poll(requestID)
	knowledgeEvents := readEvents(t, client, ctx, httpServer.URL, requestID)
	if knowledge["status"] != "DONE" || knowledge["is_mock"] != true {
		t.Fatalf("knowledge turn failed: %#v; events=%s", knowledge, knowledgeEvents)
	}
	evidence, ok := knowledge["evidence"].([]any)
	if !ok || len(evidence) != 1 || evidence[0].(map[string]any)["documentId"] != "v2-doc-bottle" {
		t.Fatalf("unexpected v2 evidence: %#v", knowledge["evidence"])
	}
	customerReply := knowledge["customer_reply"].(map[string]any)
	if customerReply["can_copy"] != false {
		t.Fatal("fixture customer reply became copyable")
	}
	if !strings.Contains(knowledgeEvents, "event: completed") {
		t.Fatalf("knowledge SSE did not complete: %s", knowledgeEvents)
	}

	status, factTurn := doJSON(http.MethodPost, "/v1/conversations/"+conversationID+"/turns", map[string]string{"query": "保温杯价格是多少？"}, map[string]string{"Idempotency-Key": "v2-http-fact-integration"})
	if status != http.StatusAccepted {
		t.Fatalf("create fact turn: %d %#v", status, factTurn)
	}
	factRequestID := factTurn["request_id"].(string)
	factResult := poll(factRequestID)
	if factResult["status"] != "FAILED" {
		t.Fatalf("unavailable fact unexpectedly succeeded: %#v", factResult)
	}
	factEvents := readEvents(t, client, ctx, httpServer.URL, factRequestID)
	if !strings.Contains(factEvents, "fact_provider_unavailable") {
		t.Fatalf("fact failure was not exposed in SSE: %s", factEvents)
	}

	askingConversation := newConversation()
	status, askingTurn := doJSON(http.MethodPost, "/v1/conversations/"+askingConversation+"/turns", map[string]string{"query": "这个呢"}, map[string]string{"Idempotency-Key": "v2-http-asking-integration"})
	if status != http.StatusAccepted {
		t.Fatalf("create asking turn: %d %#v", status, askingTurn)
	}
	askingRequestID := askingTurn["request_id"].(string)
	askingTurnID := askingTurn["turn_id"].(string)
	askingResult := poll(askingRequestID)
	if askingResult["status"] != "ASKING" {
		t.Fatalf("v2 turn did not enter ASKING: %#v", askingResult)
	}
	status, resume := doJSON(http.MethodPost, "/v1/turns/"+askingTurnID+"/resume", map[string]string{"text": "保温杯容量是多少？"}, map[string]string{"Idempotency-Key": "v2-http-resume-integration"})
	if status != http.StatusAccepted {
		t.Fatalf("resume asking turn: %d %#v", status, resume)
	}
	resumeRequestID := resume["request_id"].(string)
	if result := poll(resumeRequestID); result["status"] != "DONE" {
		t.Fatalf("resumed turn failed: %#v", result)
	}
	status, replay := doJSON(http.MethodPost, "/v1/turns/"+askingTurnID+"/resume", map[string]string{"text": "保温杯容量是多少？"}, map[string]string{"Idempotency-Key": "v2-http-resume-integration"})
	if status != http.StatusOK || replay["request_id"] != resumeRequestID || replay["execution_no"] != float64(2) {
		t.Fatalf("resume replay was not exact: %d %#v", status, replay)
	}
	status, conflict := doJSON(http.MethodPost, "/v1/turns/"+askingTurnID+"/resume", map[string]string{"text": "另一个问题"}, map[string]string{"Idempotency-Key": "v2-http-resume-integration"})
	if status != http.StatusConflict || conflict["code"] != "idempotency_conflict" {
		t.Fatalf("resume conflict was not rejected: %d %#v", status, conflict)
	}

	if _, err = admin.Exec(ctx, `UPDATE app_user u SET revoked_at=now() FROM tenant t WHERE u.tenant_id=t.id AND t.name='V2 LangGraph fixture tenant' AND u.external_id='dev-user'`); err != nil {
		t.Fatalf("revoke v2 fixture identity: %v", err)
	}
	defer func() {
		_, _ = admin.Exec(context.Background(), `UPDATE app_user u SET revoked_at=NULL FROM tenant t WHERE u.tenant_id=t.id AND t.name='V2 LangGraph fixture tenant' AND u.external_id='dev-user'`)
	}()
	status, revoked := doJSON(http.MethodGet, "/v1/session", nil, nil)
	if status != http.StatusForbidden || revoked["code"] != "identity_revoked" {
		t.Fatalf("revoked v2 identity was accepted: %d %#v", status, revoked)
	}
}

func readEvents(t *testing.T, client *http.Client, ctx context.Context, base, requestID string) string {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/turns/"+requestID+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("SSE returned %d", response.StatusCode)
	}
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
