package domain

import (
	"testing"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

func TestValidateCitationAcceptsAcceptedEvidence(t *testing.T) {
	original := evidence("a")
	if !ValidateCitation(evidence("a"), []*ragv1.Evidence{original, evidence("b")}) {
		t.Fatal("an exact copy of an accepted candidate must validate")
	}
}

func TestValidateCitationRejectsUnknownOrAlteredEvidence(t *testing.T) {
	original := evidence("a")
	if ValidateCitation(evidence("z"), []*ragv1.Evidence{original}) {
		t.Fatal("evidence outside the accepted set must be rejected")
	}
	altered := evidence("a")
	altered.Rank = 9
	if ValidateCitation(altered, []*ragv1.Evidence{original}) {
		t.Fatal("any field difference must be rejected")
	}
	if ValidateCitation(evidence("a"), nil) {
		t.Fatal("an empty accepted set must reject everything")
	}
	if ValidateCitation(nil, []*ragv1.Evidence{original}) {
		t.Fatal("a nil citation must be rejected")
	}
}
