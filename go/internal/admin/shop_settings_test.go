package admin

import "testing"

func TestValidShopTimezoneAcceptsIANAAndRejectsInvalidNames(t *testing.T) {
	for _, value := range []string{"Asia/Shanghai", "America/New_York", "Europe/Berlin"} {
		if !validShopTimezone(value) {
			t.Fatalf("%q should be accepted", value)
		}
	}
	for _, value := range []string{"", "Local", "Mars/Olympus", "../etc/passwd"} {
		if validShopTimezone(value) {
			t.Fatalf("%q should be rejected", value)
		}
	}
}
