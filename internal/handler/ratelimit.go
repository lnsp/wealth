package handler

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// RateLimiter provides per-IP rate limiting with a token bucket algorithm.
type RateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	rate     int           // tokens per window
	window   time.Duration // time window
	cleanTTL time.Duration // cleanup interval
}

type bucket struct {
	tokens    int
	lastReset time.Time
}

// NewRateLimiter creates a rate limiter allowing `rate` requests per `window`.
func NewRateLimiter(rate int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		buckets:  make(map[string]*bucket),
		rate:     rate,
		window:   window,
		cleanTTL: window * 2,
	}
	// Background cleanup of stale entries
	go func() {
		for {
			time.Sleep(rl.cleanTTL)
			rl.mu.Lock()
			now := time.Now()
			for ip, b := range rl.buckets {
				if now.Sub(b.lastReset) > rl.cleanTTL {
					delete(rl.buckets, ip)
				}
			}
			rl.mu.Unlock()
		}
	}()
	return rl
}

// Allow checks if a request from the given key is allowed.
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[key]
	if !ok {
		rl.buckets[key] = &bucket{tokens: rl.rate - 1, lastReset: now}
		return true
	}

	// Reset bucket if window has passed
	if now.Sub(b.lastReset) >= rl.window {
		b.tokens = rl.rate - 1
		b.lastReset = now
		return true
	}

	if b.tokens > 0 {
		b.tokens--
		return true
	}
	return false
}

// RateLimitMiddleware returns HTTP middleware that limits requests per IP.
func RateLimitMiddleware(rl *RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := r.RemoteAddr
			// Use first IP from X-Forwarded-For if behind proxy
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				if first, _, ok := strings.Cut(xff, ","); ok {
					ip = strings.TrimSpace(first)
				} else {
					ip = strings.TrimSpace(xff)
				}
			}
			if !rl.Allow(ip) {
				w.Header().Set("Retry-After", "60")
				http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// UploadRateLimiter is a stricter limiter for file uploads.
func UploadRateLimitMiddleware(rl *RateLimiter) func(http.Handler) http.Handler {
	return RateLimitMiddleware(rl)
}
