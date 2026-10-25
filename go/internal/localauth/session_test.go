package localauth

import (
	"regexp"
	"testing"
	"time"
)

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestNewSessionMintsOpaqueTokenAndMatchingHashes(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	session, cookie, csrf, err := NewSession(7, 11, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if !hex64.MatchString(session.IDHash) || !hex64.MatchString(session.CSRFHash) {
		t.Fatalf("hash shape does not satisfy the database constraint: %q %q", session.IDHash, session.CSRFHash)
	}
	if cookie == csrf || cookie == "" || csrf == "" {
		t.Fatal("cookie and csrf must be distinct secrets")
	}
	if HashToken(cookie) != session.IDHash || HashToken(csrf) != session.CSRFHash {
		t.Fatal("stored hashes do not match the minted values")
	}
	if session.MustChangePassword != true {
		t.Fatal("must_change_password was dropped")
	}
	if !session.IdleExpiresAt.Equal(now.Add(IdleTTL)) || !session.AbsoluteExpiresAt.Equal(now.Add(AbsoluteTTL)) {
		t.Fatal("unexpected expiry")
	}
}

func TestSessionExpiryAndSlidingCap(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	session, _, _, err := NewSession(7, 11, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if session.Expired(now) {
		t.Fatal("fresh session expired")
	}
	if !session.Expired(now.Add(IdleTTL)) {
		t.Fatal("idle expiry not enforced")
	}
	if !session.Expired(now.Add(AbsoluteTTL)) {
		t.Fatal("absolute expiry not enforced")
	}
	late := now.Add(AbsoluteTTL - time.Minute)
	next := session.NextIdleExpiry(late)
	if !next.Equal(session.AbsoluteExpiresAt) {
		t.Fatalf("sliding expiry passed the absolute cap: %v", next)
	}
	if session.NextIdleExpiry(now).After(session.AbsoluteExpiresAt) {
		t.Fatal("idle expiry exceeded the absolute cap")
	}
}

func TestTokenShape(t *testing.T) {
	if err := ValidateTokenShape("short"); err == nil {
		t.Fatal("short token accepted")
	}
	long := make([]byte, 129)
	for i := range long {
		long[i] = 'a'
	}
	if err := ValidateTokenShape(string(long)); err == nil {
		t.Fatal("oversized token accepted")
	}
}
