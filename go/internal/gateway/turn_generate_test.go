package gateway

import (
	"testing"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

func TestValidateCitationAcceptsAcceptedEvidence(t *testing.T) {
	original := evidence("a")
	copyOfOriginal := evidence("a")
	if !validateCitation(copyOfOriginal, []*ragv1.Evidence{original, evidence("b")}) {
		t.Fatal("an exact copy of an accepted candidate must validate")
	}
}

func TestValidateCitationRejectsUnknownOrAlteredEvidence(t *testing.T) {
	original := evidence("a")
	unknown := evidence("z")
	if validateCitation(unknown, []*ragv1.Evidence{original}) {
		t.Fatal("evidence outside the accepted set must be rejected")
	}
	altered := evidence("a")
	altered.Rank = 9
	if validateCitation(altered, []*ragv1.Evidence{original}) {
		t.Fatal("any field difference must be rejected")
	}
	if validateCitation(evidence("a"), nil) {
		t.Fatal("an empty accepted set must reject everything")
	}
	if validateCitation(nil, []*ragv1.Evidence{original}) {
		t.Fatal("a nil citation must be rejected")
	}
}

// evidence is the fixture helper shared by the gateway tests.
func evidence(id string) *ragv1.Evidence {
	return &ragv1.Evidence{Id: id, DocumentId: "doc-" + id, VersionId: "v-" + id,
		ShopId: "shop-a", DisclosureClass: "external_allowed", CustomerEligible: true, Rank: 1}
}
