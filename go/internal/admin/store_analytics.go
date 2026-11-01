package admin

import (
	"context"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ResolutionSummary is the merchant-facing outcome metric for one time window.
// turns_total is every turn the window returned; scored_total is the rate
// denominator and deliberately excludes unknown turns. Rates are pointers so
// "no data" (scored_total=0) serializes as null, not 0: the UI must be able to
// tell "0% solved" apart from "nothing happened yet".
type ResolutionSummary struct {
	TurnsTotal   int64    `json:"turns_total"`
	ScoredTotal  int64    `json:"scored_total"`
	Solved       int64    `json:"solved"`
	Clarified    int64    `json:"clarified"`
	Failed       int64    `json:"failed"`
	Unknown      int64    `json:"unknown"`
	AutoRate     *float64 `json:"auto_rate"`
	AssistedRate *float64 `json:"assisted_rate"`
	Truncated    bool     `json:"truncated"`
}

// resolutionFetchLimit bounds how many turns one request reads into memory.
// The query asks for one extra row only to detect that the bound was hit.
const resolutionFetchLimit = 50000

// resolutionTurn is one turn in the window with the state its decision row
// recorded. A nil State means the turn has no turn_decision row (or a row from
// before 0041); those are counted as unknown, never as failures.
type resolutionTurn struct {
	ConversationID int64
	CreatedAt      time.Time
	State          *string
}

// ResolutionSummary reads every turn in the window and classifies it. The
// classification lives here, next to its only query, so the five rules cannot
// drift apart: a DONE turn counts as solved only when the conversation moved
// on no later than five minutes after it; an ASKING turn counts as clarified
// only when the same conversation later reached DONE; unknown turns are
// excluded from the denominator.
func (s *Store) ResolutionSummary(ctx context.Context, db *pgxpool.Pool, tenantID int64, from time.Time) (ResolutionSummary, error) {
	rows, err := db.Query(ctx, `SELECT t.conversation_id, t.created_at, td.state
		FROM turn t
		LEFT JOIN turn_decision td ON td.tenant_id=t.tenant_id AND td.turn_id=t.id::text
		WHERE t.tenant_id=$1 AND t.created_at>=$2
		ORDER BY t.created_at DESC
		LIMIT $3`, tenantID, from, resolutionFetchLimit+1)
	if err != nil {
		return ResolutionSummary{}, err
	}
	defer rows.Close()
	turns := make([]resolutionTurn, 0)
	for rows.Next() {
		var turn resolutionTurn
		if err := rows.Scan(&turn.ConversationID, &turn.CreatedAt, &turn.State); err != nil {
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
	summary := summarizeResolution(turns)
	summary.Truncated = truncated
	return summary, nil
}

// summarizeResolution applies the outcome rules to one window of turns. It is
// pure so the three edge cases (empty window, normal counts, unknown excluded)
// can be tested without a database.
func summarizeResolution(turns []resolutionTurn) ResolutionSummary {
	summary := ResolutionSummary{TurnsTotal: int64(len(turns))}
	byConversation := make(map[int64][]resolutionTurn)
	for _, turn := range turns {
		byConversation[turn.ConversationID] = append(byConversation[turn.ConversationID], turn)
	}
	const resolutionWindow = 5 * time.Minute
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
				if !hasNewerWithin(conversation, index, resolutionWindow) {
					summary.Solved++
				}
			case "ASKING":
				if hasLaterDone(conversation, index) {
					summary.Clarified++
				}
			case "FAILED":
				summary.Failed++
			}
		}
	}
	resolved := summary.Solved + summary.Clarified + summary.Failed
	summary.ScoredTotal = resolved
	if resolved > 0 {
		auto := float64(summary.Solved) / float64(resolved)
		assisted := float64(summary.Solved+summary.Clarified) / float64(resolved)
		summary.AutoRate = &auto
		summary.AssistedRate = &assisted
	}
	return summary
}

// hasNewerWithin reports whether the conversation gained a strictly newer turn
// no later than window after index.
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

// hasLaterDone reports whether the conversation reached DONE after index.
func hasLaterDone(conversation []resolutionTurn, index int) bool {
	created := conversation[index].CreatedAt
	for next := index + 1; next < len(conversation); next++ {
		if !conversation[next].CreatedAt.After(created) {
			continue
		}
		if conversation[next].State != nil && *conversation[next].State == "DONE" {
			return true
		}
	}
	return false
}
