package localauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

// Session lifetimes and cookie material. Values are recorded in
// docs/decisions/0008.
const (
	IdleTTL       = 30 * time.Minute
	AbsoluteTTL   = 12 * time.Hour
	TouchInterval = 60 * time.Second
	tokenBytes    = 32
)

var ErrShortToken = errors.New("localauth: token material is too short")

// Session is the row the gateway resolves a cookie into. Revocation fields are
// filled by the store, not by the caller.
type Session struct {
	TenantID           int64
	UserID             int64
	IDHash             string
	CSRFHash           string
	CreatedAt          time.Time
	IdleExpiresAt      time.Time
	AbsoluteExpiresAt  time.Time
	MustChangePassword bool
	RevokedAt          *time.Time
	RevokedReason      string
}

// NewSession mints a cookie value, its stored hash and a CSRF token. The raw
// values are returned once and never persisted.
func NewSession(tenantID, userID int64, mustChange bool, now time.Time) (Session, string, string, error) {
	cookie, idHash, err := newSecret()
	if err != nil {
		return Session{}, "", "", err
	}
	csrf, csrfHash, err := newSecret()
	if err != nil {
		return Session{}, "", "", err
	}
	return Session{
		TenantID:           tenantID,
		UserID:             userID,
		IDHash:             idHash,
		CSRFHash:           csrfHash,
		CreatedAt:          now,
		IdleExpiresAt:      now.Add(IdleTTL),
		AbsoluteExpiresAt:  now.Add(AbsoluteTTL),
		MustChangePassword: mustChange,
	}, cookie, csrf, nil
}

// HashToken maps a cookie or CSRF value to the stored hex digest. The database
// constrains both columns to 64 hex characters.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// Expired reports whether the session must be treated as gone at now.
func (s Session) Expired(now time.Time) bool {
	return s.RevokedAt != nil || !now.Before(s.IdleExpiresAt) || !now.Before(s.AbsoluteExpiresAt)
}

// NextIdleExpiry returns the sliding expiry after a successful use, capped by
// the absolute deadline.
func (s Session) NextIdleExpiry(now time.Time) time.Time {
	next := now.Add(IdleTTL)
	if next.After(s.AbsoluteExpiresAt) {
		return s.AbsoluteExpiresAt
	}
	return next
}

func newSecret() (raw string, hashed string, err error) {
	buffer := make([]byte, tokenBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(buffer)
	return raw, HashToken(raw), nil
}

// ValidateTokenShape rejects empty or oversized cookie values before hashing.
func ValidateTokenShape(raw string) error {
	if len(raw) < 32 || len(raw) > 128 {
		return ErrShortToken
	}
	return nil
}
