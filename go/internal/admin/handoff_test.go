package admin

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHandoffRateNumeratorDenominatorAndNull(t *testing.T) {
	cases := []struct {
		name        string
		numerator   int64
		denominator int64
		want        *float64
	}{
		{name: "measured fraction", numerator: 2, denominator: 8, want: floatPtr(0.25)},
		{name: "zero numerator is measured zero", numerator: 0, denominator: 4, want: floatPtr(0)},
		{name: "zero denominator is null", numerator: 0, denominator: 0, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := handoffRate(tc.numerator, tc.denominator)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("handoffRate(%d,%d) = %v, want nil", tc.numerator, tc.denominator, *got)
				}
				return
			}
			if got == nil || *got != *tc.want {
				t.Fatalf("handoffRate(%d,%d) = %v, want %v", tc.numerator, tc.denominator, got, *tc.want)
			}
		})
	}
}

func TestHandoffSummarySerializesNullRate(t *testing.T) {
	body, err := json.Marshal(HandoffSummary{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"handoff_rate":null`) {
		t.Fatalf("null denominator must serialize as null: %s", body)
	}
}

func floatPtr(value float64) *float64 { return &value }
