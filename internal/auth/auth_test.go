package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	// Empty password returns nil (auth disabled)
	a := New("", "secret")
	if a != nil {
		t.Error("expected nil for empty password")
	}

	// Non-empty password returns valid instance
	a = New("mypassword", "secret")
	if a == nil {
		t.Fatal("expected non-nil for non-empty password")
	}
}

func TestCheckPassword(t *testing.T) {
	a := New("correcthorse", "secret")

	if !a.CheckPassword("correcthorse") {
		t.Error("expected correct password to pass")
	}
	if a.CheckPassword("wrongpassword") {
		t.Error("expected wrong password to fail")
	}
	if a.CheckPassword("") {
		t.Error("expected empty password to fail")
	}
}

func TestSessionCookie(t *testing.T) {
	a := New("password", "test-secret-key")

	// Set session cookie
	w := httptest.NewRecorder()
	a.SetSessionCookie(w, httptest.NewRequest("GET", "/", nil))

	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != "session" {
		t.Errorf("expected cookie name 'session', got %q", cookie.Name)
	}
	if !cookie.HttpOnly {
		t.Error("expected HttpOnly flag")
	}

	// Validate the session from a request with the cookie
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(cookie)
	if !a.ValidateSession(req) {
		t.Error("expected session to be valid")
	}

	// Invalid cookie value should fail
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.AddCookie(&http.Cookie{Name: "session", Value: "invalid:token"})
	if a.ValidateSession(req2) {
		t.Error("expected invalid token to fail")
	}

	// No cookie should fail
	req3 := httptest.NewRequest("GET", "/", nil)
	if a.ValidateSession(req3) {
		t.Error("expected missing cookie to fail")
	}
}

func TestClearSessionCookie(t *testing.T) {
	a := New("password", "secret")

	w := httptest.NewRecorder()
	a.ClearSessionCookie(w)

	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
	if cookies[0].MaxAge != -1 {
		t.Errorf("expected MaxAge -1, got %d", cookies[0].MaxAge)
	}
}

func TestTokenExpiry(t *testing.T) {
	a := New("password", "secret")

	// Create a token that expires in the past
	expiry := time.Now().Add(-1 * time.Hour)
	token := a.signToken(expiry)

	if a.verifyToken(token) {
		t.Error("expected expired token to fail verification")
	}

	// Create a token that expires in the future
	expiry2 := time.Now().Add(1 * time.Hour)
	token2 := a.signToken(expiry2)

	if !a.verifyToken(token2) {
		t.Error("expected valid token to pass verification")
	}
}

func TestDifferentSecrets(t *testing.T) {
	a1 := New("password", "secret1")
	a2 := New("password", "secret2")

	// Token from a1 should not be valid for a2
	w := httptest.NewRecorder()
	a1.SetSessionCookie(w, httptest.NewRequest("GET", "/", nil))
	cookie := w.Result().Cookies()[0]

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(cookie)
	if a2.ValidateSession(req) {
		t.Error("expected token signed with different secret to fail")
	}
}
