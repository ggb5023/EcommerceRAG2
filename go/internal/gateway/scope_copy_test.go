package gateway

import "testing"

// The per-request scoped copy must carry every long-lived client. A missing
// intel client made /admin/v1/intel/observations answer 503 forever.
func TestScopedGatewayKeepsLongLivedClients(t *testing.T) {
	root := &gateway{}
	scoped := root.scoped(mockSession{}, nil)
	if scoped.intel != root.intel {
		t.Fatal("scoped gateway dropped the intel client")
	}
	if scoped.intelConn != root.intelConn {
		t.Fatal("scoped gateway dropped the intel connection")
	}
	if scoped.rag != root.rag || scoped.ingest != root.ingest {
		t.Fatal("scoped gateway dropped a core RPC client")
	}
}
