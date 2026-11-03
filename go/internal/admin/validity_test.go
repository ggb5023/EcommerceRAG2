package admin

import "testing"

func TestValidValidityDates(t *testing.T) {
	for _, dates := range [][2]string{{"", ""}, {"2026-01-01", ""}, {"", "2026-01-01"}, {"2026-01-01", "2026-01-02"}} {
		if !validValidityDates(dates[0], dates[1]) {
			t.Fatalf("%v should be valid", dates)
		}
	}
	for _, dates := range [][2]string{{"2026-01-01", "2026-01-01"}, {"2026-01-02", "2026-01-01"}, {"2026-02-30", ""}, {"2026-1-1", ""}} {
		if validValidityDates(dates[0], dates[1]) {
			t.Fatalf("%v should be invalid", dates)
		}
	}
}
