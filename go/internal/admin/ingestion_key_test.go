package admin

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// The key rule is a cross-process contract: Python deduplicates on it, so a change
// here must change this test too.
func TestIngestIdempotencyKeyMatchesTheDocumentedRule(t *testing.T) {
	manifest := "schema_version: 1\nsource:\n  id: docs\n"
	sum := sha256.Sum256([]byte(manifest))
	want := "admin:shop-demo:" + hex.EncodeToString(sum[:])
	if got := ingestIdempotencyKey("shop-demo", manifest); got != want {
		t.Fatalf("key = %q, want %q", got, want)
	}
}

func TestIngestIdempotencyKeyIsStableAndShopScoped(t *testing.T) {
	manifest := "schema_version: 1\n"
	first := ingestIdempotencyKey("shop-a", manifest)
	if second := ingestIdempotencyKey("shop-a", manifest); first != second {
		t.Fatalf("same input produced %q and %q", first, second)
	}
	if other := ingestIdempotencyKey("shop-b", manifest); other == first {
		t.Fatal("different shops must not share a key")
	}
	if changed := ingestIdempotencyKey("shop-a", manifest+"\n"); changed == first {
		t.Fatal("a different manifest must not reuse the key")
	}
}
