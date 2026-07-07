package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookieName = "session"
	sessionDuration   = 30 * 24 * time.Hour // 30 days
)

// Auth handles password verification and session management.
type Auth struct {
	passwordHash []byte
	sessionKey   []byte
}

// New creates an Auth instance. If password is empty, auth is disabled.
// Session secret must be at least 16 bytes for HMAC security.
func New(password, sessionSecret string) *Auth {
	if password == "" {
		return nil
	}
	if len(sessionSecret) < 16 {
		// Pad short secrets with hash to ensure minimum HMAC key length
		h := sha256.Sum256([]byte(sessionSecret))
		sessionSecret = hex.EncodeToString(h[:])
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		panic(fmt.Sprintf("hash password: %v", err))
	}
	return &Auth{
		passwordHash: hash,
		sessionKey:   []byte(sessionSecret),
	}
}

// CheckPassword verifies a password against the stored hash.
func (a *Auth) CheckPassword(password string) bool {
	return bcrypt.CompareHashAndPassword(a.passwordHash, []byte(password)) == nil
}

// SetSessionCookie creates a signed session cookie on the response.
func (a *Auth) SetSessionCookie(w http.ResponseWriter, r *http.Request) {
	a.SetSessionCookieForUser(w, r, "")
}

// SetSessionCookieForUser creates a session cookie embedding the user ID.
func (a *Auth) SetSessionCookieForUser(w http.ResponseWriter, r *http.Request, userID string) {
	expiry := time.Now().Add(sessionDuration)
	token := a.signTokenWithUser(expiry, userID)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiry,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
	})
}

// isHTTPS detects if the request was made over HTTPS, checking TLS state
// and the X-Forwarded-Proto header (set by reverse proxies like Caddy/nginx).
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// ClearSessionCookie removes the session cookie.
func (a *Auth) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// ValidateSession checks if the request has a valid session cookie.
func (a *Auth) ValidateSession(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	return a.verifyToken(cookie.Value)
}

// signTokenWithUser creates a signed token: "user_id|expiry_unix:hmac_signature"
func (a *Auth) signTokenWithUser(expiry time.Time, userID string) string {
	payload := fmt.Sprintf("%s|%d", userID, expiry.Unix())
	mac := hmac.New(sha256.New, a.sessionKey)
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	return payload + ":" + sig
}

// signToken creates a legacy token (backward compatible).
func (a *Auth) signToken(expiry time.Time) string {
	return a.signTokenWithUser(expiry, "")
}

// verifyToken checks the HMAC signature and expiry. Returns (valid, userID).
func (a *Auth) verifyToken(token string) bool {
	_, ok := a.VerifyTokenWithUser(token)
	return ok
}

// VerifyTokenWithUser checks the token and extracts the user ID.
func (a *Auth) VerifyTokenWithUser(token string) (string, bool) {
	parts := strings.SplitN(token, ":", 2)
	if len(parts) != 2 {
		return "", false
	}
	payload, sig := parts[0], parts[1]

	mac := hmac.New(sha256.New, a.sessionKey)
	mac.Write([]byte(payload))
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expectedSig)) {
		return "", false
	}

	// Parse payload: "user_id|expiry" or legacy "expiry"
	userID := ""
	var expiryUnix int64
	if idx := strings.LastIndex(payload, "|"); idx >= 0 {
		userID = payload[:idx]
		if _, err := fmt.Sscanf(payload[idx+1:], "%d", &expiryUnix); err != nil {
			return "", false
		}
	} else {
		if _, err := fmt.Sscanf(payload, "%d", &expiryUnix); err != nil {
			return "", false
		}
	}

	if time.Now().Unix() >= expiryUnix {
		return "", false
	}
	return userID, true
}

// UserIDFromRequest extracts the user ID from the session cookie.
func (a *Auth) UserIDFromRequest(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	userID, ok := a.VerifyTokenWithUser(cookie.Value)
	if !ok {
		return ""
	}
	return userID
}
