package localauth

import (
	"sync"
	"time"
)

// IP limits are process-local because a single gateway owns the database (see
// docs/decisions/0008). They stop a single source from spraying passwords; the
// account lock in local_credential stops one account from being guessed.
const (
	IPMinuteLimit = 10
	IPHourLimit   = 60
	// ProbeLimit is how many failures from one IP inside ProbeWindow turn every
	// further auth response into 429 instead of revealing 423 lock state.
	ProbeLimit  = 20
	ProbeWindow = 10 * time.Minute
	maxEntries  = 4096
)

// Limiter is a sliding-window counter with an injectable clock.
type Limiter struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string][]time.Time
}

func NewLimiter(now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{now: now, entries: make(map[string][]time.Time)}
}

// Allow reports whether the source may attempt a login now. When it may not,
// the second value is the wait before the oldest window entry expires.
func (l *Limiter) Allow(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	recent := l.prune(ip, now, time.Hour)
	if len(recent) >= IPHourLimit {
		return false, recent[len(recent)-IPHourLimit].Add(time.Hour).Sub(now)
	}
	minuteAgo := now.Add(-time.Minute)
	perMinute := 0
	for _, at := range recent {
		if at.After(minuteAgo) {
			perMinute++
		}
	}
	if perMinute >= IPMinuteLimit {
		for _, at := range recent {
			if at.After(minuteAgo) {
				return false, at.Add(time.Minute).Sub(now)
			}
		}
	}
	return true, 0
}

// Record records one attempt from a source.
func (l *Limiter) Record(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.entries[ip] = append(l.pruneUnlocked(ip, now, time.Hour), now)
	l.evict(now)
}

// ProbeExceeded reports whether this source has produced enough failures that
// the response must stop distinguishing locked accounts (423 -> 429).
func (l *Limiter) ProbeExceeded(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cutoff := now.Add(-ProbeWindow)
	count := 0
	for _, at := range l.entries[ip] {
		if at.After(cutoff) {
			count++
		}
	}
	return count >= ProbeLimit
}

func (l *Limiter) prune(ip string, now time.Time, window time.Duration) []time.Time {
	kept := l.pruneUnlocked(ip, now, window)
	l.entries[ip] = kept
	return kept
}

func (l *Limiter) pruneUnlocked(ip string, now time.Time, window time.Duration) []time.Time {
	cutoff := now.Add(-window)
	all := l.entries[ip]
	first := 0
	for first < len(all) && !all[first].After(cutoff) {
		first++
	}
	if first == 0 {
		return all
	}
	return append([]time.Time(nil), all[first:]...)
}

func (l *Limiter) evict(now time.Time) {
	if len(l.entries) <= maxEntries {
		return
	}
	cutoff := now.Add(-time.Hour)
	for key, values := range l.entries {
		if len(values) == 0 || !values[len(values)-1].After(cutoff) {
			delete(l.entries, key)
		}
	}
}
