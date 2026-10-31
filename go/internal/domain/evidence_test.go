package domain

import (
	"errors"
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
	if len(got) != len(want) {
		return false
	}
	for index := range want {
		if got[index].GetId() != want[index] {
			return false
		}
	}
	return true
}

func TestFilterEvidencePermission(t *testing.T) {
	raw := []*ragv1.Evidence{evidence("allowed"), evidence("denied")}
	grants := []EvidenceGrant{{Allowed: true}, {Allowed: false}}
	if got, _, err := FilterEvidence(raw, grants, true); err != nil || !equalIDs(got, "allowed") {
		t.Fatalf("permission filter kept %v, want [allowed]", ids(got))
	}
	// Missing grants count as denied.
	if got, _, err := FilterEvidence(raw, []EvidenceGrant{{Allowed: true}}, true); err == nil || got != nil {
		t.Fatalf("short grants kept %v, want [allowed]", ids(got))
	}
}

func TestFilterEvidenceDisclosureClass(t *testing.T) {
	internal := evidence("internal")
	internal.DisclosureClass = "internal_only"
	raw := []*ragv1.Evidence{evidence("external"), internal}
	grants := []EvidenceGrant{{Allowed: true}, {Allowed: true}}
	if got, _, err := FilterEvidence(raw, grants, true); err != nil || !equalIDs(got, "external") {
		t.Fatalf("disclosure filter kept %v, want [external]", ids(got))
	}
}

func TestFilterEvidenceCustomerEligibility(t *testing.T) {
	ineligible := evidence("ineligible")
	ineligible.CustomerEligible = false
	raw := []*ragv1.Evidence{evidence("eligible"), ineligible}
	grants := []EvidenceGrant{{Allowed: true}, {Allowed: true}}
	if got, _, err := FilterEvidence(raw, grants, true); err != nil || !equalIDs(got, "eligible") {
		t.Fatalf("eligibility filter kept %v, want [eligible]", ids(got))
	}
	if got, _, err := FilterEvidence(raw, grants, false); err != nil || !equalIDs(got, "eligible", "ineligible") {
		t.Fatalf("without the eligibility rule kept %v", ids(got))
	}
}

func TestFilterEvidenceEmpty(t *testing.T) {
	if got, _, err := FilterEvidence(nil, nil, true); err != nil || len(got) != 0 {
		t.Fatalf("nil input produced %v, err=%v", ids(got), err)
	}
}

// A broken contract fails the turn closed instead of silently dropping evidence.
func TestFilterEvidenceRejectsNilItem(t *testing.T) {
	raw := []*ragv1.Evidence{nil, evidence("x")}
	grants := []EvidenceGrant{{Allowed: true}, {Allowed: true}}
	got, rejected, err := FilterEvidence(raw, grants, true)
	if err == nil || got != nil || len(rejected) != 0 {
		t.Fatalf("nil item must return an error and no evidence/rejection, got %v rejected=%v err=%v", ids(got), rejected, err)
	}
	var contract ErrEvidenceContract
	if !errors.As(err, &contract) || contract.Detail != "nil evidence item" {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Each rejection names the rule that dropped the candidate, so the stored
// decision row can answer "why was this evidence not used?".
func TestFilterEvidenceRejectedNotAuthorized(t *testing.T) {
	raw := []*ragv1.Evidence{evidence("denied")}
	got, rejected, err := FilterEvidence(raw, []EvidenceGrant{{Allowed: false}}, true)
	if err != nil || len(got) != 0 {
		t.Fatalf("kept %v, want none (err=%v)", ids(got), err)
	}
	if len(rejected) != 1 || rejected[0].ID != "denied" || rejected[0].Reason != "not_authorized" {
		t.Fatalf("rejected %v, want [{denied not_authorized}]", rejected)
	}
}

func TestFilterEvidenceRejectedDisclosureDenied(t *testing.T) {
	internal := evidence("internal")
	internal.DisclosureClass = "internal_only"
	got, rejected, err := FilterEvidence([]*ragv1.Evidence{internal}, []EvidenceGrant{{Allowed: true}}, true)
	if err != nil || len(got) != 0 {
		t.Fatalf("kept %v, want none (err=%v)", ids(got), err)
	}
	if len(rejected) != 1 || rejected[0].ID != "internal" || rejected[0].Reason != "disclosure_denied" {
		t.Fatalf("rejected %v, want [{internal disclosure_denied}]", rejected)
	}
}

func TestFilterEvidenceRejectedNotCustomerEligible(t *testing.T) {
	ineligible := evidence("ineligible")
	ineligible.CustomerEligible = false
	got, rejected, err := FilterEvidence([]*ragv1.Evidence{ineligible}, []EvidenceGrant{{Allowed: true}}, true)
	if err != nil || len(got) != 0 {
		t.Fatalf("kept %v, want none (err=%v)", ids(got), err)
	}
	if len(rejected) != 1 || rejected[0].ID != "ineligible" || rejected[0].Reason != "not_customer_eligible" {
		t.Fatalf("rejected %v, want [{ineligible not_customer_eligible}]", rejected)
	}
}

func TestFilterEvidenceRejectsGrantMismatch(t *testing.T) {
	raw := []*ragv1.Evidence{evidence("a"), evidence("b")}
	grants := []EvidenceGrant{{Allowed: true}}
	got, _, err := FilterEvidence(raw, grants, true)
	if err == nil || got != nil {
		t.Fatalf("missing grant must return an error and no evidence, got %v err=%v", ids(got), err)
	}
	var contract ErrEvidenceContract
	if !errors.As(err, &contract) || contract.Detail != "grant index mismatch" {
		t.Fatalf("unexpected error: %v", err)
	}
}
