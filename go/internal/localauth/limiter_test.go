package localauth

import (
	"testing"
	"time"
)

type clock struct{ at time.Time }

func (c *clock) now() time.Time          { return c.at }
func (c *clock) advance(d time.Duration) { c.at = c.at.Add(d) }

func TestLimiterAllowsThenThrottlesPerMinute(t *testing.T) {
	c := &clock{at: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	l := NewLimiter(c.now)
	for i := 0; i < IPMinuteLimit; i++ {
		if ok, _ := l.Allow("192.0.2.10"); !ok {
			t.Fatalf("attempt %d was throttled early", i+1)
		}
		l.Record("192.0.2.10")
	}
	ok, wait := l.Allow("192.0.2.10")
	if ok || wait <= 0 || wait > time.Minute {
		t.Fatalf("expected minute throttle, got ok=%v wait=%v", ok, wait)
	}
	c.advance(time.Minute)
	if ok, _ := l.Allow("192.0.2.10"); !ok {
		t.Fatal("window did not slide")
	}
	if ok, _ := l.Allow("192.0.2.11"); !ok {
		t.Fatal("another source was throttled")
	}
}

func TestLimiterThrottlesPerHour(t *testing.T) {
	c := &clock{at: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	l := NewLimiter(c.now)
	for i := 0; i < IPHourLimit; i++ {
		c.advance(2 * time.Second)
		l.Record("192.0.2.20")
	}
	ok, wait := l.Allow("192.0.2.20")
	if ok || wait <= 0 || wait > time.Hour {
		t.Fatalf("expected hour throttle, got ok=%v wait=%v", ok, wait)
	}
	c.advance(time.Hour)
	if ok, _ := l.Allow("192.0.2.20"); !ok {
		t.Fatal("hour window did not slide")
	}
}

func TestProbeThresholdSuppressesLockReveal(t *testing.T) {
	c := &clock{at: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	l := NewLimiter(c.now)
	for i := 0; i < ProbeLimit-1; i++ {
		l.Record("192.0.2.30")
	}
	if l.ProbeExceeded("192.0.2.30") {
		t.Fatal("probe threshold reached too early")
	}
	l.Record("192.0.2.30")
	if !l.ProbeExceeded("192.0.2.30") {
		t.Fatal("probe threshold not reached")
	}
	c.advance(ProbeWindow)
	if l.ProbeExceeded("192.0.2.30") {
		t.Fatal("probe window did not slide")
	}
}
