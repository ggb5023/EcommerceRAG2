package admin

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ResolutionSummary is the merchant-facing outcome metric for one time window.
// turns_total is every turn the window returned; scored_total is the five
// settled buckets (solved, clarified, abandoned, failed, cancelled). Pending
// ASKING turns are right-censored and unknown turns have no ledger row, so
// turns_total = scored_total + unknown + pending.
//
// Both rates share the scored_total denominator defined in docs/metrics.md:
// auto_rate = solved/scored_total and assisted_rate = (solved+clarified)/
// scored_total. A nil rate means scored_total is zero, which is different from
// a real 0%.
type ResolutionSummary struct {
	TurnsTotal  int64 `json:"turns_total"`
	ScoredTotal int64 `json:"scored_total"`
	Solved      int64 `json:"solved"`
	Clarified   int64 `json:"clarified"`
	Abandoned   int64 `json:"abandoned"`
	Failed      int64 `json:"failed"`
	Cancelled   int64 `json:"cancelled"`
	Pending     int64 `json:"pending"`
	Unknown     int64 `json:"unknown"`
	// ReaskedWithin5m is a diagnostic only: it is never part of a rate because
	// a follow-up question may be a new topic, not dissatisfaction.
	ReaskedWithin5m int64    `json:"reasked_within_5m"`
	AutoRate        *float64 `json:"auto_rate"`
	AssistedRate    *float64 `json:"assisted_rate"`
	Truncated       bool     `json:"truncated"`
	MetricGap       bool     `json:"metric_gap"`
}

// resolutionFetchLimit bounds how many turns one request reads into memory.
// The query asks for one extra row only to detect that the bound was hit.
const resolutionFetchLimit = 50000

// Suggestion turns are internal M6 workbench executions, not merchant outcomes
// or knowledge gaps. Both analytics queries filter them at the source.
const resolutionSummarySQL = `SELECT t.id, t.conversation_id, t.parent_turn_id, t.created_at,
			latest.state, latest.finished_at
		FROM turn t
		LEFT JOIN LATERAL (
			SELECT td.state, e.finished_at
			FROM conversation_execution e
			LEFT JOIN turn_decision td ON td.tenant_id=e.tenant_id AND td.execution_id=e.id
			WHERE e.tenant_id=t.tenant_id AND e.turn_id=t.id
			ORDER BY e.execution_no DESC
			LIMIT 1
		) latest ON true
		WHERE t.tenant_id=$1 AND t.created_at>=$2 AND t.kind='user'
		ORDER BY t.created_at DESC
		LIMIT $3`

const knowledgeGapsSQL = `WITH latest AS (
			SELECT t.id, t.conversation_id, t.parent_turn_id, t.created_at, t.user_query,
				decision.state, decision.finished_at
			FROM turn t
			LEFT JOIN LATERAL (
				SELECT td.state, e.finished_at
				FROM conversation_execution e
				LEFT JOIN turn_decision td ON td.tenant_id=e.tenant_id AND td.execution_id=e.id
				WHERE e.tenant_id=t.tenant_id AND e.turn_id=t.id
				ORDER BY e.execution_no DESC
				LIMIT 1
			) decision ON true
			WHERE t.tenant_id=$1 AND t.created_at>=$2 AND t.kind='user'
		)
		SELECT id, conversation_id, parent_turn_id, created_at, user_query, state, finished_at
		FROM latest
		WHERE state IN ('FAILED','ASKING')
		   OR (state='DONE' AND id IN (
		       SELECT parent_turn_id FROM latest WHERE state='ASKING' AND parent_turn_id IS NOT NULL
		   ))
		ORDER BY created_at DESC
		LIMIT $3`

// pendingGrace is the right-censoring window for ASKING turns: younger than
// this from the ASKING terminal state, the customer may still answer.
const pendingGrace = 10 * time.Minute

// resolutionTurn is one turn in the window with its parent link and the state
// and finish time recorded for its latest execution. A nil State means that
// execution has no decision row (or a row from before 0041); those are counted
// as unknown, never as failures.
type resolutionTurn struct {
	TurnID         int64
	ConversationID int64
	ParentTurnID   *int64
	CreatedAt      time.Time
	UserQuery      string
	State          *string
	FinishedAt     *time.Time
}

