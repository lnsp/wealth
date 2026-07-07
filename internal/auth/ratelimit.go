package auth

import (
	"sync"
	"time"
)

// LoginLimiter tracks failed login attempts per IP and blocks after too many.
type LoginLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	maxFails int
	window   time.Duration
}

// NewLoginLimiter creates a limiter that allows maxFails attempts per window per IP.
func NewLoginLimiter(maxFails int, window time.Duration) *LoginLimiter {
	return &LoginLimiter{
		attempts: make(map[string][]time.Time),
		maxFails: maxFails,
		window:   window,
	}
}

// Allow checks if the IP is allowed to attempt a login.
// Returns false if rate-limited.
func (l *LoginLimiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-l.window)

	// Prune old attempts
	recent := l.attempts[ip][:0]
	for _, t := range l.attempts[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	l.attempts[ip] = recent

	return len(recent) < l.maxFails
}

// RecordFailure records a failed login attempt for the IP.
func (l *LoginLimiter) RecordFailure(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempts[ip] = append(l.attempts[ip], time.Now())
}

// Reset clears attempts for an IP (called on successful login).
func (l *LoginLimiter) Reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, ip)
}
