package auth

import (
	"testing"
	"time"
)

func TestLoginLimiter_AllowsUnderLimit(t *testing.T) {
	l := NewLoginLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !l.Allow("1.2.3.4") {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
		l.RecordFailure("1.2.3.4")
	}
}

func TestLoginLimiter_BlocksAfterLimit(t *testing.T) {
	l := NewLoginLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		l.RecordFailure("1.2.3.4")
	}
	if l.Allow("1.2.3.4") {
		t.Fatal("should be blocked after 3 failures")
	}
}

func TestLoginLimiter_DifferentIPsIndependent(t *testing.T) {
	l := NewLoginLimiter(2, time.Minute)
	l.RecordFailure("1.1.1.1")
	l.RecordFailure("1.1.1.1")
	if l.Allow("1.1.1.1") {
		t.Fatal("1.1.1.1 should be blocked")
	}
	if !l.Allow("2.2.2.2") {
		t.Fatal("2.2.2.2 should be allowed (different IP)")
	}
}

func TestLoginLimiter_ResetClearsAttempts(t *testing.T) {
	l := NewLoginLimiter(2, time.Minute)
	l.RecordFailure("1.2.3.4")
	l.RecordFailure("1.2.3.4")
	if l.Allow("1.2.3.4") {
		t.Fatal("should be blocked")
	}
	l.Reset("1.2.3.4")
	if !l.Allow("1.2.3.4") {
		t.Fatal("should be allowed after reset")
	}
}

func TestLoginLimiter_ExpiredAttemptsIgnored(t *testing.T) {
	l := NewLoginLimiter(2, 50*time.Millisecond)
	l.RecordFailure("1.2.3.4")
	l.RecordFailure("1.2.3.4")
	if l.Allow("1.2.3.4") {
		t.Fatal("should be blocked immediately")
	}
	time.Sleep(60 * time.Millisecond)
	if !l.Allow("1.2.3.4") {
		t.Fatal("should be allowed after window expires")
	}
}
