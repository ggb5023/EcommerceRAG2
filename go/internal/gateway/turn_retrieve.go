package gateway

import (
	"context"
	"errors"

	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// stageFailure is a terminal (state, code) pair produced by a stage. The
// orchestrator turns it into finish(...); stages never write the terminal row
// themselves.
type stageFailure struct {
	state string
	code  string
}

func (f *stageFailure) Error() string { return f.code }

// errStageContract reports a broken contract between the orchestrator and a
// stage. It is a programming error, never a user error, so the caller fails the
// turn closed instead of continuing with partial data (invariant #9).
type errStageContract struct {
	detail string
}

func (e errStageContract) Error() string { return "stage contract violated: " + e.detail }

// evidenceGrant records what the authorization layer decided about one candidate.
// It is materialised outside the pure filter because deciding it needs the
// database: document scope, resource disclosure class and the active version.
type evidenceGrant struct {
	allowed bool
}

// retrieveEvidence drains the retrieval stream and returns every candidate the
// provider produced, before authorization or disclosure filtering.
//
// aborted is true when the authorization callback already finished the
// execution; the caller must return without finishing again.
func retrieveEvidence(ctx context.Context, client ragv1.RagServiceClient, scope *ragv1.RequestContext,
	query string, mock bool, authorized func() bool) (raw []*ragv1.Evidence, failure *stageFailure, aborted bool) {
	search, err := client.Search(ctx, &ragv1.SearchRequest{Context: scope, Query: query,
		ResponsePurpose: "customer_reply"}, grpc.WaitForReady(true))
	if err != nil {
		return nil, grpcStageFailure(ctx, err), false
	}
	raw = make([]*ragv1.Evidence, 0)
	for {
		part, recvErr := search.Recv()
		if errors.Is(recvErr, context.Canceled) || errors.Is(recvErr, context.DeadlineExceeded) {
			return nil, grpcStageFailure(ctx, recvErr), false
		}
		if recvErr != nil {
			if status.Code(recvErr) == codes.Canceled || status.Code(recvErr) == codes.DeadlineExceeded {
				return nil, grpcStageFailure(ctx, recvErr), false
			}
			return nil, &stageFailure{state: "FAILED", code: "rag_search_failed"}, false
		}
		if part.IsMock != mock {
			return nil, &stageFailure{state: "FAILED", code: "provider_profile_mismatch"}, false
		}
		if !authorized() {
			return nil, nil, true
		}
		raw = append(raw, part.Evidence...)
		if part.Complete {
			break
		}
	}
	return raw, nil, false
}

// authorizeEvidence asks the authorization layer about every candidate. It is the
// only place that turns raw evidence into grants; the filter stays pure.
func authorizeEvidence(check func(*ragv1.Evidence) (bool, error), raw []*ragv1.Evidence) ([]evidenceGrant, *stageFailure) {
	grants := make([]evidenceGrant, len(raw))
	for index, item := range raw {
		allowed, err := check(item)
		if err != nil {
			return nil, &stageFailure{state: "FAILED", code: auth.FailureCode(err)}
		}
		grants[index] = evidenceGrant{allowed: allowed}
	}
	return grants, nil
}

// filterEvidence applies the customer-facing rules to raw candidates. It is pure:
// no database, no context, no client. The document scope, resource disclosure
// class and active-version checks arrive pre-computed in grants.
func filterEvidence(raw []*ragv1.Evidence, grants []evidenceGrant, requireCustomerEligible bool) ([]*ragv1.Evidence, error) {
	keep := make([]*ragv1.Evidence, 0, len(raw))
	for index, item := range raw {
		if item == nil {
			return nil, errStageContract{detail: "nil evidence item"}
		}
		if index >= len(grants) {
			return nil, errStageContract{detail: "grant index mismatch"}
		}
		if !grants[index].allowed {
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

// persistEvidence writes the accepted evidence rows for one execution. It checks
// authorization before every insert, exactly like the inline loop it replaces;
// aborted means the callback already finished the execution.
func persistEvidence(ctx context.Context, db *pgxpool.Pool, tenantID, executionID int64,
	evidence []*ragv1.Evidence, authorized func() bool) (aborted bool, failure *stageFailure) {
	for _, item := range evidence {
		if !authorized() {
			return true, nil
		}
		if _, err := db.Exec(ctx, `INSERT INTO evidence_snapshot(tenant_id,execution_id,evidence_id,document_id,version_id,source_ref,content,disclosure_class,customer_eligible,citation_index,shop_id)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(tenant_id,execution_id,evidence_id) DO NOTHING`,
			tenantID, executionID, item.Id, item.DocumentId, item.VersionId, item.SourceRef,
			item.Content, item.DisclosureClass, item.CustomerEligible, item.Rank, item.ShopId); err != nil {
			return false, &stageFailure{state: "FAILED", code: "persistence_failed"}
		}
	}
	return false, nil
}

// grpcStageFailure maps a transport error to the terminal state used by the turn.
func grpcStageFailure(ctx context.Context, err error) *stageFailure {
	state, code := grpcFailure(ctx, err)
	return &stageFailure{state: state, code: code}
}

// runRetrievalStage turns one query into authorized, persisted evidence rows.
// The evidence event is emitted by the caller, after the snapshot fallback check.
func runRetrievalStage(ctx context.Context, client ragv1.RagServiceClient, db *pgxpool.Pool, tenantID, executionID int64,
	scope *ragv1.RequestContext, query string, mock bool,
	authorized func() bool, check func(*ragv1.Evidence) (bool, error)) (evidence []*ragv1.Evidence, failure *stageFailure, aborted bool) {
	raw, searchFailure, searchAborted := retrieveEvidence(ctx, client, scope, query, mock, authorized)
	if searchAborted {
		return nil, nil, true
	}
	if searchFailure != nil {
		return nil, searchFailure, false
	}
	grants, grantFailure := authorizeEvidence(check, raw)
	if grantFailure != nil {
		return nil, grantFailure, false
	}
	evidence, filterErr := filterEvidence(raw, grants, true)
	if filterErr != nil {
		return nil, &stageFailure{state: "FAILED", code: "evidence_contract_violated"}, false
	}
	if validateEvidenceRanks(evidence) != nil {
		return nil, &stageFailure{state: "FAILED", code: "invalid_evidence_labels"}, false
	}
	if persistAborted, persistFailure := persistEvidence(ctx, db, tenantID, executionID, evidence, authorized); persistAborted {
		return nil, nil, true
	} else if persistFailure != nil {
		return nil, persistFailure, false
	}
	return evidence, nil, false
}
