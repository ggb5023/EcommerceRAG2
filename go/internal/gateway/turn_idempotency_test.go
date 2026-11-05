package gateway

import "testing"

func TestClassifyTurnIdempotency(t *testing.T) {
	for _, tc := range []struct {
		name       string
		found      bool
		storedHash string
		payload    string
		want       turnIdempotencyDecision
	}{
		{name: "not recorded creates", found: false, storedHash: "", payload: "hash-a", want: turnIdempotencyNew},
		{name: "same hash replays", found: true, storedHash: "hash-a", payload: "hash-a", want: turnIdempotencyReplay},
		{name: "different hash conflicts", found: true, storedHash: "hash-a", payload: "hash-b", want: turnIdempotencyConflict},
		{name: "empty stored hash replays empty payload", found: true, storedHash: "", payload: "", want: turnIdempotencyReplay},
		{name: "empty stored hash conflicts with non-empty payload", found: true, storedHash: "", payload: "hash-a", want: turnIdempotencyConflict},
		{name: "not found ignores hashes", found: false, storedHash: "hash-b", payload: "hash-a", want: turnIdempotencyNew},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyTurnIdempotency(tc.found, tc.storedHash, tc.payload); got != tc.want {
				t.Fatalf("classifyTurnIdempotency() = %v, want %v", got, tc.want)
			}
		})
	}
}
