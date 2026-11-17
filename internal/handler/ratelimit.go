package handler

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const maxRateLimitBuckets = 10000

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
		if len(rl.buckets) >= maxRateLimitBuckets {
			for ip, entry := range rl.buckets {
				if now.Sub(entry.lastReset) >= rl.window {
					delete(rl.buckets, ip)
				}
			}
			if len(rl.buckets) >= maxRateLimitBuckets {
				for ip := range rl.buckets {
					delete(rl.buckets, ip)
					break
				}
			}
		}
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

// ParseTrustedProxies parses a comma-separated list of CIDRs or IP addresses.
func ParseTrustedProxies(csv string) []*net.IPNet {
	if strings.TrimSpace(csv) == "" {
		return nil
	}
	var nets []*net.IPNet
	for _, raw := range strings.Split(csv, ",") {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if _, ipNet, err := net.ParseCIDR(s); err == nil {
			nets = append(nets, ipNet)
			continue
		}
		if ip := net.ParseIP(s); ip != nil {
			bits := 128
			if v4 := ip.To4(); v4 != nil {
				ip = v4
				bits = 32
			}
			nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		}
	}
	return nets
}

func isIPTrusted(ipStr string, trusted []*net.IPNet) bool {
	if len(trusted) == 0 {
		return false
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	for _, n := range trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ExtractClientIP extracts the normalized client IP without port, trusting
// X-Forwarded-For only when the immediate peer IP belongs to trusted.
func ExtractClientIP(r *http.Request, trusted []*net.IPNet) string {
	peer := strings.TrimSpace(r.RemoteAddr)
	if host, _, err := net.SplitHostPort(peer); err == nil {
		peer = host
	}
	peer = strings.Trim(peer, "[]")

	if isIPTrusted(peer, trusted) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			// Walk right-to-left: return the rightmost valid IP that is not a trusted proxy.
			for i := len(parts) - 1; i >= 0; i-- {
				candidate := strings.TrimSpace(parts[i])
				if host, _, err := net.SplitHostPort(candidate); err == nil {
					candidate = host
				}
				candidate = strings.Trim(candidate, "[]")
				if net.ParseIP(candidate) == nil {
					continue
				}
				if !isIPTrusted(candidate, trusted) || i == 0 {
					return candidate
				}
			}
		}
	}
	return peer
}

// RateLimitMiddleware returns HTTP middleware that limits requests per IP.
func RateLimitMiddleware(rl *RateLimiter) func(http.Handler) http.Handler {
	return RateLimitMiddlewareWithProxies(rl, nil)
}

// RateLimitMiddlewareWithProxies returns HTTP middleware that limits requests per IP,
// honoring X-Forwarded-For only from trusted proxy CIDRs.
func RateLimitMiddlewareWithProxies(rl *RateLimiter, trusted []*net.IPNet) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := ExtractClientIP(r, trusted)
			if !rl.Allow(ip) {
				w.Header().Set("Retry-After", "60")
				http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// UploadRateLimitMiddleware is a stricter limiter for file uploads.
func UploadRateLimitMiddleware(rl *RateLimiter) func(http.Handler) http.Handler {
	return RateLimitMiddleware(rl)
}
