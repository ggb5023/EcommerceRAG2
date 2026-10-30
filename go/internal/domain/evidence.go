// Package domain holds the pure decision rules shared by the gateway stages.
//
// Nothing here may touch a request, a transaction, a client or a clock: see
// scripts/check/check-domain-imports.py for the enforced boundary.
package domain

import ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"

// ErrEvidenceContract reports a broken contract between the orchestrator and the
// filter. It is a programming error, never a user error, so the caller fails the
// turn closed instead of continuing with partial data (invariant #9).
type ErrEvidenceContract struct {
	// Detail names the broken precondition; it is a fixed short string, never a
	// value that could carry user data.
	Detail string
}

func (e ErrEvidenceContract) Error() string { return "stage contract violated: " + e.Detail }

// EvidenceGrant records what the authorization layer decided about one candidate.
type EvidenceGrant struct {
	Allowed bool
}

// FilterEvidence applies the customer-facing rules to raw candidates. It is pure:
// no database, no context, no client. The document scope, resource disclosure
// class and active-version checks arrive pre-computed in grants.
func FilterEvidence(raw []*ragv1.Evidence, grants []EvidenceGrant, requireCustomerEligible bool) ([]*ragv1.Evidence, error) {
	keep := make([]*ragv1.Evidence, 0, len(raw))
	for index, item := range raw {
		if item == nil {
			return nil, ErrEvidenceContract{Detail: "nil evidence item"}
		}
		if index >= len(grants) {
			return nil, ErrEvidenceContract{Detail: "grant index mismatch"}
		}
		if !grants[index].Allowed {
			continue
		}
		if item.GetDisclosureClass() != "external_allowed" {
			continue
		}
		if requireCustomerEligible && !item.GetCustomerEligible() {
			continue
		}
		keep = append(keep, item)
	}
	return keep, nil
}
