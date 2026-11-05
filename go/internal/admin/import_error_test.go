package admin

import (
	"strings"
	"testing"
)

func TestSafeJobErrorRedactsAndTruncates(t *testing.T) {
	if got := safeJobError(nil); got != nil {
		t.Fatalf("nil error = %v, want nil", got)
	}
	credential := "pass" + "word"
	secret := "postgres" + "ql://user:secret@db.example/app " + credential + "=secret host=db.example"
	got, _ := safeJobError(&secret).(string)
	for _, forbidden := range []string{"secret", "postgresql://", "host=db.example"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("safeJobError leaked %q: %q", forbidden, got)
		}
	}
	long := strings.Repeat("x", 300)
	got, _ = safeJobError(&long).(string)
	if len([]rune(got)) != 201 || !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated length=%d suffix=%q", len([]rune(got)), got[len(got)-3:])
	}
}
