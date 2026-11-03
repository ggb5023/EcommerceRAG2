package admin

import (
	"fmt"
	"time"
)

const validityNoticeDays = 30

// validityStatus uses the same signed business-date rule as the AI scope:
// effective_to is exclusive, so the expiry date itself is already expired.
func validityStatus(effectiveTo *time.Time, businessDate string) (string, int, error) {
	day, err := time.Parse("2006-01-02", businessDate)
	if err != nil {
		return "", 0, err
	}
	if effectiveTo == nil {
		return "active", 0, nil
	}
	expiry := time.Date(effectiveTo.Year(), effectiveTo.Month(), effectiveTo.Day(), 0, 0, 0, 0, time.UTC)
	days := int(expiry.Sub(day).Hours() / 24)
	switch {
	case !expiry.After(day):
		return "expired", days, nil
	case days <= validityNoticeDays:
		return fmt.Sprintf("expiring_in_%d", validityNoticeDays), days, nil
	default:
		return "active", days, nil
	}
}

func validityDateString(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.Format("2006-01-02")
}
