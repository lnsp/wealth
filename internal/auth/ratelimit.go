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

	existing, ok := l.attempts[ip]
	if !ok {
		return true
	}

	now := time.Now()
	cutoff := now.Add(-l.window)

	// Prune old attempts
	recent := existing[:0]
	for _, t := range existing {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) == 0 {
		delete(l.attempts, ip)
		return true
	}
	l.attempts[ip] = recent

	return len(recent) < l.maxFails
}

const maxTrackedLoginIPs = 4096

// RecordFailure records a failed login attempt for the IP.
func (l *LoginLimiter) RecordFailure(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-l.window)

	if len(l.attempts) >= maxTrackedLoginIPs/2 {
		for k, times := range l.attempts {
			recent := times[:0]
			for _, t := range times {
				if t.After(cutoff) {
					recent = append(recent, t)
				}
			}
			if len(recent) == 0 {
				delete(l.attempts, k)
			} else {
				l.attempts[k] = recent
			}
		}
	}
	if len(l.attempts) >= maxTrackedLoginIPs {
		if _, exists := l.attempts[ip]; !exists {
			// Evict the IP whose latest attempt is oldest.
			var oldestKey string
			var oldestTime time.Time
			for k, times := range l.attempts {
				last := times[len(times)-1]
				if oldestKey == "" || last.Before(oldestTime) {
					oldestKey = k
					oldestTime = last
				}
			}
			if oldestKey != "" {
				delete(l.attempts, oldestKey)
			}
		}
	}

	l.attempts[ip] = append(l.attempts[ip], now)
}

// Reset clears attempts for an IP (called on successful login).
func (l *LoginLimiter) Reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, ip)
}
