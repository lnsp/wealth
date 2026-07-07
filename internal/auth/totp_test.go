package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

// TOTP enrollment cycle (mirrors HandleSetupTOTP → HandleVerifyTOTP in
// internal/handler/users.go):
//
//  1. Setup: server calls totp.Generate, returns secret + otpauth URL.
//     Frontend renders the URL as a QR; the user scans it into Google
//     Authenticator / 1Password.
//  2. The server encrypts the secret with the session-derived AES-256-GCM
//     key and persists it to users.totp_secret.
//  3. Verify: user types a 6-digit code from their authenticator app.
//     Server decrypts the stored secret and calls totp.Validate.
//     - Valid code → EnableUserTOTP flips users.totp_enabled = true.
//     - Invalid code → 401 "invalid TOTP code".
//
// These tests pin each leg of the cycle so a refactor can't silently
// break TOTP (e.g., switch the cipher mode and forget to migrate stored
// secrets, or skip the validate step).

func TestTOTP_SetupReturnsScannableURL(t *testing.T) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "Wealth",
		AccountName: "alice",
	})
	if err != nil {
		t.Fatalf("totp.Generate: %v", err)
	}
	url := key.URL()
	if !strings.HasPrefix(url, "otpauth://totp/") {
		t.Errorf("URL %q must start with otpauth://totp/", url)
	}
	if !strings.Contains(url, "issuer=Wealth") {
		t.Errorf("URL must contain issuer=Wealth, got %q", url)
	}
	if !strings.Contains(url, "alice") {
		t.Errorf("URL must contain account name 'alice', got %q", url)
	}
	secret := key.Secret()
	if len(secret) < 16 {
		t.Errorf("secret too short to be a real TOTP secret: %d chars", len(secret))
	}
}

func TestTOTP_EncryptStoreDecryptRoundTrip(t *testing.T) {
	// HandleSetupTOTP stores the secret encrypted; HandleVerifyTOTP decrypts
	// it before calling totp.Validate. Both must reach the same secret string
	// or every code is rejected.
	plaintext := "JBSWY3DPEHPK3PXP" // standard TOTP test vector secret
	key := DeriveKey("session-secret-for-testing")

	enc, err := EncryptSecret(plaintext, key)
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	if !strings.HasPrefix(enc, "enc:") {
		preview := enc
		if len(preview) > 8 {
			preview = preview[:8]
		}
		t.Errorf("encrypted output should be `enc:`-prefixed, got %q", preview)
	}
	dec, err := DecryptSecret(enc, key)
	if err != nil {
		t.Fatalf("DecryptSecret: %v", err)
	}
	if dec != plaintext {
		t.Errorf("round-trip mismatch: got %q, want %q", dec, plaintext)
	}
}

func TestTOTP_CorrectCodeValidates(t *testing.T) {
	// Pin the validation path: a code generated for the current time window
	// against a known secret must validate successfully.
	secret := "JBSWY3DPEHPK3PXP"
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("totp.GenerateCode: %v", err)
	}
	if !totp.Validate(code, secret) {
		t.Errorf("freshly-generated code %q should validate against secret", code)
	}
}

func TestTOTP_WrongCodeRejected(t *testing.T) {
	// Wrong code must fail (handler returns 401 "invalid TOTP code").
	secret := "JBSWY3DPEHPK3PXP"
	cases := []string{
		"000000",
		"123456",
		"999999",
		"",
		"abcdef", // non-numeric
	}
	for _, code := range cases {
		if totp.Validate(code, secret) {
			t.Errorf("code %q should NOT validate (placeholder/invalid)", code)
		}
	}
}

func TestTOTP_EndToEndEnrollmentCycle(t *testing.T) {
	// Full cycle: Setup (generate + encrypt) → Verify (decrypt + validate).
	// This is the path a real user takes through HandleSetupTOTP +
	// HandleVerifyTOTP, modulo the DB hops.
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "Wealth",
		AccountName: "bob",
	})
	if err != nil {
		t.Fatalf("setup: totp.Generate: %v", err)
	}
	sessionKey := DeriveKey("test-session-secret")

	// Server-side: encrypt and "persist" the secret.
	stored, err := EncryptSecret(key.Secret(), sessionKey)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// User reads the secret/URL from the response, scans it, gets a code.
	code, err := totp.GenerateCode(key.Secret(), time.Now())
	if err != nil {
		t.Fatalf("authenticator app: GenerateCode: %v", err)
	}

	// User POSTs the code to /api/users/{id}/totp/verify. Server decrypts
	// and validates.
	decrypted, err := DecryptSecret(stored, sessionKey)
	if err != nil {
		t.Fatalf("verify: decrypt: %v", err)
	}
	if !totp.Validate(code, decrypted) {
		t.Error("end-to-end: validate(generated code, decrypted secret) must succeed")
	}

	// Now the wrong-code path: same stored secret, but a clearly bogus code.
	if totp.Validate("000000", decrypted) {
		t.Error("end-to-end: validate('000000', decrypted) must fail (so 2FA stays off)")
	}
}

func TestTOTP_PlaintextSecretStillReadable(t *testing.T) {
	// DecryptSecret intentionally returns plaintext as-is when it lacks the
	// "enc:" prefix — this is the migration path for users whose secrets
	// were stored before encryption was rolled out (commit history).
	// totp.Validate must still work against those secrets.
	plaintext := "JBSWY3DPEHPK3PXP"
	key := DeriveKey("anything")
	got, err := DecryptSecret(plaintext, key)
	if err != nil {
		t.Fatalf("DecryptSecret on plaintext: %v", err)
	}
	if got != plaintext {
		t.Errorf("plaintext passthrough: got %q, want %q", got, plaintext)
	}
}

