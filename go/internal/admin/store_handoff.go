package admin

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// HandoffSummary counts customer conversations that entered the human queue in
// one window and conversations with at least one customer message in the same
// window. The rate is nil when the denominator is zero; that is different from
// a measured zero.
type HandoffSummary struct {
	HandoffConversations  int64    `json:"handoff_conversations"`
	CustomerConversations int64    `json:"customer_conversations"`
	HandoffRate           *float64 `json:"handoff_rate"`
}

// CustomerHandoff reads customer_event and customer_message without changing
// either table. tenantID always comes from the admin session; shops narrows a
// non-tenant-wide grant. The event type is checked because emit writes the
// current state into every event, including message events.
func (s *Store) CustomerHandoff(ctx context.Context, db *pgxpool.Pool, tenantID int64, shops []string,
	tenantWide bool, from, to time.Time) (HandoffSummary, error) {
	shopFilter := ""
	args := []any{tenantID, from, to}
	if !tenantWide {
		shopFilter = " AND c.shop_id = ANY($4::text[])"
		args = append(args, shops)
	}
	query := `/* customer_handoff_readonly_v1 */
		WITH handoffs AS (
			SELECT DISTINCT e.conversation_id
			FROM customer_event e
			JOIN customer_conversation c ON c.tenant_id=e.tenant_id AND c.id=e.conversation_id
			WHERE e.tenant_id=$1 AND e.created_at >= $2 AND e.created_at < $3
				AND e.event_type='state' AND e.data_json->>'state'='WAITING_HUMAN'` + shopFilter + `
		), messages AS (
			SELECT DISTINCT m.conversation_id
			FROM customer_message m
			JOIN customer_conversation c ON c.tenant_id=m.tenant_id AND c.id=m.conversation_id
			WHERE m.tenant_id=$1 AND m.created_at >= $2 AND m.created_at < $3
				AND m.source='customer'` + shopFilter + `
		)
		SELECT (SELECT count(*) FROM handoffs), (SELECT count(*) FROM messages)`
	var summary HandoffSummary
	if err := db.QueryRow(ctx, query, args...).Scan(&summary.HandoffConversations, &summary.CustomerConversations); err != nil {
		return HandoffSummary{}, err
	}
	summary.HandoffRate = handoffRate(summary.HandoffConversations, summary.CustomerConversations)
	return summary, nil
}

// handoffRate keeps the null-denominator rule in one place so the API and its
// tests cannot disagree about 0/0 versus 0/N.
func handoffRate(numerator, denominator int64) *float64 {
	if denominator == 0 {
		return nil
	}
	value := float64(numerator) / float64(denominator)
	return &value
}