// ResolutionSummary reads every turn in the window and classifies it. The
// classification lives here, next to its only query, so the buckets cannot
// drift apart. Priority for ASKING: a later child turn linked by parent_turn_id
// whose latest execution is DONE makes this turn clarified; otherwise a turn
// still inside the grace window is pending; everything else is abandoned.
// DONE is solved (a resumed execution is still the same turn), FAILED and
// CANCELLED are separate buckets. The ledger is keyed by execution, so the
// state is read from the turn's latest execution; rows that 0042 could not map
// (NULL execution_id) are excluded.
func (s *Store) ResolutionSummary(ctx context.Context, db *pgxpool.Pool, tenantID int64, from time.Time) (ResolutionSummary, error) {
	rows, err := db.Query(ctx, resolutionSummarySQL, tenantID, from, resolutionFetchLimit+1)
	if err != nil {
		return ResolutionSummary{}, err
	}
	defer rows.Close()
	turns := make([]resolutionTurn, 0)
	for rows.Next() {
		var turn resolutionTurn
		if err := rows.Scan(&turn.TurnID, &turn.ConversationID, &turn.ParentTurnID, &turn.CreatedAt,
			&turn.State, &turn.FinishedAt); err != nil {
			return ResolutionSummary{}, err
		}
		turns = append(turns, turn)
	}
	if err := rows.Err(); err != nil {
		return ResolutionSummary{}, err
	}
	truncated := len(turns) > resolutionFetchLimit
	if truncated {
		turns = turns[:resolutionFetchLimit]
	}
	summary := summarizeResolution(turns, time.Now())
	summary.Truncated = truncated
	return summary, nil
}

// summarizeResolution applies the outcome rules to one window of turns. It is
// pure except for the supplied clock, so the edge cases can be tested without a
// database.
func summarizeResolution(turns []resolutionTurn, now time.Time) ResolutionSummary {
	summary := ResolutionSummary{TurnsTotal: int64(len(turns))}
	byConversation := make(map[int64][]resolutionTurn)
	for _, turn := range turns {
		byConversation[turn.ConversationID] = append(byConversation[turn.ConversationID], turn)
	}
	clarifiedParents := clarifiedParentSet(turns)
	for _, conversation := range byConversation {
		sort.SliceStable(conversation, func(i, j int) bool {
			return conversation[i].CreatedAt.Before(conversation[j].CreatedAt)
		})
		for index, turn := range conversation {
			if turn.State == nil {
				summary.Unknown++
				continue
			}
			switch *turn.State {
			case "DONE":
				summary.Solved++
				if hasNewerWithin(conversation, index, reaskWindow) {
					summary.ReaskedWithin5m++
				}
			case "ASKING":
				switch askingOutcome(turn, clarifiedParents[turn.TurnID], now) {
				case askingClarified:
					summary.Clarified++
				case askingPending:
					summary.Pending++
				default:
					summary.Abandoned++
				}
			case "FAILED":
				summary.Failed++
			case "CANCELLED":
				summary.Cancelled++
			}
		}
	}
	summary.ScoredTotal = summary.Solved + summary.Clarified + summary.Abandoned + summary.Failed + summary.Cancelled
	// both rates share the scored_total denominator (docs/metrics.md)
	if summary.ScoredTotal > 0 {
		auto := float64(summary.Solved) / float64(summary.ScoredTotal)
		assisted := float64(summary.Solved+summary.Clarified) / float64(summary.ScoredTotal)
		summary.AutoRate = &auto
		summary.AssistedRate = &assisted
	}
	return summary
}

// isPending reports whether an ASKING turn is still inside its grace window.
// The clock starts at the execution's finished_at; older rows without one fall
// back to the turn's created_at.
func isPending(turn resolutionTurn, now time.Time) bool {
	base := turn.CreatedAt
	if turn.FinishedAt != nil {
		base = *turn.FinishedAt
	}
	return now.Sub(base) < pendingGrace
}

const (
	askingClarified = "clarified"
	askingPending   = "pending"
	askingAbandoned = "abandoned"
)

// clarifiedParentSet maps a parent turn to true when a later child linked by
// parent_turn_id reached DONE. It is shared by the outcome summary and the gap
// list so "clarified" has one definition.
func clarifiedParentSet(turns []resolutionTurn) map[int64]bool {
	byID := make(map[int64]resolutionTurn, len(turns))
	for _, turn := range turns {
		byID[turn.TurnID] = turn
	}
	clarified := make(map[int64]bool)
	for _, child := range turns {
		if child.ParentTurnID == nil || child.State == nil || *child.State != "DONE" {
			continue
		}
		parent, ok := byID[*child.ParentTurnID]
		if ok && child.CreatedAt.After(parent.CreatedAt) {
			clarified[parent.TurnID] = true
		}
	}
	return clarified
}

