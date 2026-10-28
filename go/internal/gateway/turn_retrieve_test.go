package gateway

import (
	"testing"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

func evidence(id string) *ragv1.Evidence {
	return &ragv1.Evidence{Id: id, DocumentId: "doc-" + id, VersionId: "v-" + id,
		ShopId: "shop-a", DisclosureClass: "external_allowed", CustomerEligible: true, Rank: 1}
}

func ids(items []*ragv1.Evidence) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.GetId())
	}
	return out
}

func equalIDs(got []*ragv1.Evidence, want ...string) bool {
	gotIDs := ids(got)
	if len(gotIDs) != len(want) {
		return false
	}
	for index := range want {
		if gotIDs[index] != want[index] {
			return false
		}
	}
	return true
}

func TestFilterEvidencePermission(t *testing.T) {
	raw := []*ragv1.Evidence{evidence("allowed"), evidence("denied")}
	grants := []evidenceGrant{{allowed: true}, {allowed: false}}
	if got := filterEvidence(raw, grants, true); !equalIDs(got, "allowed") {
		t.Fatalf("permission filter kept %v, want [allowed]", ids(got))
	}
	// Missing grants count as denied.
	if got := filterEvidence(raw, []evidenceGrant{{allowed: true}}, true); !equalIDs(got, "allowed") {
		t.Fatalf("short grants kept %v, want [allowed]", ids(got))
	}
}

func TestFilterEvidenceDisclosureClass(t *testing.T) {
	internal := evidence("internal")
	internal.DisclosureClass = "internal_only"
	raw := []*ragv1.Evidence{evidence("external"), internal}
	grants := []evidenceGrant{{allowed: true}, {allowed: true}}
	if got := filterEvidence(raw, grants, true); !equalIDs(got, "external") {
		t.Fatalf("disclosure filter kept %v, want [external]", ids(got))
	}
}

func TestFilterEvidenceCustomerEligibility(t *testing.T) {
	ineligible := evidence("ineligible")
	ineligible.CustomerEligible = false
	raw := []*ragv1.Evidence{evidence("eligible"), ineligible}
	grants := []evidenceGrant{{allowed: true}, {allowed: true}}
	if got := filterEvidence(raw, grants, true); !equalIDs(got, "eligible") {
		t.Fatalf("eligibility filter kept %v, want [eligible]", ids(got))
	}
	if got := filterEvidence(raw, grants, false); !equalIDs(got, "eligible", "ineligible") {
		t.Fatalf("without the eligibility rule kept %v", ids(got))
	}
}

func TestFilterEvidenceEmptyAndNil(t *testing.T) {
	if got := filterEvidence(nil, nil, true); len(got) != 0 {
		t.Fatalf("nil input produced %v", ids(got))
	}
	raw := []*ragv1.Evidence{nil, evidence("x")}
	grants := []evidenceGrant{{allowed: false}, {allowed: true}}
	if got := filterEvidence(raw, grants, true); !equalIDs(got, "x") {
		t.Fatalf("nil entry handling kept %v, want [x]", ids(got))
	}
}
