package handler

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/lnsp/wealth/internal/auth"
)

func TestFmtEURAndWriteJSON_SafeOnNaNAndOverflow(t *testing.T) {
	if got := fmtEUR(math.NaN()); got != "0" {
		t.Fatalf("fmtEUR(NaN) = %q, want 0", got)
	}
	if got := fmtEUR(math.Inf(1)); got != "0" {
		t.Fatalf("fmtEUR(+Inf) = %q, want 0", got)
	}
	if got := fmtEUR(1e20); !strings.Contains(got, ".") {
		t.Fatalf("fmtEUR(1e20) = %q, expected formatted digits without stack overflow", got)
	}
	if got := fmtEUR(-1e20); !strings.HasPrefix(got, "-") {
		t.Fatalf("fmtEUR(-1e20) = %q, expected negative formatted string", got)
	}

	// writeJSON on NaN must return 500 JSON error instead of empty 200 OK.
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, map[string]float64{"bad": math.NaN()})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("writeJSON(NaN) status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"error"`) {
		t.Fatalf("writeJSON(NaN) body = %q, expected JSON error payload", rec.Body.String())
	}

	if _, err := parseFiniteFloat("NaN"); err == nil {
		t.Error("parseFiniteFloat(NaN) must fail")
	}
	if _, err := parseFiniteFloat("+Inf"); err == nil {
		t.Error("parseFiniteFloat(+Inf) must fail")
	}
}

func TestExtractClientIP_IPv6AndTrustedProxies(t *testing.T) {
	// Untrusted peer: ignore spoofed X-Forwarded-For and strip TCP port cleanly for IPv4 & IPv6.
	r1 := httptest.NewRequest(http.MethodGet, "/", nil)
	r1.RemoteAddr = "[2001:db8::abcd]:54321"
	r1.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := ExtractClientIP(r1, nil); got != "2001:db8::abcd" {
		t.Fatalf("ExtractClientIP IPv6 untrusted = %q, want 2001:db8::abcd", got)
	}

	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.RemoteAddr = "198.51.100.7:12345"
	r2.Header.Set("X-Forwarded-For", "8.8.8.8")
	if got := ExtractClientIP(r2, ParseTrustedProxies("10.0.0.0/8")); got != "198.51.100.7" {
		t.Fatalf("ExtractClientIP untrusted peer = %q, want 198.51.100.7", got)
	}

	// Trusted reverse proxy: walk XFF right-to-left so client-prepended spoofed IPs are ignored.
	trusted := ParseTrustedProxies("10.0.0.0/8, 172.16.0.1")
	r3 := httptest.NewRequest(http.MethodGet, "/", nil)
	r3.RemoteAddr = "10.0.0.2:44321"
	r3.Header.Set("X-Forwarded-For", "9.9.9.9, 203.0.113.42, 172.16.0.1")
	if got := ExtractClientIP(r3, trusted); got != "203.0.113.42" {
		t.Fatalf("ExtractClientIP trusted XFF = %q, want 203.0.113.42", got)
	}
}

func TestAuthMiddleware_RejectsEmptyUserIDSession(t *testing.T) {
	a := auth.NewSessionOnly("non-default-test-secret")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/portfolio/summary", nil)
	// Mint legacy/god-mode empty-userID session cookie
	a.SetSessionCookieForUser(rec, req, "")
	cookie := rec.Result().Cookies()[0]

	apiReq := httptest.NewRequest(http.MethodGet, "/api/portfolio/summary", nil)
	apiReq.AddCookie(cookie)
	apiRec := httptest.NewRecorder()

	called := false
	mw := AuthMiddlewareWithQueries(a, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	mw.ServeHTTP(apiRec, apiReq)

	if called || apiRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for empty userID session, got status=%d called=%v", apiRec.Code, called)
	}
}

func TestSingleHouseholdScopeHelpers(t *testing.T) {
	if ids := userAccountIDs(httptest.NewRequest(http.MethodGet, "/", nil).Context(), nil); ids != nil {
		t.Fatalf("expected nil userAccountIDs under single-household scope, got %v", ids)
	}
	if !isUserAccount(httptest.NewRequest(http.MethodGet, "/", nil).Context(), nil, uuid.New()) {
		t.Fatal("expected isUserAccount to return true under single-household scope")
	}
}

func TestWebAuthnCeremonyCookieClearedBeforeWriteHeader(t *testing.T) {
	wh := &WebAuthnHandler{sessions: make(map[string]webauthnSessionEntry)}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/passkey/login/finish", nil)
	req.AddCookie(&http.Cookie{Name: webauthnCeremonyCookie, Value: "expired-nonce"})
	rec := httptest.NewRecorder()

	wh.HandleFinishLogin(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on expired ceremony, got %d", rec.Code)
	}
	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == webauthnCeremonyCookie && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("expected webauthn_ceremony cookie deletion header before WriteHeader")
	}
}

func TestValidateOutboundEndpointURL_BlocksCGNATAndZeroNet(t *testing.T) {
	for _, bad := range []string{"http://0.0.0.1/hook", "https://100.64.1.2/hook", "http://127.0.0.1/hook"} {
		if err := validateOutboundEndpointURL(bad); err == nil {
			t.Errorf("expected %s to be rejected", bad)
		}
	}
}
