package gateway

import (
	"testing"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

func TestCitationLabelsSurviveContextFiltering(t *testing.T) {
	items := []*ragv1.Evidence{{Id: "fixture-a", Rank: 2}, {Id: "fixture-b", Rank: 5}}
	if err := validateEvidenceRanks(items); err != nil {
		t.Fatal(err)
	}
	view := toEvidence(items)
	if view[0]["citationIndex"] != int32(2) || view[1]["citationIndex"] != int32(5) {
		t.Fatalf("context filtering renumbered citation labels: %v", view)
	}
}

func TestInvalidCitationLabelsFailClosed(t *testing.T) {
	for _, items := range [][]*ragv1.Evidence{
		{nil}, {{Id: "a"}}, {{Id: "a", Rank: -1}},
		{{Id: "a", Rank: 1}, {Id: "b", Rank: 1}},
		{{Id: "a", Rank: 1}, {Id: "a", Rank: 2}},
	} {
		if validateEvidenceRanks(items) == nil {
			t.Fatalf("accepted ambiguous citation labels: %v", items)
		}
	}
}
