package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// TestGoCallsDedicatedPythonV2GRPC is opt-in because it starts a temporary
// Python process.  It exercises the actual generated Go client against the
// dedicated v2 entrypoint; the regular unit suite stays self-contained.
// ECR_V2_PYTHON_ENTRYPOINT selects app.rpc.server (default) or the merged
// app.ai_process, so both entrypoints keep a real cross-process boundary.
func TestGoCallsDedicatedPythonV2GRPC(t *testing.T) {
	if os.Getenv("ECR_V2_GO_PYTHON") != "1" {
		t.Skip("set ECR_V2_GO_PYTHON=1 to run the temporary Go-to-Python boundary test")
	}
	repo, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	python := filepath.Join(repo, "python/.venv/bin/python")
	if _, err = os.Stat(python); err != nil {
		t.Skipf("Python venv is unavailable: %v", err)
	}
	authority := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/internal/authorization/validate" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"allowed": true})
	}))
	defer authority.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	entrypoint := os.Getenv("ECR_V2_PYTHON_ENTRYPOINT")
	if entrypoint == "" {
		entrypoint = "app.rpc.server"
	}
	process := exec.Command(python, "-m", entrypoint)
	process.Dir = repo
	process.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"PYTHONPATH=" + filepath.Join(repo, "python"),
		"APP_ENV=test",
		"RAG_PROFILE=v2_langgraph_fixture",
		"V2_GRPC_ADDR=" + address,
		"V2_GRPC_REFLECTION=0",
		"V2_FIXTURE_MODE=1",
		"AUTHORITY_HTTP_BASE=" + authority.URL,
	}
	if err = process.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = process.Process.Kill()
		_, _ = process.Process.Wait()
	}()

	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	client := ragv1.NewRagServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	scope := &ragv1.RequestContext{
		RequestId:            "go-v2-boundary",
		TenantId:             "v2-demo-tenant",
		UserId:               "dev-user",
		AllowedShopIds:       []string{"shop-demo"},
		AllowedDocumentIds:   []string{"v2-doc-bottle"},
		ShopId:               "shop-demo",
		Role:                 "operator",
		PermissionRevision:   "1",
		EnforceDocumentScope: true,
		ConversationId:       "conversation-1",
		TurnId:               "turn-1",
		ExecutionId:          "execution-1",
		ScopeAudience:        "ai",
	}
	var understood *ragv1.UnderstandResponse
	for {
		understood, err = client.Understand(ctx, &ragv1.UnderstandRequest{Context: scope, Query: "保温杯容量是多少？"}, grpc.WaitForReady(true))
		if err == nil {
			break
		}
		if status.Code(err) != codes.Unavailable && status.Code(err) != codes.DeadlineExceeded {
			t.Fatal(err)
		}
		if ctx.Err() != nil {
			t.Fatal("Python v2 gRPC process did not become ready")
		}
		time.Sleep(30 * time.Millisecond)
	}
	if understood.Intent != "knowledge" || understood.RewrittenQuery == "" {
		t.Fatalf("unexpected Understand response: %#v", understood)
	}
	saved, err := client.SaveCheckpoint(ctx, &ragv1.SaveCheckpointRequest{Context: scope, PendingKind: "ASKING"})
	if err != nil || saved == nil || !saved.Accepted {
		t.Fatalf("v2 checkpoint save failed: %v", err)
	}
	resumed, err := client.ResumeCheckpoint(ctx, &ragv1.ResumeCheckpointRequest{Context: scope, PendingKind: "ASKING"})
	if err != nil || resumed == nil || !resumed.Accepted || resumed.ThreadId != saved.ThreadId {
		t.Fatalf("v2 checkpoint resume failed: %v", err)
	}
	stream, err := client.Search(ctx, &ragv1.SearchRequest{Context: scope, Query: understood.RewrittenQuery, ResponsePurpose: "customer_reply"})
	if err != nil {
		t.Fatal(err)
	}
	var evidence []*ragv1.Evidence
	for {
		part, recvErr := stream.Recv()
		if recvErr == nil {
			evidence = append(evidence, part.Evidence...)
			if part.Complete {
				break
			}
			continue
		}
		if status.Code(recvErr) == codes.Canceled || status.Code(recvErr) == codes.DeadlineExceeded {
			t.Fatal(recvErr)
		}
		t.Fatal(recvErr)
	}
	if len(evidence) != 1 || evidence[0].DocumentId != "v2-doc-bottle" {
		t.Fatalf("unexpected v2 evidence: %#v", evidence)
	}
	generated, err := client.Generate(ctx, &ragv1.GenerateRequest{Context: scope, Query: understood.RewrittenQuery, Evidence: evidence, ResponsePurpose: "customer_reply"})
	if err != nil {
		t.Fatal(err)
	}
	var done, copyable bool
	for {
		part, recvErr := generated.Recv()
		if recvErr != nil {
			t.Fatal(recvErr)
		}
		switch event := part.Event.(type) {
		case *ragv1.GenerateResponse_Done:
			done = event.Done
		case *ragv1.GenerateResponse_CanCopy:
			copyable = event.CanCopy
		}
		if done {
			break
		}
	}
	if !done || copyable {
		t.Fatalf("v2 Generate did not preserve non-copyable fixture contract: done=%t can_copy=%t", done, copyable)
	}

	deadlineCtx, deadlineCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Millisecond))
	defer deadlineCancel()
	_, err = client.Understand(deadlineCtx, &ragv1.UnderstandRequest{Context: scope, Query: "超时测试"})
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("expired Go deadline was not preserved by gRPC: %v", err)
	}
	cancelCtx, cancelRequest := context.WithCancel(context.Background())
	cancelRequest()
	_, err = client.Understand(cancelCtx, &ragv1.UnderstandRequest{Context: scope, Query: "取消测试"})
	if status.Code(err) != codes.Canceled {
		t.Fatalf("Go cancellation was not preserved by gRPC: %v", err)
	}
}
