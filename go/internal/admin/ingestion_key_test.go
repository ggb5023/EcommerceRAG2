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
	want := "admin:shop-demo:42:" + hex.EncodeToString(sum[:])
	if got := ingestIdempotencyKey("shop-demo", 42, manifest); got != want {
		t.Fatalf("key = %q, want %q", got, want)
	}
}

func TestIngestIdempotencyKeyIsStableAndShopScoped(t *testing.T) {
	manifest := "schema_version: 1\n"
	first := ingestIdempotencyKey("shop-a", 7, manifest)
	if second := ingestIdempotencyKey("shop-a", 7, manifest); first != second {
		t.Fatalf("same batch produced %q and %q", first, second)
	}
	if other := ingestIdempotencyKey("shop-b", 7, manifest); other == first {
		t.Fatal("different shops must not share a key")
	}
	if changed := ingestIdempotencyKey("shop-a", 8, manifest); changed == first {
		t.Fatal("two same-content batches must not share a key")
	}
	if changed := ingestIdempotencyKey("shop-a", 7, manifest+"\n"); changed == first {
		t.Fatal("a different manifest must not reuse the key")
	}
}

// Reimport after revoke is a new batch: it must not reuse the old batch's key.
func TestReimportAfterRevokeUsesANewBatchKey(t *testing.T) {
	manifest := "schema_version: 1\n"
	oldKey := ingestIdempotencyKey("shop-a", 10, manifest)
	newKey := ingestIdempotencyKey("shop-a", 11, manifest)
	if oldKey == newKey {
		t.Fatalf("reimport reused key %q", oldKey)
	}
}

func TestIngestIdempotencyKeyLegacyPathStaysShopAndManifestScoped(t *testing.T) {
	manifest := "schema_version: 1\n"
	if got, legacy := ingestIdempotencyKey("shop-a", 0, manifest), legacyIngestIdempotencyKey("shop-a", manifest); got != legacy {
		t.Fatalf("direct path key = %q, want legacy %q", got, legacy)
	}
}
