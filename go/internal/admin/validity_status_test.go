package admin

import (
	"testing"
	"time"
)

func testDate(value string) *time.Time {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		panic(err)
	}
	return &parsed
}

func TestValidityStatusBoundaries(t *testing.T) {
	businessDate := "2026-11-03"
	cases := []struct {
		name string
		to   *time.Time
		want string
		days int
	}{
		{"unbounded", nil, "active", 0},
		{"expired-on-day", testDate("2026-11-03"), "expired", 0},
		{"expired-past", testDate("2026-11-02"), "expired", -1},
		{"expiring-thirtieth-day", testDate("2026-12-03"), "expiring_in_30", 30},
		{"active-thirty-first-day", testDate("2026-12-04"), "active", 31},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, days, err := validityStatus(tc.to, businessDate)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want || days != tc.days {
				t.Fatalf("status=%q days=%d, want %q/%d", got, days, tc.want, tc.days)
			}
		})
	}
}