// askingOutcome is the single ASKING classifier shared by ResolutionSummary
// and KnowledgeGaps. Priority: clarified > pending > abandoned.
func askingOutcome(turn resolutionTurn, clarified bool, now time.Time) string {
	switch {
	case clarified:
		return askingClarified
	case isPending(turn, now):
		return askingPending
	default:
		return askingAbandoned
	}
}

const reaskWindow = 5 * time.Minute

// hasNewerWithin reports whether the conversation gained a strictly newer turn
// no later than window after index. It feeds the diagnostic counter only.
func hasNewerWithin(conversation []resolutionTurn, index int, window time.Duration) bool {
	created := conversation[index].CreatedAt
	for next := index + 1; next < len(conversation); next++ {
		at := conversation[next].CreatedAt
		if !at.After(created) {
			continue
		}
		return !at.After(created.Add(window))
	}
	return false
}

// KnowledgeGap is one normalized query cluster that needs documentation or
// content work. Query is the most recent original wording, Normalized is the
// aggregation key.
type KnowledgeGap struct {
	Query      string    `json:"query"`
	Normalized string    `json:"normalized"`
	Count      int64     `json:"count"`
	LastSeen   time.Time `json:"last_seen"`
	Reason     string    `json:"reason"`
}

// KnowledgeGaps reads the window's abandoned and failed turns and aggregates
// them by normalized query. Classification uses the same askingOutcome helper
// as ResolutionSummary; the query also fetches DONE children of ASKING turns so
// parent-linked clarification can be evaluated in Go.
func (s *Store) KnowledgeGaps(ctx context.Context, db *pgxpool.Pool, tenantID int64, from time.Time, minCount int64) ([]KnowledgeGap, bool, error) {
	rows, err := db.Query(ctx, knowledgeGapsSQL, tenantID, from, resolutionFetchLimit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	turns := make([]resolutionTurn, 0)
	for rows.Next() {
		var turn resolutionTurn
		if err := rows.Scan(&turn.TurnID, &turn.ConversationID, &turn.ParentTurnID, &turn.CreatedAt,
			&turn.UserQuery, &turn.State, &turn.FinishedAt); err != nil {
			return nil, false, err
		}
		turns = append(turns, turn)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	truncated := len(turns) > resolutionFetchLimit
	if truncated {
		turns = turns[:resolutionFetchLimit]
	}
	return summarizeKnowledgeGaps(turns, minCount, time.Now()), truncated, nil
}

// summarizeKnowledgeGaps keeps only abandoned and failed turns, normalizes
// their queries, and aggregates by the normalized text. Pending, clarified,
// cancelled and unknown turns never enter the gap list.
func summarizeKnowledgeGaps(turns []resolutionTurn, minCount int64, now time.Time) []KnowledgeGap {
	if minCount < 1 {
		minCount = 1
	}
	clarifiedParents := clarifiedParentSet(turns)
	byKey := make(map[string]*KnowledgeGap)
	for _, turn := range turns {
		if turn.State == nil {
			continue
		}
		reason := ""
		switch *turn.State {
		case "FAILED":
			reason = "failed"
		case "ASKING":
			if askingOutcome(turn, clarifiedParents[turn.TurnID], now) != askingAbandoned {
				continue
			}
			reason = "abandoned"
		default:
			continue
		}
		normalized := normalizeGapQuery(turn.UserQuery)
		if normalized == "" {
			continue
		}
		item, ok := byKey[normalized]
		if !ok {
			byKey[normalized] = &KnowledgeGap{Query: turn.UserQuery, Normalized: normalized,
				Count: 1, LastSeen: turn.CreatedAt, Reason: reason}
			continue
		}
		item.Count++
		if turn.CreatedAt.After(item.LastSeen) {
			item.LastSeen = turn.CreatedAt
			item.Query = turn.UserQuery
			item.Reason = reason
		}
	}
	items := make([]KnowledgeGap, 0, len(byKey))
	for _, item := range byKey {
		if item.Count >= minCount {
			items = append(items, *item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count
		}
		if !items[i].LastSeen.Equal(items[j].LastSeen) {
			return items[i].LastSeen.After(items[j].LastSeen)
		}
		return items[i].Normalized < items[j].Normalized
	})
	return items
}

// normalizeGapQuery lowercases, drops punctuation, and collapses whitespace.
// It deliberately does not merge synonyms or correct spelling.
func normalizeGapQuery(query string) string {
	var builder strings.Builder
	space := false
	for _, r := range strings.ToLower(strings.TrimSpace(query)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if space && builder.Len() > 0 {
				builder.WriteByte(' ')
			}
			builder.WriteRune(r)
			space = false
		case unicode.IsSpace(r):
			space = true
		}
	}
	return builder.String()
}
