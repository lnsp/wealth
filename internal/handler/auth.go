package handler

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"

	"github.com/lnsp/wealth/internal/auth"
	db "github.com/lnsp/wealth/internal/database/generated"
)

type AuthHandler struct {
	auth           *auth.Auth
	limiter        *auth.LoginLimiter
	queries        *db.Queries
	cryptoKey      []byte
	trustedProxies []*net.IPNet
}

func NewAuthHandler(a *auth.Auth, q *db.Queries, sessionSecret string) *AuthHandler {
	return &AuthHandler{
		auth:      a,
		limiter:   auth.NewLoginLimiter(5, 15*time.Minute),
		queries:   q,
		cryptoKey: auth.DeriveKey(sessionSecret),
	}
}

// SetTrustedProxies configures trusted proxy CIDRs for X-Forwarded-For extraction.
func (h *AuthHandler) SetTrustedProxies(trusted []*net.IPNet) {
	h.trustedProxies = trusted
}

func (h *AuthHandler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	ip := ExtractClientIP(r, h.trustedProxies)

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

	username := strings.TrimSpace(body.Username)
	if username == "" {
		// Single-user / default admin convenience when username is left blank.
		username = "admin"
	}

	authenticated := false
	userID := ""

	if h.queries != nil {
		user, err := h.queries.GetUserByUsername(r.Context(), username)
		if err == nil && user.IsActive {
			if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(body.Password)) == nil {
				if user.TOTPEnabled {
					if strings.TrimSpace(body.TOTPCode) == "" {
						writeJSON(w, http.StatusUnauthorized, map[string]any{
							"error":         "totp_required",
							"totp_required": true,
						})
						return
					}
					secret, decErr := auth.DecryptSecret(user.TOTPSecret.String, h.cryptoKey)
					if decErr != nil || !totp.Validate(strings.TrimSpace(body.TOTPCode), secret) {
						h.limiter.RecordFailure(ip)
						writeJSON(w, http.StatusUnauthorized, map[string]any{
							"error":         "invalid_totp",
							"totp_required": true,
						})
						return
					}
				}
				authenticated = true
				userID = user.ID.String()
			}
		}
	}

	if !authenticated || userID == "" {
		h.limiter.RecordFailure(ip)
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	h.limiter.Reset(ip)
	h.auth.SetSessionCookieForUser(w, r, userID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *AuthHandler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	h.auth.ClearSessionCookieWithRequest(w, r)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func isSessionActive(ctx context.Context, a *auth.Auth, q *db.Queries, r *http.Request) (string, bool) {
	if a == nil || !a.ValidateSession(r) {
		return "", false
	}
	userID := a.UserIDFromRequest(r)
	if userID == "" {
		return "", false
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		return "", false
	}
	if q != nil {
		user, err := q.GetUserByID(ctx, uid)
		if err != nil || !user.IsActive {
			return "", false
		}
	}
	return userID, true
}

func (h *AuthHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	_, authenticated := isSessionActive(r.Context(), h.auth, h.queries, r)
	if !authenticated {
		if _, err := r.Cookie("session"); err == nil {
			h.auth.ClearSessionCookieWithRequest(w, r)
		}
	}
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
func AuthMiddleware(a *auth.Auth) func(http.Handler) http.Handler {
	return AuthMiddlewareWithQueries(a, nil)
}

// AuthMiddlewareWithQueries returns a Chi middleware that requires a valid session
// belonging to an existing, active user in the database.
func AuthMiddlewareWithQueries(a *auth.Auth, q *db.Queries) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if userID, ok := isSessionActive(r.Context(), a, q, r); ok {
				ctx := context.WithValue(r.Context(), userIDContextKey, userID)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			if a != nil {
				a.ClearSessionCookieWithRequest(w, r)
			}
			writeError(w, http.StatusUnauthorized, "authentication required")
		})
	}
}
