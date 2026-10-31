package gateway

import (
	"context"
	"testing"
)

// A missing database must never break a turn: the decision row is explanatory.
func TestRecordTurnDecisionWithoutDatabaseDoesNotPanic(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("recordTurnDecision panicked without a database: %v", recovered)
		}
	}()
	recordTurnDecision(context.Background(), nil, turnEventIdentity{tenantID: 1}, "DONE", "", []string{"doc-a"}, nil)
}
