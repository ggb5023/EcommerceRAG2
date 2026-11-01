package gateway

import (
	"context"
	"testing"
)

// The decision row is written inside finishTurn's transaction; a caller that
// forgets the transaction must get an error instead of a separate write.
func TestAppendTurnDecisionRequiresTransaction(t *testing.T) {
	err := appendTurnDecision(context.Background(), nil, turnEventIdentity{tenantID: 1}, "DONE", "", []string{"doc-a"}, nil)
	if err == nil {
		t.Fatal("appendTurnDecision accepted a nil transaction")
	}
}
