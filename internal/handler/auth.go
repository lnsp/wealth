package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"

	"github.com/lnsp/wealth/internal/auth"
	db "github.com/lnsp/wealth/internal/database/generated"
)

type AuthHandler struct {
	auth      *auth.Auth
	limiter   *auth.LoginLimiter
	queries   *db.Queries
	cryptoKey []byte
}

func NewAuthHandler(a *auth.Auth, q *db.Queries, sessionSecret string) *AuthHandler {
	return &AuthHandler{
		auth:      a,
		limiter:   auth.NewLoginLimiter(5, 15*time.Minute),
		queries:   q,
		cryptoKey: auth.DeriveKey(sessionSecret),
	}
}

// clientIP extracts the client IP from the request, respecting X-Forwarded-For.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if ip, _, ok := strings.Cut(xff, ","); ok {
			return strings.TrimSpace(ip)
		}
		return strings.TrimSpace(xff)
	}
	if ip, _, ok := strings.Cut(r.RemoteAddr, ":"); ok {
		return ip
	}
	return r.RemoteAddr
}

func (h *AuthHandler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)

	// Rate limit: block after 5 failed attempts in 15 minutes
	if !h.limiter.Allow(ip) {
		w.Header().Set("Retry-After", "900")
		writeError(w, http.StatusTooManyRequests, "too many login attempts, try again later")
		return
	}

	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		TOTPCode string `json:"totp_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	authenticated := false
	userID := ""

	// Try user-based auth first (username + password from users table)
	if body.Username != "" && h.queries != nil {
		user, err := h.queries.GetUserByUsername(r.Context(), body.Username)
		if err == nil && user.IsActive {
			if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(body.Password)) == nil {
				// Check TOTP if enabled
				if user.TOTPEnabled {
					secret, decErr := auth.DecryptSecret(user.TOTPSecret.String, h.cryptoKey)
					if decErr != nil || body.TOTPCode == "" || !totp.Validate(body.TOTPCode, secret) {
						writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "totp_required", "totp_required": true})
						return
					}
				}
				authenticated = true
				userID = user.ID.String()
			}
		}
	}

	// Fall back to legacy single-password auth (use first admin user)
	if !authenticated && h.auth.CheckPassword(body.Password) {
		authenticated = true
		if h.queries != nil {
			users, err := h.queries.ListUsers(r.Context())
			if err == nil && len(users) > 0 {
				userID = users[0].ID.String()
			}
		}
	}

	if !authenticated {
		h.limiter.RecordFailure(ip)
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	h.limiter.Reset(ip)
	h.auth.SetSessionCookieForUser(w, r, userID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *AuthHandler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	h.auth.ClearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *AuthHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	authenticated := h.auth.ValidateSession(r)
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": authenticated, "required": true})
}

type contextKey string
const userIDContextKey contextKey = "user_id"

// UserIDFromContext extracts the user ID set by AuthMiddleware.
func UserIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(userIDContextKey).(string); ok {
		return v
	}
	return ""
}

// AuthMiddleware returns a Chi middleware that requires a valid session.
// It also sets the user_id in the request context for downstream handlers.
func AuthMiddleware(a *auth.Auth) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if a.ValidateSession(r) {
				// Set user_id in context
				userID := a.UserIDFromRequest(r)
				if userID != "" {
					ctx := context.WithValue(r.Context(), userIDContextKey, userID)
					r = r.WithContext(ctx)
				}
				next.ServeHTTP(w, r)
				return
			}
			writeError(w, http.StatusUnauthorized, "authentication required")
		})
	}
}
